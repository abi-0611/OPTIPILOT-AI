package metrics_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/optipilot-ai/optipilot/internal/metrics"
)

// prometheusVectorResponse builds a minimal Prometheus instant-query JSON response
// holding a single vector sample with the given value string.
func prometheusVectorResponse(value string) []byte {
	resp := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"resultType": "vector",
			"result": []interface{}{
				map[string]interface{}{
					"metric": map[string]string{},
					"value":  []interface{}{1609459200.0, value},
				},
			},
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

func prometheusEmptyResponse() []byte {
	resp := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"resultType": "vector",
			"result":     []interface{}{},
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

func prometheusMultiResponse() []byte {
	resp := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"resultType": "vector",
			"result": []interface{}{
				map[string]interface{}{
					"metric": map[string]string{"service": "a"},
					"value":  []interface{}{1609459200.0, "0.5"},
				},
				map[string]interface{}{
					"metric": map[string]string{"service": "b"},
					"value":  []interface{}{1609459200.0, "0.3"},
				},
			},
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

func prometheusRangeResponse(values [][2]interface{}) []byte {
	resp := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"resultType": "matrix",
			"result": []interface{}{
				map[string]interface{}{
					"metric": map[string]string{},
					"values": values,
				},
			},
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

// TestHTTPPrometheusClient_Query_Success verifies a normal scalar result.
func TestHTTPPrometheusClient_Query_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(prometheusVectorResponse("0.150"))
	}))
	defer srv.Close()

	c := metrics.NewHTTPPrometheusClient(srv.URL, 5*time.Second, 5*time.Second)
	val, err := c.Query(context.Background(), `http_request_duration_seconds`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != 0.150 {
		t.Errorf("expected 0.150, got %v", val)
	}
}

// TestHTTPPrometheusClient_Query_Caching verifies that a second call within TTL uses cached value.
func TestHTTPPrometheusClient_Query_Caching(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(prometheusVectorResponse("0.200"))
	}))
	defer srv.Close()

	c := metrics.NewHTTPPrometheusClient(srv.URL, 5*time.Second, 10*time.Second)
	q := `test_query`

	if _, err := c.Query(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Query(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 HTTP call (cache hit), got %d", callCount)
	}
}

// TestHTTPPrometheusClient_Query_NoData verifies ErrNoData on empty result.
func TestHTTPPrometheusClient_Query_NoData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(prometheusEmptyResponse())
	}))
	defer srv.Close()

	c := metrics.NewHTTPPrometheusClient(srv.URL, 5*time.Second, 5*time.Second)
	_, err := c.Query(context.Background(), `missing_metric`)
	if err != metrics.ErrNoData {
		t.Errorf("expected ErrNoData, got %v", err)
	}
}

// TestHTTPPrometheusClient_Query_MultipleResults verifies ErrMultipleResults.
func TestHTTPPrometheusClient_Query_MultipleResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(prometheusMultiResponse())
	}))
	defer srv.Close()

	c := metrics.NewHTTPPrometheusClient(srv.URL, 5*time.Second, 5*time.Second)
	_, err := c.Query(context.Background(), `ambiguous_metric`)
	if err != metrics.ErrMultipleResults {
		t.Errorf("expected ErrMultipleResults, got %v", err)
	}
}

// TestHTTPPrometheusClient_Query_ServerDown verifies error when Prometheus is down.
func TestHTTPPrometheusClient_Query_ServerDown(t *testing.T) {
	c := metrics.NewHTTPPrometheusClient("http://127.0.0.1:19999", 1*time.Second, 5*time.Second)
	_, err := c.Query(context.Background(), `any_metric`)
	if err == nil {
		t.Error("expected error when server is down, got nil")
	}
}

// TestHTTPPrometheusClient_Query_HTTPError verifies error on non-200 status.
func TestHTTPPrometheusClient_Query_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("service unavailable"))
	}))
	defer srv.Close()

	c := metrics.NewHTTPPrometheusClient(srv.URL, 5*time.Second, 5*time.Second)
	_, err := c.Query(context.Background(), `test`)
	if err == nil {
		t.Error("expected error on HTTP 503, got nil")
	}
}

// TestHTTPPrometheusClient_Query_MalformedJSON verifies error on malformed response.
func TestHTTPPrometheusClient_Query_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not valid json`))
	}))
	defer srv.Close()

	c := metrics.NewHTTPPrometheusClient(srv.URL, 5*time.Second, 5*time.Second)
	_, err := c.Query(context.Background(), `test`)
	if err == nil {
		t.Error("expected error on malformed JSON, got nil")
	}
}

// TestHTTPPrometheusClient_QueryRange_Success verifies range query parsing.
func TestHTTPPrometheusClient_QueryRange_Success(t *testing.T) {
	now := time.Unix(1609459200, 0)
	values := [][2]interface{}{
		{float64(now.Unix()), "0.100"},
		{float64(now.Add(30 * time.Second).Unix()), "0.150"},
		{float64(now.Add(60 * time.Second).Unix()), "0.200"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(prometheusRangeResponse(values))
	}))
	defer srv.Close()

	c := metrics.NewHTTPPrometheusClient(srv.URL, 5*time.Second, 5*time.Second)
	points, err := c.QueryRange(context.Background(), `test_range`, now, now.Add(time.Minute), 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(points) != 3 {
		t.Errorf("expected 3 points, got %d", len(points))
	}
	if points[2].Value != 0.200 {
		t.Errorf("expected last value 0.200, got %v", points[2].Value)
	}
}

// TestHTTPPrometheusClient_Healthy_OK verifies health check on responding server.
func TestHTTPPrometheusClient_Healthy_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := metrics.NewHTTPPrometheusClient(srv.URL, 5*time.Second, 5*time.Second)
	if err := c.Healthy(context.Background()); err != nil {
		t.Errorf("expected healthy, got %v", err)
	}
}

// TestHTTPPrometheusClient_Healthy_Unreachable verifies health check failure.
func TestHTTPPrometheusClient_Healthy_Unreachable(t *testing.T) {
	c := metrics.NewHTTPPrometheusClient("http://127.0.0.1:19999", 1*time.Second, 5*time.Second)
	if err := c.Healthy(context.Background()); err == nil {
		t.Error("expected error for unreachable server, got nil")
	}
}
