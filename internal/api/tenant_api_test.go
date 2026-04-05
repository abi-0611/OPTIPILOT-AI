package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/optipilot-ai/optipilot/internal/metrics"
	"github.com/optipilot-ai/optipilot/internal/tenant"
)

// ── fakes ────────────────────────────────────────────────────────────────────

type fakeStateReader struct {
	mu     sync.RWMutex
	states map[string]*tenant.TenantState
}

func newFakeReader(states ...*tenant.TenantState) *fakeStateReader {
	m := make(map[string]*tenant.TenantState, len(states))
	for _, s := range states {
		m[s.Name] = s
	}
	return &fakeStateReader{states: m}
}

func (f *fakeStateReader) GetState(name string) *tenant.TenantState {
	f.mu.RLock()
	defer f.mu.RUnlock()
	s, ok := f.states[name]
	if !ok {
		return nil
	}
	c := *s
	return &c
}

func (f *fakeStateReader) GetAllStates() map[string]*tenant.TenantState {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make(map[string]*tenant.TenantState, len(f.states))
	for k, v := range f.states {
		c := *v
		out[k] = &c
	}
	return out
}

type fakeProm struct {
	mu       sync.Mutex
	ranges   map[string][]metrics.DataPoint
	rangeErr error
}

func newFakeProm() *fakeProm { return &fakeProm{ranges: make(map[string][]metrics.DataPoint)} }

func (p *fakeProm) Query(_ context.Context, _ string) (float64, error) { return 0, nil }
func (p *fakeProm) Healthy(_ context.Context) error                    { return nil }
func (p *fakeProm) QueryRange(_ context.Context, query string, _, _ time.Time, _ time.Duration) ([]metrics.DataPoint, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rangeErr != nil {
		return nil, p.rangeErr
	}
	return p.ranges[query], nil
}

func (p *fakeProm) SetRange(query string, pts []metrics.DataPoint) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ranges[query] = pts
}

func sampleState(name, tier string, weight int32, cores, memGiB float64) *tenant.TenantState {
	return &tenant.TenantState{
		Name:                   name,
		Tier:                   tier,
		Weight:                 weight,
		Namespaces:             []string{name + "-ns"},
		CurrentCores:           cores,
		CurrentMemoryGiB:       memGiB,
		CurrentCostUSD:         100,
		MaxCores:               50,
		MaxMemoryGiB:           128,
		GuaranteedCoresPercent: 10,
		Burstable:              true,
		AllocationStatus:       "guaranteed",
		FairnessScore:          0.9,
		LastRefreshed:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func newMux(h *TenantAPIHandler) *http.ServeMux {
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux
}

func getJSON(t *testing.T, mux *http.ServeMux, path string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		return w, nil
	}
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		// might be an array
		return w, nil
	}
	return w, result
}

func getJSONArray(t *testing.T, mux *http.ServeMux, path string) (*httptest.ResponseRecorder, []any) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var result []any
	if w.Code == http.StatusOK {
		json.Unmarshal(w.Body.Bytes(), &result)
	}
	return w, result
}

// ── GET /api/v1/tenants ───────────────────────────────────────────────────────

func TestTenantAPI_ListTenants_Empty(t *testing.T) {
	h := NewTenantAPIHandler(newFakeReader(), nil, nil)
	_, items := getJSONArray(t, newMux(h), "/api/v1/tenants")
	if len(items) != 0 {
		t.Errorf("expected empty list, got %d", len(items))
	}
}

func TestTenantAPI_ListTenants_ReturnsAll(t *testing.T) {
	h := NewTenantAPIHandler(newFakeReader(
		sampleState("gold", "gold", 10, 8, 16),
		sampleState("silver", "silver", 5, 4, 8),
	), nil, nil)

	_, items := getJSONArray(t, newMux(h), "/api/v1/tenants")
	if len(items) != 2 {
		t.Fatalf("expected 2 tenants, got %d", len(items))
	}
}

func TestTenantAPI_ListTenants_FieldsPresent(t *testing.T) {
	h := NewTenantAPIHandler(newFakeReader(
		sampleState("gold", "gold", 20, 8, 16),
	), nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/tenants", nil)
	w := httptest.NewRecorder()
	newMux(h).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}

	var items []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(items) != 1 {
		t.Fatal("expected 1 item")
	}
	item := items[0]
	for _, field := range []string{"name", "tier", "weight", "current_cores", "is_noisy", "is_victim"} {
		if _, ok := item[field]; !ok {
			t.Errorf("missing field %q in response", field)
		}
	}
}

func TestTenantAPI_ListTenants_ContentTypeJSON(t *testing.T) {
	h := NewTenantAPIHandler(newFakeReader(), nil, nil)
	req := httptest.NewRequest("GET", "/api/v1/tenants", nil)
	w := httptest.NewRecorder()
	newMux(h).ServeHTTP(w, req)
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type=%s, want application/json", ct)
	}
}

// ── GET /api/v1/tenants/{name} ───────────────────────────────────────────────

func TestTenantAPI_GetTenant_Found(t *testing.T) {
	h := NewTenantAPIHandler(newFakeReader(
		sampleState("gold", "gold", 10, 8, 16),
	), nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/tenants/gold", nil)
	w := httptest.NewRecorder()
	newMux(h).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, body=%s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["name"] != "gold" {
		t.Errorf("name=%v, want gold", resp["name"])
	}
	// Detail fields.
	for _, field := range []string{"namespaces", "guaranteed_cores_percent", "burstable", "max_burst_percent"} {
		if _, ok := resp[field]; !ok {
			t.Errorf("detail field %q missing", field)
		}
	}
}

func TestTenantAPI_GetTenant_NotFound(t *testing.T) {
	h := NewTenantAPIHandler(newFakeReader(), nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/tenants/unknown", nil)
	w := httptest.NewRecorder()
	newMux(h).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", w.Code)
	}
}

func TestTenantAPI_GetTenant_LastRefreshed(t *testing.T) {
	s := sampleState("gold", "gold", 10, 8, 16)
	h := NewTenantAPIHandler(newFakeReader(s), nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/tenants/gold", nil)
	w := httptest.NewRecorder()
	newMux(h).ServeHTTP(w, req)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["last_refreshed"] == nil || resp["last_refreshed"] == "" {
		t.Error("last_refreshed should be set")
	}
}

// ── GET /api/v1/tenants/{name}/usage ────────────────────────────────────────

func TestTenantAPI_Usage_NotFound(t *testing.T) {
	h := NewTenantAPIHandler(newFakeReader(), nil, nil)
	req := httptest.NewRequest("GET", "/api/v1/tenants/unknown/usage", nil)
	w := httptest.NewRecorder()
	newMux(h).ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", w.Code)
	}
}

func TestTenantAPI_Usage_FallbackWhenNoProm(t *testing.T) {
	s := sampleState("gold", "gold", 10, 8, 16)
	h := NewTenantAPIHandler(newFakeReader(s), nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/tenants/gold/usage", nil)
	w := httptest.NewRecorder()
	newMux(h).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	history, _ := resp["history"].([]any)
	if len(history) != 1 {
		t.Fatalf("expected 1 fallback point, got %d", len(history))
	}
	pt := history[0].(map[string]any)
	if pt["cores"] == nil {
		t.Error("cores missing from usage point")
	}
}

func TestTenantAPI_Usage_FromPrometheus(t *testing.T) {
	s := sampleState("gold", "gold", 10, 8, 16)
	prom := newFakeProm()

	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cpuQ := `sum(namespace_cpu_usage_seconds_total{namespace=~"gold-ns"})`
	memQ := `sum(namespace_memory_working_set_bytes{namespace=~"gold-ns"})`

	prom.SetRange(cpuQ, []metrics.DataPoint{
		{Timestamp: ts, Value: 8.0},
		{Timestamp: ts.Add(5 * time.Minute), Value: 9.0},
	})
	prom.SetRange(memQ, []metrics.DataPoint{
		{Timestamp: ts, Value: 16 * 1024 * 1024 * 1024},
		{Timestamp: ts.Add(5 * time.Minute), Value: 17 * 1024 * 1024 * 1024},
	})

	h := NewTenantAPIHandler(newFakeReader(s), prom, nil)
	req := httptest.NewRequest("GET", "/api/v1/tenants/gold/usage", nil)
	w := httptest.NewRecorder()
	newMux(h).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, body=%s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	history, _ := resp["history"].([]any)
	if len(history) != 2 {
		t.Fatalf("expected 2 history points, got %d", len(history))
	}
}

func TestTenantAPI_Usage_FieldsPresent(t *testing.T) {
	s := sampleState("gold", "gold", 10, 8, 16)
	h := NewTenantAPIHandler(newFakeReader(s), nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/tenants/gold/usage", nil)
	w := httptest.NewRecorder()
	newMux(h).ServeHTTP(w, req)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	for _, f := range []string{"tenant_name", "history"} {
		if _, ok := resp[f]; !ok {
			t.Errorf("field %q missing", f)
		}
	}
}

// ── GET /api/v1/fairness ─────────────────────────────────────────────────────

func TestTenantAPI_Fairness_Structure(t *testing.T) {
	h := NewTenantAPIHandler(newFakeReader(
		sampleState("gold", "gold", 10, 10, 16),
		sampleState("silver", "silver", 5, 5, 8),
	), nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/fairness", nil)
	w := httptest.NewRecorder()
	newMux(h).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	for _, f := range []string{"timestamp", "global_index", "per_tenant"} {
		if _, ok := resp[f]; !ok {
			t.Errorf("field %q missing", f)
		}
	}
}

func TestTenantAPI_Fairness_GlobalIndexRange(t *testing.T) {
	h := NewTenantAPIHandler(newFakeReader(
		sampleState("gold", "gold", 10, 10, 16),
		sampleState("silver", "silver", 5, 5, 8),
	), nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/fairness", nil)
	w := httptest.NewRecorder()
	newMux(h).ServeHTTP(w, req)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	idx, _ := resp["global_index"].(float64)
	if idx < 0 || idx > 1.0 {
		t.Errorf("global_index=%f out of range [0,1]", idx)
	}
}

func TestTenantAPI_Fairness_NoTenants(t *testing.T) {
	h := NewTenantAPIHandler(newFakeReader(), nil, nil)
	req := httptest.NewRequest("GET", "/api/v1/fairness", nil)
	w := httptest.NewRecorder()
	newMux(h).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status=%d, want 200", w.Code)
	}
}

// ── GET /api/v1/fairness/impact/{service} ────────────────────────────────────

func TestTenantAPI_Impact_MissingTenantParam(t *testing.T) {
	h := NewTenantAPIHandler(newFakeReader(
		sampleState("gold", "gold", 10, 10, 16),
	), nil, nil)
	req := httptest.NewRequest("GET", "/api/v1/fairness/impact/my-service", nil)
	w := httptest.NewRecorder()
	newMux(h).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", w.Code)
	}
}

func TestTenantAPI_Impact_TenantNotFound(t *testing.T) {
	h := NewTenantAPIHandler(newFakeReader(), nil, nil)
	req := httptest.NewRequest("GET", "/api/v1/fairness/impact/my-service?tenant=unknown", nil)
	w := httptest.NewRecorder()
	newMux(h).ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", w.Code)
	}
}

func TestTenantAPI_Impact_InvalidDelta(t *testing.T) {
	h := NewTenantAPIHandler(newFakeReader(
		sampleState("gold", "gold", 10, 10, 16),
	), nil, nil)
	req := httptest.NewRequest("GET", "/api/v1/fairness/impact/svc?tenant=gold&delta_cores=bad", nil)
	w := httptest.NewRecorder()
	newMux(h).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", w.Code)
	}
}

func TestTenantAPI_Impact_Response(t *testing.T) {
	h := NewTenantAPIHandler(newFakeReader(
		sampleState("gold", "gold", 10, 10, 16),
		sampleState("silver", "silver", 5, 5, 8),
	), nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/fairness/impact/my-service?tenant=gold&delta_cores=5", nil)
	w := httptest.NewRecorder()
	newMux(h).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, body=%s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	for _, f := range []string{"service", "tenant_name", "delta_cores", "current_index", "projected_index", "index_delta"} {
		if _, ok := resp[f]; !ok {
			t.Errorf("field %q missing", f)
		}
	}

	if resp["service"] != "my-service" {
		t.Errorf("service=%v, want my-service", resp["service"])
	}
	if resp["tenant_name"] != "gold" {
		t.Errorf("tenant_name=%v, want gold", resp["tenant_name"])
	}
}

func TestTenantAPI_Impact_ZeroDelta(t *testing.T) {
	h := NewTenantAPIHandler(newFakeReader(
		sampleState("gold", "gold", 10, 10, 16),
	), nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/fairness/impact/svc?tenant=gold&delta_cores=0", nil)
	w := httptest.NewRecorder()
	newMux(h).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	// Zero delta → projected should equal current.
	curr, _ := resp["current_index"].(float64)
	proj, _ := resp["projected_index"].(float64)
	delta, _ := resp["index_delta"].(float64)
	if curr != proj {
		t.Errorf("current=%f ≠ projected=%f for zero delta", curr, proj)
	}
	if delta != 0 {
		t.Errorf("index_delta=%f, want 0 for zero delta", delta)
	}
}

// ── Noisy/victim signals in list ─────────────────────────────────────────────

func TestTenantAPI_NoisySignalInList(t *testing.T) {
	s := sampleState("gold", "gold", 10, 8, 16)
	reader := newFakeReader(s)

	// Create a detector and manually trigger a noisy signal.
	clk := &struct{ tenant.Clock }{} // not used directly
	_ = clk
	detector := tenant.NewNoisyNeighborDetector()

	h := NewTenantAPIHandler(reader, nil, detector)

	req := httptest.NewRequest("GET", "/api/v1/tenants", nil)
	w := httptest.NewRecorder()
	newMux(h).ServeHTTP(w, req)

	var items []map[string]any
	json.Unmarshal(w.Body.Bytes(), &items)

	if len(items) != 1 {
		t.Fatal("expected 1 item")
	}
	// Default: not noisy, not victim.
	if items[0]["is_noisy"].(bool) {
		t.Error("should not be noisy by default")
	}
}
