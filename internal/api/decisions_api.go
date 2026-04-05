package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/optipilot-ai/optipilot/internal/engine"
	"github.com/optipilot-ai/optipilot/internal/explainability"
)

// DecisionJournal is the subset of explainability.Journal used by this handler.
type DecisionJournal interface {
	Query(filter explainability.QueryFilter) ([]engine.DecisionRecord, error)
	GetByID(id string) (*engine.DecisionRecord, error)
	Search(text string, limit int) ([]engine.DecisionRecord, error)
	AggregateStats(window time.Duration) (*explainability.JournalStats, error)
}

// DecisionNarrator is the subset of the narrator used by this handler.
type DecisionNarrator interface {
	NarrateDecision(r engine.DecisionRecord) string
}

// narratorAdapter wraps the package-level NarrateDecision func.
type narratorAdapter struct{}

func (narratorAdapter) NarrateDecision(r engine.DecisionRecord) string {
	return explainability.NarrateDecision(r)
}

// DecisionsAPIHandler extends the decision journal REST API with explain + summary.
type DecisionsAPIHandler struct {
	journal  DecisionJournal
	narrator DecisionNarrator
}

// NewDecisionsAPIHandler creates a handler using the given journal.
// The narrator defaults to the package-level NarrateDecision function.
func NewDecisionsAPIHandler(journal DecisionJournal) *DecisionsAPIHandler {
	return &DecisionsAPIHandler{
		journal:  journal,
		narrator: narratorAdapter{},
	}
}

// WithNarrator overrides the default narrator (useful for testing).
func (h *DecisionsAPIHandler) WithNarrator(n DecisionNarrator) *DecisionsAPIHandler {
	h.narrator = n
	return h
}

// RegisterRoutes registers decision-related routes on the given mux.
// These routes augment (not replace) the base routes in explainability.APIHandler.
func (h *DecisionsAPIHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/decisions", h.handleListDecisions)
	mux.HandleFunc("GET /api/v1/decisions/summary", h.handleSummary)
	mux.HandleFunc("GET /api/v1/decisions/search", h.handleSearch)
	mux.HandleFunc("GET /api/v1/decisions/{id}", h.handleGetDecision)
	mux.HandleFunc("GET /api/v1/decisions/{id}/explain", h.handleExplain)
}

// handleListDecisions handles GET /api/v1/decisions?namespace=X&service=Y&trigger=Z&since=T&limit=N
func (h *DecisionsAPIHandler) handleListDecisions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := explainability.QueryFilter{
		Namespace: q.Get("namespace"),
		Service:   q.Get("service"),
		Trigger:   q.Get("trigger"),
	}

	if sinceStr := q.Get("since"); sinceStr != "" {
		t, err := time.Parse(time.RFC3339, sinceStr)
		if err != nil {
			http.Error(w, "invalid 'since' (use RFC3339)", http.StatusBadRequest)
			return
		}
		filter.Since = &t
	}

	if limitStr := q.Get("limit"); limitStr != "" {
		limit, err := strconv.Atoi(limitStr)
		if err != nil || limit < 1 {
			http.Error(w, "invalid 'limit'", http.StatusBadRequest)
			return
		}
		filter.Limit = limit
	}

	records, err := h.journal.Query(filter)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if records == nil {
		records = []engine.DecisionRecord{}
	}
	json.NewEncoder(w).Encode(records)
}

// handleGetDecision handles GET /api/v1/decisions/{id}
func (h *DecisionsAPIHandler) handleGetDecision(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	record, err := h.journal.GetByID(id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if record == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(record)
}

// handleExplain handles GET /api/v1/decisions/{id}/explain
// Returns a human-readable narrative for the decision.
func (h *DecisionsAPIHandler) handleExplain(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	record, err := h.journal.GetByID(id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if record == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	narrative := h.narrator.NarrateDecision(*record)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"id":        id,
		"narrative": narrative,
	})
}

// handleSummary handles GET /api/v1/decisions/summary?window=24h
func (h *DecisionsAPIHandler) handleSummary(w http.ResponseWriter, r *http.Request) {
	windowStr := r.URL.Query().Get("window")
	if windowStr == "" {
		windowStr = "24h"
	}

	window, err := time.ParseDuration(windowStr)
	if err != nil || window <= 0 {
		http.Error(w, "invalid 'window' (e.g. 1h, 24h, 7d not supported — use hours)", http.StatusBadRequest)
		return
	}

	stats, err := h.journal.AggregateStats(window)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// handleSearch handles GET /api/v1/decisions/search?q=text&limit=N
func (h *DecisionsAPIHandler) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	text := q.Get("q")
	if text == "" {
		http.Error(w, "missing 'q' query parameter", http.StatusBadRequest)
		return
	}

	limit := 20
	if limitStr := q.Get("limit"); limitStr != "" {
		v, err := strconv.Atoi(limitStr)
		if err != nil || v < 1 {
			http.Error(w, "invalid 'limit'", http.StatusBadRequest)
			return
		}
		limit = v
	}

	records, err := h.journal.Search(text, limit)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if records == nil {
		records = []engine.DecisionRecord{}
	}
	json.NewEncoder(w).Encode(records)
}
