package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/optipilot-ai/optipilot/internal/cel"
	"github.com/optipilot-ai/optipilot/internal/engine"
	"github.com/optipilot-ai/optipilot/internal/explainability"
)

// ── Fakes ─────────────────────────────────────────────────────────────────────

type fakeJournal struct {
	records []engine.DecisionRecord
	stats   *explainability.JournalStats
}

func (f *fakeJournal) Query(filter explainability.QueryFilter) ([]engine.DecisionRecord, error) {
	var out []engine.DecisionRecord
	for _, r := range f.records {
		if filter.Service != "" && r.Service != filter.Service {
			continue
		}
		if filter.Namespace != "" && r.Namespace != filter.Namespace {
			continue
		}
		if filter.Trigger != "" && r.Trigger != filter.Trigger {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeJournal) GetByID(id string) (*engine.DecisionRecord, error) {
	for i, r := range f.records {
		if r.ID == id {
			return &f.records[i], nil
		}
	}
	return nil, nil
}

func (f *fakeJournal) Search(text string, limit int) ([]engine.DecisionRecord, error) {
	var out []engine.DecisionRecord
	for _, r := range f.records {
		if len(out) >= limit {
			break
		}
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeJournal) AggregateStats(window time.Duration) (*explainability.JournalStats, error) {
	if f.stats != nil {
		return f.stats, nil
	}
	return &explainability.JournalStats{TotalDecisions: len(f.records)}, nil
}

type fakeNarrator struct {
	text string
}

func (f *fakeNarrator) NarrateDecision(r engine.DecisionRecord) string {
	if f.text != "" {
		return f.text
	}
	return "Decision: " + r.ID
}

func newDecisionRecord(id, service, trigger string) engine.DecisionRecord {
	return engine.DecisionRecord{
		ID:         id,
		Timestamp:  time.Now(),
		Namespace:  "default",
		Service:    service,
		Trigger:    trigger,
		ActionType: engine.ActionType("scale_up"),
		SLOStatus:  cel.SLOStatus{Compliant: true},
		Confidence: 0.9,
	}
}

func setupDecisionsHandler(journal *fakeJournal) (*DecisionsAPIHandler, *http.ServeMux) {
	h := NewDecisionsAPIHandler(journal).WithNarrator(&fakeNarrator{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return h, mux
}

// ── List decisions ─────────────────────────────────────────────────────────────

func TestDecisionsAPI_ListAll(t *testing.T) {
	j := &fakeJournal{records: []engine.DecisionRecord{
		newDecisionRecord("id-1", "api-server", "slo_breach"),
		newDecisionRecord("id-2", "worker", "scale"),
	}}
	_, mux := setupDecisionsHandler(j)

	req := httptest.NewRequest("GET", "/api/v1/decisions", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var records []engine.DecisionRecord
	if err := json.NewDecoder(rec.Body).Decode(&records); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("want 2 records, got %d", len(records))
	}
}

func TestDecisionsAPI_ListFilterByService(t *testing.T) {
	j := &fakeJournal{records: []engine.DecisionRecord{
		newDecisionRecord("id-1", "api-server", "slo_breach"),
		newDecisionRecord("id-2", "worker", "scale"),
	}}
	_, mux := setupDecisionsHandler(j)

	req := httptest.NewRequest("GET", "/api/v1/decisions?service=api-server", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var records []engine.DecisionRecord
	json.NewDecoder(rec.Body).Decode(&records)
	if len(records) != 1 || records[0].Service != "api-server" {
		t.Errorf("expected 1 api-server record, got %+v", records)
	}
}

func TestDecisionsAPI_ListFilterByTrigger(t *testing.T) {
	j := &fakeJournal{records: []engine.DecisionRecord{
		newDecisionRecord("id-1", "api-server", "slo_breach"),
		newDecisionRecord("id-2", "worker", "scale"),
	}}
	_, mux := setupDecisionsHandler(j)

	req := httptest.NewRequest("GET", "/api/v1/decisions?trigger=slo_breach", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var records []engine.DecisionRecord
	json.NewDecoder(rec.Body).Decode(&records)
	if len(records) != 1 || records[0].Trigger != "slo_breach" {
		t.Errorf("expected 1 slo_breach record, got %+v", records)
	}
}

func TestDecisionsAPI_ListInvalidLimit(t *testing.T) {
	_, mux := setupDecisionsHandler(&fakeJournal{})

	req := httptest.NewRequest("GET", "/api/v1/decisions?limit=abc", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", rec.Code)
	}
}

func TestDecisionsAPI_ListEmptyReturnsArray(t *testing.T) {
	_, mux := setupDecisionsHandler(&fakeJournal{})

	req := httptest.NewRequest("GET", "/api/v1/decisions", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var records []interface{}
	if err := json.NewDecoder(rec.Body).Decode(&records); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected empty array, got %v", records)
	}
}

// ── Get by ID ─────────────────────────────────────────────────────────────────

func TestDecisionsAPI_GetByID(t *testing.T) {
	j := &fakeJournal{records: []engine.DecisionRecord{
		newDecisionRecord("abc-123", "api-server", "scale"),
	}}
	_, mux := setupDecisionsHandler(j)

	req := httptest.NewRequest("GET", "/api/v1/decisions/abc-123", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var record engine.DecisionRecord
	if err := json.NewDecoder(rec.Body).Decode(&record); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if record.ID != "abc-123" {
		t.Errorf("expected id abc-123, got %s", record.ID)
	}
}

func TestDecisionsAPI_GetByID_NotFound(t *testing.T) {
	_, mux := setupDecisionsHandler(&fakeJournal{})

	req := httptest.NewRequest("GET", "/api/v1/decisions/no-such-id", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", rec.Code)
	}
}

// ── Explain ────────────────────────────────────────────────────────────────────

func TestDecisionsAPI_Explain(t *testing.T) {
	j := &fakeJournal{records: []engine.DecisionRecord{
		newDecisionRecord("xyz-999", "api-server", "scale"),
	}}
	h := NewDecisionsAPIHandler(j).WithNarrator(&fakeNarrator{text: "Scale-up recommended."})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/v1/decisions/xyz-999/explain", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["id"] != "xyz-999" {
		t.Errorf("id: want xyz-999, got %s", resp["id"])
	}
	if resp["narrative"] != "Scale-up recommended." {
		t.Errorf("narrative: want 'Scale-up recommended.', got %s", resp["narrative"])
	}
}

func TestDecisionsAPI_Explain_NotFound(t *testing.T) {
	_, mux := setupDecisionsHandler(&fakeJournal{})

	req := httptest.NewRequest("GET", "/api/v1/decisions/missing/explain", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", rec.Code)
	}
}

// ── Summary ────────────────────────────────────────────────────────────────────

func TestDecisionsAPI_Summary(t *testing.T) {
	j := &fakeJournal{
		records: []engine.DecisionRecord{newDecisionRecord("id-1", "svc", "scale")},
		stats: &explainability.JournalStats{
			TotalDecisions:    42,
			DecisionsPerHour:  1.75,
			AverageConfidence: 0.88,
		},
	}
	_, mux := setupDecisionsHandler(j)

	req := httptest.NewRequest("GET", "/api/v1/decisions/summary?window=12h", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var stats explainability.JournalStats
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stats.TotalDecisions != 42 {
		t.Errorf("TotalDecisions: want 42, got %d", stats.TotalDecisions)
	}
}

func TestDecisionsAPI_SummaryDefaultWindow(t *testing.T) {
	_, mux := setupDecisionsHandler(&fakeJournal{})

	req := httptest.NewRequest("GET", "/api/v1/decisions/summary", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("want 200 (default window), got %d", rec.Code)
	}
}

func TestDecisionsAPI_SummaryInvalidWindow(t *testing.T) {
	_, mux := setupDecisionsHandler(&fakeJournal{})

	req := httptest.NewRequest("GET", "/api/v1/decisions/summary?window=bad", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", rec.Code)
	}
}

// ── Search ─────────────────────────────────────────────────────────────────────

func TestDecisionsAPI_Search(t *testing.T) {
	j := &fakeJournal{records: []engine.DecisionRecord{
		newDecisionRecord("id-1", "api-server", "scale"),
	}}
	_, mux := setupDecisionsHandler(j)

	req := httptest.NewRequest("GET", "/api/v1/decisions/search?q=scale", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var records []engine.DecisionRecord
	json.NewDecoder(rec.Body).Decode(&records)
	if len(records) == 0 {
		t.Error("expected at least 1 result")
	}
}

func TestDecisionsAPI_SearchMissingQuery(t *testing.T) {
	_, mux := setupDecisionsHandler(&fakeJournal{})

	req := httptest.NewRequest("GET", "/api/v1/decisions/search", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", rec.Code)
	}
}

// ── Content-Type ───────────────────────────────────────────────────────────────

func TestDecisionsAPI_ContentTypeJSON(t *testing.T) {
	j := &fakeJournal{records: []engine.DecisionRecord{
		newDecisionRecord("id-1", "svc", "scale"),
	}}
	_, mux := setupDecisionsHandler(j)

	for _, path := range []string{
		"/api/v1/decisions",
		"/api/v1/decisions/id-1",
		"/api/v1/decisions/id-1/explain",
		"/api/v1/decisions/summary",
	} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		ct := rec.Header().Get("Content-Type")
		if ct != "application/json" {
			t.Errorf("path %s: want application/json content-type, got %q", path, ct)
		}
	}
}

// ── Context propagation ────────────────────────────────────────────────────────

func TestDecisionsAPI_ContextPropagated(t *testing.T) {
	j := &fakeJournal{}
	_, mux := setupDecisionsHandler(j)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	req := httptest.NewRequest("GET", "/api/v1/decisions", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	// Should not panic even with cancelled context.
	mux.ServeHTTP(rec, req)
}
