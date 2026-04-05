package explainability

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/optipilot-ai/optipilot/internal/cel"
	"github.com/optipilot-ai/optipilot/internal/engine"
)

func testRecord(id, ns, svc, trigger string, actionType engine.ActionType, dryRun bool, confidence float64, ts time.Time) engine.DecisionRecord {
	return engine.DecisionRecord{
		ID:        id,
		Timestamp: ts,
		Namespace: ns,
		Service:   svc,
		Trigger:   trigger,
		CurrentState: cel.CurrentState{
			Replicas:   4,
			CPURequest: 0.5,
			CPUUsage:   0.3,
		},
		SLOStatus: cel.SLOStatus{Compliant: true, BudgetRemaining: 0.95},
		Candidates: []engine.ScoredCandidate{
			{
				Plan:   cel.CandidatePlan{Replicas: 4, CPURequest: 0.5, SpotRatio: 0.0},
				Score:  engine.CandidateScore{SLO: 0.8, Cost: 0.5, Carbon: 0.5, Fairness: 1.0, Weighted: 0.7},
				Viable: true,
			},
		},
		SelectedAction: engine.ScalingAction{
			Type:          actionType,
			TargetReplica: 4,
			CPURequest:    0.5,
			DryRun:        dryRun,
			Confidence:    confidence,
		},
		ActionType: actionType,
		DryRun:     dryRun,
		Confidence: confidence,
	}
}

func TestJournal_WriteAndGetByID(t *testing.T) {
	j, err := NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer j.Close()

	ts := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	rec := testRecord("d-001", "production", "api-server", "periodic", engine.ActionNoAction, false, 0.9, ts)

	if err := j.Write(rec); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := j.GetByID("d-001")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil {
		t.Fatal("expected record, got nil")
	}
	if got.ID != "d-001" {
		t.Errorf("ID = %q, want %q", got.ID, "d-001")
	}
	if got.Namespace != "production" {
		t.Errorf("Namespace = %q, want %q", got.Namespace, "production")
	}
	if got.Service != "api-server" {
		t.Errorf("Service = %q, want %q", got.Service, "api-server")
	}
	if got.Confidence != 0.9 {
		t.Errorf("Confidence = %f, want 0.9", got.Confidence)
	}
}

func TestJournal_GetByID_NotFound(t *testing.T) {
	j, err := NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer j.Close()

	got, err := j.GetByID("nonexistent")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing ID, got %+v", got)
	}
}

func TestJournal_QueryByNamespaceAndService(t *testing.T) {
	j, err := NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer j.Close()

	ts := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	j.Write(testRecord("d-001", "prod", "api", "periodic", engine.ActionScaleUp, false, 0.8, ts))
	j.Write(testRecord("d-002", "prod", "web", "periodic", engine.ActionScaleDown, false, 0.7, ts))
	j.Write(testRecord("d-003", "staging", "api", "periodic", engine.ActionNoAction, true, 0.6, ts))

	// Query prod/api
	records, err := j.Query(QueryFilter{Namespace: "prod", Service: "api"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].ID != "d-001" {
		t.Errorf("expected d-001, got %s", records[0].ID)
	}

	// Query all prod
	records, err = j.Query(QueryFilter{Namespace: "prod"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("expected 2 records for prod, got %d", len(records))
	}
}

func TestJournal_QueryWithSinceAndLimit(t *testing.T) {
	j, err := NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer j.Close()

	t1 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 3, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC)

	j.Write(testRecord("d-001", "prod", "api", "periodic", engine.ActionNoAction, false, 0.5, t1))
	j.Write(testRecord("d-002", "prod", "api", "periodic", engine.ActionScaleUp, false, 0.6, t2))
	j.Write(testRecord("d-003", "prod", "api", "periodic", engine.ActionScaleDown, false, 0.7, t3))

	// Since t2
	since := time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC)
	records, err := j.Query(QueryFilter{Since: &since})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("expected 2 records since April 2, got %d", len(records))
	}

	// Limit 1
	records, err = j.Query(QueryFilter{Limit: 1})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 record with limit=1, got %d", len(records))
	}
	// Should be the most recent (desc order).
	if records[0].ID != "d-003" {
		t.Errorf("expected d-003 (most recent), got %s", records[0].ID)
	}
}

func TestJournal_QueryEmpty(t *testing.T) {
	j, err := NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer j.Close()

	records, err := j.Query(QueryFilter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}

func TestJournal_DecisionRecordRoundTrip(t *testing.T) {
	j, err := NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer j.Close()

	ts := time.Date(2026, 4, 5, 14, 30, 0, 0, time.UTC)
	rec := testRecord("d-rt", "ns", "svc", "slo-breach", engine.ActionScaleUp, true, 0.85, ts)
	rec.PolicyNames = []string{"cost-policy", "slo-policy"}
	rec.ObjectiveWeights = map[string]float64{"slo": 0.4, "cost": 0.3}

	if err := j.Write(rec); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := j.GetByID("d-rt")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(got.PolicyNames) != 2 {
		t.Errorf("expected 2 policy names, got %d", len(got.PolicyNames))
	}
	if got.ObjectiveWeights["slo"] != 0.4 {
		t.Errorf("expected slo weight 0.4, got %f", got.ObjectiveWeights["slo"])
	}
	if len(got.Candidates) != 1 {
		t.Errorf("expected 1 candidate, got %d", len(got.Candidates))
	}
	if !got.DryRun {
		t.Error("expected DryRun=true")
	}
}

// --- REST API Tests ---

func setupTestServer(t *testing.T) (*Journal, *http.ServeMux) {
	t.Helper()
	j, err := NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	mux := http.NewServeMux()
	handler := NewAPIHandler(j)
	handler.RegisterRoutes(mux)
	return j, mux
}

func TestAPI_GetDecisionByID(t *testing.T) {
	j, mux := setupTestServer(t)
	defer j.Close()

	ts := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	j.Write(testRecord("api-001", "prod", "svc", "periodic", engine.ActionScaleUp, false, 0.9, ts))

	req := httptest.NewRequest("GET", "/api/v1/decisions/api-001", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}

	var rec engine.DecisionRecord
	if err := json.NewDecoder(rr.Body).Decode(&rec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rec.ID != "api-001" {
		t.Errorf("ID = %q, want %q", rec.ID, "api-001")
	}
}

func TestAPI_GetDecisionNotFound(t *testing.T) {
	j, mux := setupTestServer(t)
	defer j.Close()

	req := httptest.NewRequest("GET", "/api/v1/decisions/nope", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestAPI_ListDecisions(t *testing.T) {
	j, mux := setupTestServer(t)
	defer j.Close()

	ts := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	j.Write(testRecord("l-001", "prod", "api", "periodic", engine.ActionNoAction, false, 0.8, ts))
	j.Write(testRecord("l-002", "prod", "web", "periodic", engine.ActionScaleUp, false, 0.7, ts))

	req := httptest.NewRequest("GET", "/api/v1/decisions?namespace=prod", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var records []engine.DecisionRecord
	body, _ := io.ReadAll(rr.Body)
	if err := json.Unmarshal(body, &records); err != nil {
		t.Fatalf("decode: %v; body: %s", err, body)
	}
	if len(records) != 2 {
		t.Errorf("expected 2 records, got %d", len(records))
	}
}

func TestAPI_ListDecisionsEmpty(t *testing.T) {
	j, mux := setupTestServer(t)
	defer j.Close()

	req := httptest.NewRequest("GET", "/api/v1/decisions", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	body := rr.Body.String()
	if body != "[]" {
		t.Errorf("expected empty array, got %q", body)
	}
}

func TestAPI_ListDecisionsWithLimit(t *testing.T) {
	j, mux := setupTestServer(t)
	defer j.Close()

	ts := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	j.Write(testRecord("lim-001", "prod", "api", "periodic", engine.ActionNoAction, false, 0.8, ts))
	j.Write(testRecord("lim-002", "prod", "api", "periodic", engine.ActionScaleUp, false, 0.7, ts.Add(time.Minute)))

	req := httptest.NewRequest("GET", "/api/v1/decisions?limit=1", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	var records []engine.DecisionRecord
	json.NewDecoder(rr.Body).Decode(&records)
	if len(records) != 1 {
		t.Errorf("expected 1 record with limit=1, got %d", len(records))
	}
}

func TestAPI_ListDecisionsBadSince(t *testing.T) {
	j, mux := setupTestServer(t)
	defer j.Close()

	req := httptest.NewRequest("GET", "/api/v1/decisions?since=not-a-date", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

// ── Enhanced Journal Tests (Task 8.1) ──────────────────────────────────────

func TestJournal_QueryByTrigger(t *testing.T) {
	j, err := NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer j.Close()

	ts := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	j.Write(testRecord("t-001", "prod", "api", "slo-breach", engine.ActionScaleUp, false, 0.9, ts))
	j.Write(testRecord("t-002", "prod", "web", "periodic", engine.ActionNoAction, false, 0.6, ts))
	j.Write(testRecord("t-003", "prod", "db", "slo-breach", engine.ActionScaleUp, false, 0.8, ts))

	records, err := j.Query(QueryFilter{Trigger: "slo-breach"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("expected 2 slo-breach records, got %d", len(records))
	}
}

func TestJournal_SearchFindsServiceMatch(t *testing.T) {
	j, err := NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer j.Close()

	ts := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	rec := testRecord("s-001", "production", "checkout-service", "slo-breach", engine.ActionScaleUp, false, 0.9, ts)
	rec.SelectedAction.Reason = "burn rate exceeded threshold"
	j.Write(rec)

	rec2 := testRecord("s-002", "staging", "api-gateway", "periodic", engine.ActionNoAction, false, 0.5, ts)
	j.Write(rec2)

	results, err := j.Search("checkout", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'checkout', got %d", len(results))
	}
	if len(results) > 0 && results[0].ID != "s-001" {
		t.Errorf("expected s-001, got %s", results[0].ID)
	}
}

func TestJournal_SearchByReason(t *testing.T) {
	j, err := NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer j.Close()

	ts := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	rec := testRecord("sr-001", "prod", "api", "slo-breach", engine.ActionScaleUp, false, 0.9, ts)
	rec.SelectedAction.Reason = "latency SLO burn rate exceeded"
	j.Write(rec)

	rec2 := testRecord("sr-002", "prod", "web", "periodic", engine.ActionNoAction, false, 0.5, ts)
	rec2.SelectedAction.Reason = "no action needed"
	j.Write(rec2)

	results, err := j.Search("latency", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'latency', got %d", len(results))
	}
}

func TestJournal_SearchNoResults(t *testing.T) {
	j, err := NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer j.Close()

	ts := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	j.Write(testRecord("sn-001", "prod", "api", "periodic", engine.ActionNoAction, false, 0.5, ts))

	results, err := j.Search("nonexistent-xyzzy", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestJournal_SearchEmptyText(t *testing.T) {
	j, err := NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer j.Close()

	results, err := j.Search("", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil for empty search, got %v", results)
	}
}

func TestJournal_AggregateStats_Basic(t *testing.T) {
	j, err := NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer j.Close()

	now := time.Now().UTC()
	j.Write(testRecord("a-001", "prod", "api", "periodic", engine.ActionScaleUp, false, 0.9, now.Add(-10*time.Minute)))
	j.Write(testRecord("a-002", "prod", "web", "slo-breach", engine.ActionScaleDown, false, 0.7, now.Add(-5*time.Minute)))

	stats, err := j.AggregateStats(1 * time.Hour)
	if err != nil {
		t.Fatalf("AggregateStats: %v", err)
	}
	if stats.TotalDecisions != 2 {
		t.Errorf("TotalDecisions: want 2, got %d", stats.TotalDecisions)
	}
	wantAvg := 0.8
	if stats.AverageConfidence < wantAvg-0.01 || stats.AverageConfidence > wantAvg+0.01 {
		t.Errorf("AverageConfidence: want ~%.2f, got %.4f", wantAvg, stats.AverageConfidence)
	}
	if stats.DecisionsPerHour <= 0 {
		t.Error("DecisionsPerHour should be positive")
	}
}

func TestJournal_AggregateStats_Empty(t *testing.T) {
	j, err := NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer j.Close()

	stats, err := j.AggregateStats(24 * time.Hour)
	if err != nil {
		t.Fatalf("AggregateStats: %v", err)
	}
	if stats.TotalDecisions != 0 {
		t.Errorf("expected 0 decisions, got %d", stats.TotalDecisions)
	}
	if stats.DecisionsPerHour != 0 {
		t.Errorf("expected 0 decisions/hr, got %.2f", stats.DecisionsPerHour)
	}
}

func TestJournal_AggregateStats_TopTriggers(t *testing.T) {
	j, err := NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer j.Close()

	now := time.Now().UTC()
	j.Write(testRecord("at-001", "prod", "api", "periodic", engine.ActionNoAction, false, 0.5, now.Add(-5*time.Minute)))
	j.Write(testRecord("at-002", "prod", "web", "periodic", engine.ActionScaleUp, false, 0.6, now.Add(-4*time.Minute)))
	j.Write(testRecord("at-003", "prod", "api", "slo-breach", engine.ActionScaleUp, false, 0.9, now.Add(-3*time.Minute)))

	stats, err := j.AggregateStats(1 * time.Hour)
	if err != nil {
		t.Fatalf("AggregateStats: %v", err)
	}
	if len(stats.TopTriggers) == 0 {
		t.Fatal("expected at least one trigger")
	}
	if stats.TopTriggers[0].Trigger != "periodic" {
		t.Errorf("top trigger should be periodic, got %q", stats.TopTriggers[0].Trigger)
	}
	if stats.TopTriggers[0].Count != 2 {
		t.Errorf("periodic count: want 2, got %d", stats.TopTriggers[0].Count)
	}
}

func TestJournal_AggregateStats_TopServices(t *testing.T) {
	j, err := NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer j.Close()

	now := time.Now().UTC()
	j.Write(testRecord("as-001", "prod", "api-server", "periodic", engine.ActionScaleUp, false, 0.5, now.Add(-5*time.Minute)))
	j.Write(testRecord("as-002", "prod", "api-server", "slo-breach", engine.ActionScaleUp, false, 0.7, now.Add(-4*time.Minute)))
	j.Write(testRecord("as-003", "prod", "api-server", "periodic", engine.ActionScaleDown, false, 0.6, now.Add(-3*time.Minute)))
	j.Write(testRecord("as-004", "prod", "web-frontend", "periodic", engine.ActionNoAction, false, 0.5, now.Add(-2*time.Minute)))

	stats, err := j.AggregateStats(1 * time.Hour)
	if err != nil {
		t.Fatalf("AggregateStats: %v", err)
	}
	if len(stats.TopServices) == 0 {
		t.Fatal("expected at least one service")
	}
	if stats.TopServices[0].Service != "api-server" {
		t.Errorf("top service should be api-server, got %q", stats.TopServices[0].Service)
	}
	if stats.TopServices[0].Count != 3 {
		t.Errorf("api-server count: want 3, got %d", stats.TopServices[0].Count)
	}
}

func TestJournal_AggregateStats_WindowExcludesOld(t *testing.T) {
	j, err := NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer j.Close()

	old := time.Now().UTC().Add(-48 * time.Hour)
	recent := time.Now().UTC().Add(-30 * time.Minute)
	j.Write(testRecord("aw-001", "prod", "api", "periodic", engine.ActionNoAction, false, 0.5, old))
	j.Write(testRecord("aw-002", "prod", "api", "slo-breach", engine.ActionScaleUp, false, 0.9, recent))

	stats, err := j.AggregateStats(1 * time.Hour)
	if err != nil {
		t.Fatalf("AggregateStats: %v", err)
	}
	if stats.TotalDecisions != 1 {
		t.Errorf("expected 1 decision in last hour, got %d", stats.TotalDecisions)
	}
}

func TestJournal_Purge_DeletesOld(t *testing.T) {
	j, err := NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer j.Close()

	old := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	j.Write(testRecord("p-001", "prod", "api", "periodic", engine.ActionNoAction, false, 0.5, old))
	j.Write(testRecord("p-002", "prod", "api", "periodic", engine.ActionScaleUp, false, 0.5, recent))

	cutoff := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	deleted, err := j.Purge(cutoff)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	got, _ := j.GetByID("p-002")
	if got == nil {
		t.Error("recent record should not be purged")
	}
	got, _ = j.GetByID("p-001")
	if got != nil {
		t.Error("old record should be purged")
	}
}

func TestJournal_Purge_KeepsRecent(t *testing.T) {
	j, err := NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer j.Close()

	recent := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	j.Write(testRecord("pk-001", "prod", "api", "periodic", engine.ActionNoAction, false, 0.5, recent))

	cutoff := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	deleted, err := j.Purge(cutoff)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted, got %d", deleted)
	}
}

func TestJournal_Purge_EmptyJournal(t *testing.T) {
	j, err := NewJournal(":memory:")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer j.Close()

	deleted, err := j.Purge(time.Now())
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted, got %d", deleted)
	}
}
