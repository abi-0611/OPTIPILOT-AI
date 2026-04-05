//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// ── REST API tests ────────────────────────────────────────────────────────────
//
// All tests in this file port-forward the cluster-agent service on port 8090
// (dashboard/API) and make real HTTP requests. They verify:
//   - HTTP endpoints exist and return expected status codes
//   - Response bodies contain required JSON fields
//   - Non-existent resource paths return 404 (not 500)

func withAPIPortForward(t *testing.T, fn func()) {
	t.Helper()
	cleanup := portForwardSvc(t, helmNamespace, clusterAgentSvcName(), localAPIPort, 8090)
	defer cleanup()
	fn()
}

func withMetricsPortForward(t *testing.T, fn func()) {
	t.Helper()
	cleanup := portForwardSvc(t, helmNamespace, clusterAgentSvcName(), localMetricsPort, 8080)
	defer cleanup()
	fn()
}

// TestE2E_API_MetricsEndpointReachable verifies /metrics returns HTTP 200 and
// contains the standard "go_goroutines" metric emitted by the Go runtime.
func TestE2E_API_MetricsEndpointReachable(t *testing.T) {
	requireSetup(t)
	withMetricsPortForward(t, func() {
		code := httpGet(t, metricsURL("/metrics"))
		if code != http.StatusOK {
			t.Errorf("GET /metrics: status %d, want 200", code)
		}
		body := httpBody(t, metricsURL("/metrics"))
		if !strings.Contains(body, "go_goroutines") {
			t.Error("/metrics body does not contain go_goroutines")
		}
	})
}

// TestE2E_API_DecisionsEndpointReturns200 verifies GET /api/v1/decisions
// returns HTTP 200.
func TestE2E_API_DecisionsEndpointReturns200(t *testing.T) {
	requireSetup(t)
	withAPIPortForward(t, func() {
		code := httpGet(t, apiURL("/api/v1/decisions"))
		if code != http.StatusOK {
			t.Errorf("GET /api/v1/decisions: status %d, want 200", code)
		}
	})
}

// TestE2E_API_DecisionsResponseIsValidJSON verifies the response body from
// GET /api/v1/decisions is parseable JSON with a "decisions" field.
func TestE2E_API_DecisionsResponseIsValidJSON(t *testing.T) {
	requireSetup(t)
	withAPIPortForward(t, func() {
		body := httpBody(t, apiURL("/api/v1/decisions"))
		var resp map[string]interface{}
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatalf("response is not valid JSON: %v\nbody: %s", err, body)
		}
		if _, ok := resp["decisions"]; !ok {
			t.Error("response JSON does not contain 'decisions' key")
		}
	})
}

// TestE2E_API_DecisionsSummaryEndpoint verifies GET /api/v1/decisions/summary
// returns HTTP 200.
func TestE2E_API_DecisionsSummaryEndpoint(t *testing.T) {
	requireSetup(t)
	withAPIPortForward(t, func() {
		code := httpGet(t, apiURL("/api/v1/decisions/summary"))
		if code != http.StatusOK {
			t.Errorf("GET /api/v1/decisions/summary: status %d, want 200", code)
		}
	})
}

// TestE2E_API_DecisionsSearchEndpoint verifies GET /api/v1/decisions/search
// does not return 404 or 500.
func TestE2E_API_DecisionsSearchEndpoint(t *testing.T) {
	requireSetup(t)
	withAPIPortForward(t, func() {
		code := httpGet(t, apiURL("/api/v1/decisions/search"))
		if code == http.StatusNotFound || code >= 500 {
			t.Errorf("GET /api/v1/decisions/search: unexpected status %d", code)
		}
	})
}

// TestE2E_API_NonExistentDecisionReturns404 verifies a GET for a decision ID
// that does not exist returns HTTP 404.
func TestE2E_API_NonExistentDecisionReturns404(t *testing.T) {
	requireSetup(t)
	withAPIPortForward(t, func() {
		code := httpGet(t, apiURL("/api/v1/decisions/dec-does-not-exist"))
		if code != http.StatusNotFound {
			t.Errorf("GET /api/v1/decisions/dec-does-not-exist: status %d, want 404", code)
		}
	})
}

// TestE2E_API_SimulateEndpointExists verifies POST /api/v1/simulate is
// registered (returns 200, 400, or 422 — NOT 404 or 500).
func TestE2E_API_SimulateEndpointExists(t *testing.T) {
	requireSetup(t)
	withAPIPortForward(t, func() {
		resp, err := http.Post(apiURL("/api/v1/simulate"), //nolint:noctx
			"application/json",
			strings.NewReader(`{"service":"test","namespace":"default"}`))
		if err != nil {
			t.Fatalf("POST /api/v1/simulate: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode >= 500 {
			t.Errorf("POST /api/v1/simulate: unexpected status %d", resp.StatusCode)
		}
	})
}

// TestE2E_API_SLOCostCurveEndpointExists verifies POST /api/v1/simulate/slo-cost-curve
// is registered.
func TestE2E_API_SLOCostCurveEndpointExists(t *testing.T) {
	requireSetup(t)
	withAPIPortForward(t, func() {
		resp, err := http.Post(apiURL("/api/v1/simulate/slo-cost-curve"), //nolint:noctx
			"application/json",
			strings.NewReader(`{"service":"test","namespace":"default"}`))
		if err != nil {
			t.Fatalf("POST /api/v1/simulate/slo-cost-curve: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode >= 500 {
			t.Errorf("POST /api/v1/simulate/slo-cost-curve: unexpected status %d", resp.StatusCode)
		}
	})
}

// TestE2E_API_CORSHeaderPresent verifies the API server emits the
// Access-Control-Allow-Origin header required for dashboard operation.
func TestE2E_API_CORSHeaderPresent(t *testing.T) {
	requireSetup(t)
	withAPIPortForward(t, func() {
		resp, err := http.Get(apiURL("/api/v1/decisions")) //nolint:noctx
		if err != nil {
			t.Fatalf("GET /api/v1/decisions: %v", err)
		}
		defer resp.Body.Close()
		if origin := resp.Header.Get("Access-Control-Allow-Origin"); origin == "" {
			t.Error("Access-Control-Allow-Origin header missing")
		}
	})
}

// TestE2E_API_QueryParamLimit verifies the ?limit= query parameter is honoured
// (does not cause a 400 for a valid integer value).
func TestE2E_API_QueryParamLimit(t *testing.T) {
	requireSetup(t)
	withAPIPortForward(t, func() {
		url := fmt.Sprintf("%s?limit=5", apiURL("/api/v1/decisions"))
		code := httpGet(t, url)
		if code != http.StatusOK {
			t.Errorf("GET /api/v1/decisions?limit=5: status %d, want 200", code)
		}
	})
}

// TestE2E_API_InvalidLimitReturnsBadRequest verifies the API returns 400 for
// a non-integer limit value.
func TestE2E_API_InvalidLimitReturnsBadRequest(t *testing.T) {
	requireSetup(t)
	withAPIPortForward(t, func() {
		url := apiURL("/api/v1/decisions") + "?limit=notanumber"
		code := httpGet(t, url)
		if code != http.StatusBadRequest {
			t.Errorf("GET ?limit=notanumber: status %d, want 400", code)
		}
	})
}

// ── Helpers ────────────────────────────────────────────────────────────────────

// httpBody returns the response body of an HTTP GET request as a string.
func httpBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body from %s: %v", url, err)
	}
	return string(b)
}
