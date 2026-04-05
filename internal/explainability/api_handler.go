package explainability

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// APIHandler serves the decision journal REST API.
type APIHandler struct {
	journal *Journal
}

// NewAPIHandler creates an HTTP handler backed by the given journal.
func NewAPIHandler(journal *Journal) *APIHandler {
	return &APIHandler{journal: journal}
}

// RegisterRoutes registers decision API routes on the given mux.
func (h *APIHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/decisions", h.handleListDecisions)
	mux.HandleFunc("GET /api/v1/decisions/{id}", h.handleGetDecision)
}

// handleListDecisions handles GET /api/v1/decisions?namespace=X&service=Y&since=T&limit=N
func (h *APIHandler) handleListDecisions(w http.ResponseWriter, r *http.Request) {
	filter := QueryFilter{}
	q := r.URL.Query()

	filter.Namespace = q.Get("namespace")
	filter.Service = q.Get("service")

	if sinceStr := q.Get("since"); sinceStr != "" {
		t, err := time.Parse(time.RFC3339, sinceStr)
		if err != nil {
			http.Error(w, "invalid 'since' format, use RFC3339", http.StatusBadRequest)
			return
		}
		filter.Since = &t
	}

	if limitStr := q.Get("limit"); limitStr != "" {
		limit, err := strconv.Atoi(limitStr)
		if err != nil || limit < 1 {
			http.Error(w, "invalid 'limit' value", http.StatusBadRequest)
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
		w.Write([]byte("[]"))
		return
	}
	json.NewEncoder(w).Encode(records)
}

// handleGetDecision handles GET /api/v1/decisions/{id}
func (h *APIHandler) handleGetDecision(w http.ResponseWriter, r *http.Request) {
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
