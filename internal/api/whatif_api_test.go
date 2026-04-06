package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/optipilot-ai/optipilot/internal/simulator"
)

// ── Fakes ─────────────────────────────────────────────────────────────────────

type fakeWhatIfHistory struct {
	data map[string][]simulator.DataPoint
}

func (f *fakeWhatIfHistory) FetchHistory(_ context.Context, query string, start, end time.Time, step time.Duration) ([]simulator.DataPoint, error) {
	if pts, ok := f.data[query]; ok {
		return pts, nil
	}
	return nil, nil
}

type fakeWhatIfDecisions struct{}

func (f *fakeWhatIfDecisions) FetchDecisions(_ context.Context, services []string, start, end time.Time) ([]simulator.HistoricalDecision, error) {
	return nil, nil
}

func noopSolver(snap simulator.SimulationSnapshot) simulator.SimulatedAction {
	return simulator.SimulatedAction{
		Action:      "no_action",
		Replicas:    2,
		CPUCores:    0.5,
		HourlyCost:  1.0,
		SLOBreached: false,
	}
}

func noopCurveFactory(sloMetric string, sloTarget float64) simulator.SolverFunc {
	return func(snap simulator.SimulationSnapshot) simulator.SimulatedAction {
		return simulator.SimulatedAction{
			Action:      "no_action",
			Replicas:    2,
			CPUCores:    0.5,
			HourlyCost:  1.0,
			SLOBreached: false,
		}
	}
}

func makeTimeSeries2(n int, start time.Time, step time.Duration, values []float64) []simulator.DataPoint {
	pts := make([]simulator.DataPoint, n)
	for i := 0; i < n; i++ {
		v := 0.5
		if i < len(values) {
			v = values[i]
		}
		pts[i] = simulator.DataPoint{Timestamp: start.Add(time.Duration(i) * step), Value: v}
	}
	return pts
}

var (
	whatIfStart = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	whatIfEnd   = whatIfStart.Add(time.Hour)
	whatIfStep  = 5 * time.Minute
)

func setupWhatIfHandler() (*WhatIfAPIHandler, *http.ServeMux) {
	cpuData := makeTimeSeries2(12, whatIfStart, whatIfStep, nil)
	history := &fakeWhatIfHistory{data: map[string][]simulator.DataPoint{
		"api-server:cpu_usage_seconds_total":              cpuData,
		"api-server:http_request_duration_seconds_bucket": makeTimeSeries2(12, whatIfStart, whatIfStep, nil),
	}}

	h := NewWhatIfAPIHandler(history, &fakeWhatIfDecisions{}, noopSolver, noopCurveFactory)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return h, mux
}

// ── POST /api/v1/simulate ─────────────────────────────────────────────────────

func TestWhatIfAPI_RunSimulation(t *testing.T) {
	_, mux := setupWhatIfHandler()

	body := map[string]any{
		"services":    []string{"api-server"},
		"start":       whatIfStart,
		"end":         whatIfEnd,
		"step":        whatIfStep,
		"description": "test run",
	}
	buf, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/v1/simulate", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var result simulator.SimulationResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.ID == "" {
		t.Error("expected non-empty result ID")
	}
}

func TestWhatIfAPI_RunSimulation_Unconfigured(t *testing.T) {
	h := NewWhatIfAPIHandler(nil, nil, nil, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := map[string]any{
		"services": []string{"api-server"},
		"start":    whatIfStart,
		"end":      whatIfEnd,
	}
	buf, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/v1/simulate", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWhatIfAPI_RunSimulation_MissingServices(t *testing.T) {
	_, mux := setupWhatIfHandler()

	body := map[string]any{
		"start": whatIfStart,
		"end":   whatIfEnd,
	}
	buf, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/v1/simulate", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", rec.Code)
	}
}

func TestWhatIfAPI_RunSimulation_InvalidTimeRange(t *testing.T) {
	_, mux := setupWhatIfHandler()

	body := map[string]any{
		"services": []string{"api-server"},
		"start":    whatIfEnd, // end before start is invalid
		"end":      whatIfStart,
	}
	buf, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/v1/simulate", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", rec.Code)
	}
}

func TestWhatIfAPI_RunSimulation_BadJSON(t *testing.T) {
	_, mux := setupWhatIfHandler()

	req := httptest.NewRequest("POST", "/api/v1/simulate", bytes.NewBufferString("{bad json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", rec.Code)
	}
}

// ── GET /api/v1/simulate/{id} ─────────────────────────────────────────────────

func TestWhatIfAPI_GetSimulation(t *testing.T) {
	h, mux := setupWhatIfHandler()

	// Pre-store a result directly.
	stored := &simulator.SimulationResult{ID: "test-sim-id", Description: "stored"}
	h.mu.Lock()
	h.results["test-sim-id"] = stored
	h.mu.Unlock()

	req := httptest.NewRequest("GET", "/api/v1/simulate/test-sim-id", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var result simulator.SimulationResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.ID != "test-sim-id" {
		t.Errorf("want id 'test-sim-id', got '%s'", result.ID)
	}
}

func TestWhatIfAPI_GetSimulation_NotFound(t *testing.T) {
	_, mux := setupWhatIfHandler()

	req := httptest.NewRequest("GET", "/api/v1/simulate/nonexistent", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", rec.Code)
	}
}

func TestWhatIfAPI_RunThenGet(t *testing.T) {
	_, mux := setupWhatIfHandler()

	body := map[string]any{
		"services": []string{"api-server"},
		"start":    whatIfStart,
		"end":      whatIfEnd,
		"step":     whatIfStep,
	}
	buf, _ := json.Marshal(body)

	// Run simulation.
	postReq := httptest.NewRequest("POST", "/api/v1/simulate", bytes.NewReader(buf))
	postReq.Header.Set("Content-Type", "application/json")
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)

	if postRec.Code != http.StatusCreated {
		t.Fatalf("POST want 201, got %d", postRec.Code)
	}
	var created simulator.SimulationResult
	json.NewDecoder(postRec.Body).Decode(&created)

	// Retrieve by ID.
	getReq := httptest.NewRequest("GET", "/api/v1/simulate/"+created.ID, nil)
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("GET want 200, got %d", getRec.Code)
	}
	var fetched simulator.SimulationResult
	json.NewDecoder(getRec.Body).Decode(&fetched)
	if fetched.ID != created.ID {
		t.Errorf("ID mismatch: created %s, fetched %s", created.ID, fetched.ID)
	}
}

// ── POST /api/v1/simulate/slo-cost-curve ─────────────────────────────────────

func TestWhatIfAPI_SLOCostCurve(t *testing.T) {
	_, mux := setupWhatIfHandler()

	body := map[string]any{
		"service":    "api-server",
		"start":      whatIfStart,
		"end":        whatIfEnd,
		"step":       whatIfStep,
		"slo_metric": "latency_p99",
		"min_target": 0.050,
		"max_target": 0.500,
		"steps":      5,
	}
	buf, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/v1/simulate/slo-cost-curve", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["service"] != "api-server" {
		t.Errorf("service: want api-server, got %v", resp["service"])
	}
	if resp["slo_metric"] != "latency_p99" {
		t.Errorf("slo_metric: want latency_p99, got %v", resp["slo_metric"])
	}

	points, ok := resp["points"].([]interface{})
	if !ok {
		t.Fatal("expected points array in response")
	}
	if len(points) != 5 {
		t.Errorf("expected 5 curve points, got %d", len(points))
	}
}

func TestWhatIfAPI_SLOCostCurve_Unconfigured(t *testing.T) {
	h := NewWhatIfAPIHandler(nil, nil, nil, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := map[string]any{
		"service":    "api-server",
		"start":      whatIfStart,
		"end":        whatIfEnd,
		"slo_metric": "latency_p99",
		"min_target": 0.050,
		"max_target": 0.500,
		"steps":      5,
	}
	buf, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/v1/simulate/slo-cost-curve", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWhatIfAPI_SLOCostCurve_BadJSON(t *testing.T) {
	_, mux := setupWhatIfHandler()

	req := httptest.NewRequest("POST", "/api/v1/simulate/slo-cost-curve", bytes.NewBufferString("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", rec.Code)
	}
}

func TestWhatIfAPI_SLOCostCurve_ValidationError(t *testing.T) {
	_, mux := setupWhatIfHandler()

	// Empty service should fail validation.
	body := map[string]any{
		"start":      whatIfStart,
		"end":        whatIfEnd,
		"slo_metric": "latency_p99",
		"min_target": 0.050,
		"max_target": 0.500,
		"steps":      5,
	}
	buf, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/v1/simulate/slo-cost-curve", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 (missing service), got %d", rec.Code)
	}
}

// ── Content-Type ───────────────────────────────────────────────────────────────

func TestWhatIfAPI_ContentTypeJSON(t *testing.T) {
	h, mux := setupWhatIfHandler()

	// Pre-store for GET.
	h.mu.Lock()
	h.results["ct-test"] = &simulator.SimulationResult{ID: "ct-test"}
	h.mu.Unlock()

	req := httptest.NewRequest("GET", "/api/v1/simulate/ct-test", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("want application/json, got %q", ct)
	}
}

func TestWhatIfAPI_SimulationResultHasDescription(t *testing.T) {
	_, mux := setupWhatIfHandler()

	body := map[string]any{
		"services":    []string{"api-server"},
		"start":       whatIfStart,
		"end":         whatIfEnd,
		"step":        whatIfStep,
		"description": "my-test-scenario",
	}
	buf, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/v1/simulate", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d", rec.Code)
	}
	var result simulator.SimulationResult
	json.NewDecoder(rec.Body).Decode(&result)
	if result.Description != "my-test-scenario" {
		t.Errorf("want description 'my-test-scenario', got '%s'", result.Description)
	}
}
