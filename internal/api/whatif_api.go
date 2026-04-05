package api

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/optipilot-ai/optipilot/internal/simulator"
)

// WhatIfAPIHandler serves the What-If Simulator and SLO-Cost Curve REST API.
// Results are stored in memory keyed by simulation ID.
type WhatIfAPIHandler struct {
	history      simulator.HistoryProvider
	decisions    simulator.DecisionProvider
	solverFunc   simulator.SolverFunc
	curveFactory simulator.SLOCurveSolverFactory

	mu      sync.RWMutex
	results map[string]*simulator.SimulationResult
}

// NewWhatIfAPIHandler creates the handler.
// solverFunc is the default solver used for what-if simulations.
// curveFactory is used for SLO-cost curve sweeps.
func NewWhatIfAPIHandler(
	history simulator.HistoryProvider,
	decisions simulator.DecisionProvider,
	solverFunc simulator.SolverFunc,
	curveFactory simulator.SLOCurveSolverFactory,
) *WhatIfAPIHandler {
	return &WhatIfAPIHandler{
		history:      history,
		decisions:    decisions,
		solverFunc:   solverFunc,
		curveFactory: curveFactory,
		results:      make(map[string]*simulator.SimulationResult),
	}
}

// RegisterRoutes registers what-if simulation routes on the given mux.
func (h *WhatIfAPIHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/simulate", h.handleRunSimulation)
	mux.HandleFunc("GET /api/v1/simulate/{id}", h.handleGetSimulation)
	mux.HandleFunc("POST /api/v1/simulate/slo-cost-curve", h.handleSLOCostCurve)
}

// ── Request / response types ─────────────────────────────────────────────────

type simulationRequestBody struct {
	Services    []string      `json:"services"`
	Start       time.Time     `json:"start"`
	End         time.Time     `json:"end"`
	Step        time.Duration `json:"step"`
	Description string        `json:"description"`
}

type sloCurveRequestBody struct {
	Service   string        `json:"service"`
	Start     time.Time     `json:"start"`
	End       time.Time     `json:"end"`
	Step      time.Duration `json:"step"`
	SLOMetric string        `json:"slo_metric"`
	MinTarget float64       `json:"min_target"`
	MaxTarget float64       `json:"max_target"`
	Steps     int           `json:"steps"`
}

// ── Handlers ─────────────────────────────────────────────────────────────────

// handleRunSimulation handles POST /api/v1/simulate
func (h *WhatIfAPIHandler) handleRunSimulation(w http.ResponseWriter, r *http.Request) {
	var body simulationRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if len(body.Services) == 0 {
		http.Error(w, "services is required", http.StatusBadRequest)
		return
	}
	if body.End.IsZero() || body.Start.IsZero() {
		http.Error(w, "start and end are required", http.StatusBadRequest)
		return
	}
	if !body.End.After(body.Start) {
		http.Error(w, "end must be after start", http.StatusBadRequest)
		return
	}

	id := uuid.New().String()

	sim := simulator.NewSimulator(h.history, h.decisions, h.solverFunc)
	result, err := sim.Run(r.Context(), simulator.SimulationRequest{
		ID:          id,
		Services:    body.Services,
		Start:       body.Start,
		End:         body.End,
		Step:        body.Step,
		Description: body.Description,
	})
	if err != nil {
		http.Error(w, "simulation failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.mu.Lock()
	h.results[id] = result
	h.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(result)
}

// handleGetSimulation handles GET /api/v1/simulate/{id}
func (h *WhatIfAPIHandler) handleGetSimulation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	h.mu.RLock()
	result, ok := h.results[id]
	h.mu.RUnlock()

	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleSLOCostCurve handles POST /api/v1/simulate/slo-cost-curve
func (h *WhatIfAPIHandler) handleSLOCostCurve(w http.ResponseWriter, r *http.Request) {
	var body sloCurveRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	gen := simulator.NewSLOCurveGenerator(h.history, h.decisions, h.curveFactory)
	points, err := gen.Generate(r.Context(), simulator.SLOCurveRequest{
		Service:   body.Service,
		Start:     body.Start,
		End:       body.End,
		Step:      body.Step,
		SLOMetric: body.SLOMetric,
		MinTarget: body.MinTarget,
		MaxTarget: body.MaxTarget,
		Steps:     body.Steps,
	})
	if err != nil {
		http.Error(w, "curve generation failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"service":    body.Service,
		"slo_metric": body.SLOMetric,
		"points":     points,
	})
}
