package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/optipilot-ai/optipilot/internal/metrics"
	"github.com/optipilot-ai/optipilot/internal/tenant"
)

// TenantStateReader provides read access to tenant states.
// *tenant.Manager satisfies this interface.
type TenantStateReader interface {
	GetState(name string) *tenant.TenantState
	GetAllStates() map[string]*tenant.TenantState
}

// TenantAPIHandler serves the tenant fairness REST API.
type TenantAPIHandler struct {
	manager  TenantStateReader
	prom     metrics.PrometheusClient
	detector *tenant.NoisyNeighborDetector
}

// NewTenantAPIHandler creates a new handler.
// prom and detector are optional (nil disables time-series / noisy-neighbor signals).
func NewTenantAPIHandler(manager TenantStateReader, prom metrics.PrometheusClient, detector *tenant.NoisyNeighborDetector) *TenantAPIHandler {
	return &TenantAPIHandler{manager: manager, prom: prom, detector: detector}
}

// RegisterRoutes registers tenant API routes on the given mux.
func (h *TenantAPIHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/tenants/{name}/usage", h.handleTenantUsage)
	mux.HandleFunc("/api/v1/tenants/{name}", h.handleGetTenant)
	mux.HandleFunc("/api/v1/tenants", h.handleListTenants)
	mux.HandleFunc("/api/v1/fairness/impact/{service}", h.handleFairnessImpact)
	mux.HandleFunc("/api/v1/fairness", h.handleFairness)
}

// ── Response types ────────────────────────────────────────────────────────────

type tenantSummaryResponse struct {
	Name             string  `json:"name"`
	Tier             string  `json:"tier"`
	Weight           int32   `json:"weight"`
	CurrentCores     float64 `json:"current_cores"`
	CurrentMemoryGiB float64 `json:"current_memory_gib"`
	CurrentCostUSD   float64 `json:"current_cost_usd"`
	MaxCores         int32   `json:"max_cores,omitempty"`
	MaxMemoryGiB     int32   `json:"max_memory_gib,omitempty"`
	AllocationStatus string  `json:"allocation_status,omitempty"`
	FairnessScore    float64 `json:"fairness_score"`
	IsNoisy          bool    `json:"is_noisy"`
	IsVictim         bool    `json:"is_victim"`
}

type tenantDetailResponse struct {
	tenantSummaryResponse
	Namespaces             []string `json:"namespaces"`
	GuaranteedCoresPercent int32    `json:"guaranteed_cores_percent"`
	Burstable              bool     `json:"burstable"`
	MaxBurstPercent        int32    `json:"max_burst_percent"`
	MaxMonthlyCostUSD      float64  `json:"max_monthly_cost_usd,omitempty"`
	LastRefreshed          string   `json:"last_refreshed,omitempty"`
}

type usagePoint struct {
	Timestamp time.Time `json:"timestamp"`
	Cores     float64   `json:"cores"`
	MemoryGiB float64   `json:"memory_gib"`
}

type usageResponse struct {
	TenantName string       `json:"tenant_name"`
	MaxCores   int32        `json:"max_cores,omitempty"`
	History    []usagePoint `json:"history"`
}

type fairnessResponse struct {
	Timestamp   time.Time          `json:"timestamp"`
	GlobalIndex float64            `json:"global_index"`
	PerTenant   map[string]float64 `json:"per_tenant"`
}

type impactResponse struct {
	Service            string             `json:"service"`
	TenantName         string             `json:"tenant_name"`
	DeltaCores         float64            `json:"delta_cores"`
	CurrentIndex       float64            `json:"current_index"`
	ProjectedIndex     float64            `json:"projected_index"`
	IndexDelta         float64            `json:"index_delta"`
	AffectedTenants    []string           `json:"affected_tenants"`
	CurrentPerTenant   map[string]float64 `json:"current_per_tenant"`
	ProjectedPerTenant map[string]float64 `json:"projected_per_tenant"`
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "encoding error", http.StatusInternalServerError)
	}
}

func (h *TenantAPIHandler) isNoisy(name string) bool {
	if h.detector == nil {
		return false
	}
	return h.detector.IsNoisy(name)
}

func (h *TenantAPIHandler) isVictim(name string) bool {
	if h.detector == nil {
		return false
	}
	return h.detector.IsVictim(name)
}

func stateToSummary(s *tenant.TenantState, noisy, victim bool) tenantSummaryResponse {
	return tenantSummaryResponse{
		Name:             s.Name,
		Tier:             s.Tier,
		Weight:           s.Weight,
		CurrentCores:     s.CurrentCores,
		CurrentMemoryGiB: s.CurrentMemoryGiB,
		CurrentCostUSD:   s.CurrentCostUSD,
		MaxCores:         s.MaxCores,
		MaxMemoryGiB:     s.MaxMemoryGiB,
		AllocationStatus: s.AllocationStatus,
		FairnessScore:    s.FairnessScore,
		IsNoisy:          noisy,
		IsVictim:         victim,
	}
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// handleListTenants handles GET /api/v1/tenants
func (h *TenantAPIHandler) handleListTenants(w http.ResponseWriter, _ *http.Request) {
	if h.manager == nil {
		writeJSON(w, []tenantSummaryResponse{})
		return
	}
	all := h.manager.GetAllStates()
	result := make([]tenantSummaryResponse, 0, len(all))
	for _, s := range all {
		result = append(result, stateToSummary(s, h.isNoisy(s.Name), h.isVictim(s.Name)))
	}
	writeJSON(w, result)
}

// handleGetTenant handles GET /api/v1/tenants/{name}
func (h *TenantAPIHandler) handleGetTenant(w http.ResponseWriter, r *http.Request) {
	if h.manager == nil {
		http.Error(w, "tenant manager not configured", http.StatusServiceUnavailable)
		return
	}
	name := r.PathValue("name")
	s := h.manager.GetState(name)
	if s == nil {
		http.Error(w, "tenant not found", http.StatusNotFound)
		return
	}

	summary := stateToSummary(s, h.isNoisy(name), h.isVictim(name))
	detail := tenantDetailResponse{
		tenantSummaryResponse:  summary,
		Namespaces:             s.Namespaces,
		GuaranteedCoresPercent: s.GuaranteedCoresPercent,
		Burstable:              s.Burstable,
		MaxBurstPercent:        s.MaxBurstPercent,
		MaxMonthlyCostUSD:      s.MaxMonthlyCostUSD,
	}
	if !s.LastRefreshed.IsZero() {
		detail.LastRefreshed = s.LastRefreshed.UTC().Format(time.RFC3339)
	}
	writeJSON(w, detail)
}

// handleTenantUsage handles GET /api/v1/tenants/{name}/usage
// Returns time-series CPU + memory usage for the last hour (5-min steps).
// Falls back to a single current-state point if Prometheus is unavailable.
func (h *TenantAPIHandler) handleTenantUsage(w http.ResponseWriter, r *http.Request) {
	if h.manager == nil {
		http.Error(w, "tenant manager not configured", http.StatusServiceUnavailable)
		return
	}
	name := r.PathValue("name")
	s := h.manager.GetState(name)
	if s == nil {
		http.Error(w, "tenant not found", http.StatusNotFound)
		return
	}

	resp := usageResponse{
		TenantName: name,
		MaxCores:   s.MaxCores,
	}

	// Query Prometheus for time-series if available.
	if h.prom != nil && len(s.Namespaces) > 0 {
		nsRegex := strings.Join(s.Namespaces, "|")
		cpuQuery := `sum(namespace_cpu_usage_seconds_total{namespace=~"` + nsRegex + `"})`
		memQuery := `sum(namespace_memory_working_set_bytes{namespace=~"` + nsRegex + `"})`

		end := time.Now().UTC()
		start := end.Add(-1 * time.Hour)
		step := 5 * time.Minute

		cpuPoints, cpuErr := h.prom.QueryRange(r.Context(), cpuQuery, start, end, step)
		memPoints, memErr := h.prom.QueryRange(r.Context(), memQuery, start, end, step)

		if cpuErr == nil && memErr == nil && len(cpuPoints) > 0 {
			// Align memory points by timestamp into a map.
			memByTime := make(map[time.Time]float64, len(memPoints))
			for _, dp := range memPoints {
				memByTime[dp.Timestamp] = dp.Value
			}
			for _, dp := range cpuPoints {
				memGiB := memByTime[dp.Timestamp] / (1024 * 1024 * 1024)
				resp.History = append(resp.History, usagePoint{
					Timestamp: dp.Timestamp,
					Cores:     dp.Value,
					MemoryGiB: memGiB,
				})
			}
			writeJSON(w, resp)
			return
		}
	}

	// Fallback: return current state as a single data point.
	resp.History = []usagePoint{
		{
			Timestamp: s.LastRefreshed,
			Cores:     s.CurrentCores,
			MemoryGiB: s.CurrentMemoryGiB,
		},
	}
	writeJSON(w, resp)
}

// handleFairness handles GET /api/v1/fairness
// Returns the current Jain's fairness index computed from live tenant states.
func (h *TenantAPIHandler) handleFairness(w http.ResponseWriter, _ *http.Request) {
	if h.manager == nil {
		writeJSON(w, fairnessResponse{
			Timestamp: time.Now().UTC(),
			PerTenant: map[string]float64{},
		})
		return
	}
	all := h.manager.GetAllStates()
	inputs := make([]tenant.FairnessInput, 0, len(all))
	for _, s := range all {
		inputs = append(inputs, tenant.FairnessInput{
			Name:            s.Name,
			CurrentCores:    s.CurrentCores,
			GuaranteedCores: float64(s.GuaranteedCoresPercent), // will be scaled below
		})
	}

	result := tenant.ComputeFairness(inputs)
	resp := fairnessResponse{
		Timestamp: time.Now().UTC(),
		PerTenant: make(map[string]float64),
	}
	if result != nil {
		resp.GlobalIndex = result.GlobalIndex
		resp.PerTenant = result.PerTenant
	}
	writeJSON(w, resp)
}

// handleFairnessImpact handles GET /api/v1/fairness/impact/{service}
// Query params: tenant=<name>&delta_cores=<float>
// Simulates how adding delta_cores to the specified tenant changes the fairness index.
func (h *TenantAPIHandler) handleFairnessImpact(w http.ResponseWriter, r *http.Request) {
	if h.manager == nil {
		http.Error(w, "tenant manager not configured", http.StatusServiceUnavailable)
		return
	}
	service := r.PathValue("service")

	tenantName := r.URL.Query().Get("tenant")
	if tenantName == "" {
		http.Error(w, "missing 'tenant' query parameter", http.StatusBadRequest)
		return
	}

	deltaCores := 0.0
	if dc := r.URL.Query().Get("delta_cores"); dc != "" {
		v, err := strconv.ParseFloat(dc, 64)
		if err != nil {
			http.Error(w, "invalid 'delta_cores' value", http.StatusBadRequest)
			return
		}
		deltaCores = v
	}

	s := h.manager.GetState(tenantName)
	if s == nil {
		http.Error(w, "tenant not found", http.StatusNotFound)
		return
	}

	all := h.manager.GetAllStates()

	// Build current fairness inputs.
	inputs := make([]tenant.FairnessInput, 0, len(all))
	for _, ts := range all {
		inputs = append(inputs, tenant.FairnessInput{
			Name:            ts.Name,
			CurrentCores:    ts.CurrentCores,
			GuaranteedCores: float64(ts.GuaranteedCoresPercent),
		})
	}

	currentResult := tenant.ComputeFairness(inputs)

	// Build projected inputs: modify target tenant.
	projectedInputs := make([]tenant.FairnessInput, len(inputs))
	copy(projectedInputs, inputs)
	for i, in := range projectedInputs {
		if in.Name == tenantName {
			projectedInputs[i].CurrentCores += deltaCores
			break
		}
	}

	projectedResult := tenant.ComputeFairness(projectedInputs)

	currentIndex := 0.0
	currentPerTenant := make(map[string]float64)
	if currentResult != nil {
		currentIndex = currentResult.GlobalIndex
		currentPerTenant = currentResult.PerTenant
	}

	projectedIndex := 0.0
	projectedPerTenant := make(map[string]float64)
	if projectedResult != nil {
		projectedIndex = projectedResult.GlobalIndex
		projectedPerTenant = projectedResult.PerTenant
	}

	// Tenants whose fairness score changes.
	var affected []string
	for name, ps := range projectedPerTenant {
		cs := currentPerTenant[name]
		if ps != cs {
			if name != tenantName {
				affected = append(affected, name)
			}
		}
	}

	writeJSON(w, impactResponse{
		Service:            service,
		TenantName:         tenantName,
		DeltaCores:         deltaCores,
		CurrentIndex:       currentIndex,
		ProjectedIndex:     projectedIndex,
		IndexDelta:         projectedIndex - currentIndex,
		AffectedTenants:    affected,
		CurrentPerTenant:   currentPerTenant,
		ProjectedPerTenant: projectedPerTenant,
	})
}
