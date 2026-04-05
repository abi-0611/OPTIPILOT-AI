package forecaster_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/optipilot-ai/optipilot/internal/forecaster"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func fakeHistory(n int) []forecaster.MetricPoint {
	pts := make([]forecaster.MetricPoint, n)
	for i := range pts {
		pts[i] = forecaster.MetricPoint{
			DS: time.Now().UTC().Add(time.Duration(i) * 5 * time.Minute).Format(time.RFC3339),
			Y:  float64(100 + i),
		}
	}
	return pts
}

func forecastServer(body interface{}, code int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(body) //nolint:errcheck
	}))
}

func spotRiskServer(body interface{}, code int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(body) //nolint:errcheck
	}))
}

// ── ForecastDemand ────────────────────────────────────────────────────────────

func TestForecastDemand_Success(t *testing.T) {
	srv := forecastServer(map[string]interface{}{
		"service":        "api",
		"metric":         "rps",
		"forecasts":      []map[string]interface{}{{"ds": "2026-01-01T00:15:00Z", "yhat": 120.5}},
		"model_used":     "ensemble",
		"change_percent": 20.5,
		"confidence":     0.85,
	}, http.StatusOK)
	defer srv.Close()

	c := forecaster.NewClient(srv.URL)
	c.SetSleepFn(func(time.Duration) {})

	result, err := c.ForecastDemand(context.Background(), "api", "rps", fakeHistory(50))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil ForecastResult")
	}
	if result.ChangePercent != 20.5 {
		t.Errorf("ChangePercent = %v, want 20.5", result.ChangePercent)
	}
	if result.Confidence != 0.85 {
		t.Errorf("Confidence = %v, want 0.85", result.Confidence)
	}
	if result.PredictedRPS != 120.5 {
		t.Errorf("PredictedRPS = %v, want 120.5", result.PredictedRPS)
	}
}

func TestForecastDemand_EmptyForecasts(t *testing.T) {
	srv := forecastServer(map[string]interface{}{
		"service": "api", "metric": "rps",
		"forecasts": []interface{}{}, "model_used": "ensemble",
		"change_percent": 0.0, "confidence": 0.5,
	}, http.StatusOK)
	defer srv.Close()

	c := forecaster.NewClient(srv.URL)
	c.SetSleepFn(func(time.Duration) {})
	result, err := c.ForecastDemand(context.Background(), "api", "rps", fakeHistory(5))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result even with empty forecasts")
	}
	if result.PredictedRPS != 0.0 {
		t.Errorf("PredictedRPS = %v, want 0.0 for empty forecasts", result.PredictedRPS)
	}
}

func TestForecastDemand_500_TripsCircuitAfter5(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c := forecaster.NewClient(srv.URL)
	c.SetSleepFn(func(time.Duration) {})
	c.SetNowFn(func() time.Time { return now })

	for i := 0; i < 5; i++ {
		c.ForecastDemand(context.Background(), "api", "rps", fakeHistory(5)) //nolint:errcheck
	}

	if !c.CircuitOpen() {
		t.Error("expected circuit to be open after 5 errors")
	}
}

func TestForecastDemand_CircuitOpen_ReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c := forecaster.NewClient(srv.URL)
	c.SetSleepFn(func(time.Duration) {})
	c.SetNowFn(func() time.Time { return now })

	// Trip the breaker
	for i := 0; i < 5; i++ {
		c.ForecastDemand(context.Background(), "api", "rps", fakeHistory(5)) //nolint:errcheck
	}

	// Now circuit is open — call should return nil, nil
	result, err := c.ForecastDemand(context.Background(), "api", "rps", fakeHistory(5))
	if err != nil {
		t.Errorf("expected nil error when circuit open, got: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result when circuit open, got: %+v", result)
	}
}

func TestForecastDemand_CircuitReset_AfterPause(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c := forecaster.NewClient(srv.URL)
	c.SetSleepFn(func(time.Duration) {})
	c.SetNowFn(func() time.Time { return now })

	for i := 0; i < 5; i++ {
		c.ForecastDemand(context.Background(), "api", "rps", fakeHistory(5)) //nolint:errcheck
	}
	if !c.CircuitOpen() {
		t.Fatal("circuit should be open")
	}

	// Advance clock past 30-second pause
	future := now.Add(31 * time.Second)
	c.SetNowFn(func() time.Time { return future })

	if c.CircuitOpen() {
		t.Error("circuit should be closed after 31s pause")
	}
}

func TestForecastDemand_Retry_Once(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			// First call: 503
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		// Second call: success
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"service": "api", "metric": "rps",
			"forecasts":  []map[string]interface{}{{"ds": "2026-01-01T00:15:00Z", "yhat": 99.0}},
			"model_used": "ensemble", "change_percent": 5.0, "confidence": 0.8,
		})
	}))
	defer srv.Close()

	c := forecaster.NewClient(srv.URL)
	c.SetSleepFn(func(time.Duration) {})

	result, err := c.ForecastDemand(context.Background(), "api", "rps", fakeHistory(5))
	if err != nil {
		t.Fatalf("expected success after retry: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 HTTP calls (1 retry), got %d", calls.Load())
	}
}

func TestForecastDemand_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	c := forecaster.NewClient(srv.URL)
	c.SetSleepFn(func(time.Duration) {})
	_, err := c.ForecastDemand(ctx, "api", "rps", fakeHistory(5))
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

// ── PredictSpotRisk ───────────────────────────────────────────────────────────

func TestPredictSpotRisk_Success(t *testing.T) {
	srv := spotRiskServer(map[string]interface{}{
		"instance_type":            "m5.xlarge",
		"az":                       "us-east-1a",
		"interruption_probability": 0.42,
		"recommended_action":       "migrate",
		"confidence":               0.75,
	}, http.StatusOK)
	defer srv.Close()

	c := forecaster.NewClient(srv.URL)
	c.SetSleepFn(func(time.Duration) {})

	result, err := c.PredictSpotRisk(context.Background(), "m5.xlarge", "us-east-1a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil SpotRiskResult")
	}
	if result.InterruptionProbability != 0.42 {
		t.Errorf("InterruptionProbability = %v, want 0.42", result.InterruptionProbability)
	}
	if result.RecommendedAction != "migrate" {
		t.Errorf("RecommendedAction = %v, want migrate", result.RecommendedAction)
	}
}

func TestPredictSpotRisk_CircuitOpen_ReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c := forecaster.NewClient(srv.URL)
	c.SetSleepFn(func(time.Duration) {})
	c.SetNowFn(func() time.Time { return now })

	for i := 0; i < 5; i++ {
		c.PredictSpotRisk(context.Background(), "m5.large", "us-east-1a") //nolint:errcheck
	}

	result, err := c.PredictSpotRisk(context.Background(), "m5.large", "us-east-1a")
	if err != nil {
		t.Errorf("expected nil error when circuit open, got %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result when circuit open, got %+v", result)
	}
}

func TestPredictSpotRisk_400_NonRetriable(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, `{"detail":"validation error"}`, http.StatusUnprocessableEntity)
	}))
	defer srv.Close()

	c := forecaster.NewClient(srv.URL)
	c.SetSleepFn(func(time.Duration) {})
	_, err := c.PredictSpotRisk(context.Background(), "m5.large", "us-east-1a")
	if err == nil {
		t.Error("expected error for 422")
	}
	// 4xx is NOT retriable — only 1 call
	if calls.Load() != 1 {
		t.Errorf("expected 1 call for 422 (non-retriable), got %d", calls.Load())
	}
}

// ── circuit breaker unit tests ────────────────────────────────────────────────

func TestCircuitBreaker_ErrorWindowEviction(t *testing.T) {
	// Errors older than 1 min should not count toward the threshold.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c := forecaster.NewClient(srv.URL)
	c.SetSleepFn(func(time.Duration) {})
	c.SetNowFn(func() time.Time { return now })

	// 4 errors at t=0
	for i := 0; i < 4; i++ {
		c.ForecastDemand(context.Background(), "api", "rps", fakeHistory(5)) //nolint:errcheck
	}

	// Advance 2 minutes — old errors should be evicted
	now = now.Add(2 * time.Minute)
	c.SetNowFn(func() time.Time { return now })

	// One more error — still only 1 within the window, circuit stays closed
	c.ForecastDemand(context.Background(), "api", "rps", fakeHistory(5)) //nolint:errcheck

	if c.CircuitOpen() {
		t.Error("circuit should be closed: old errors evicted")
	}
}
