package simulator

import (
	"context"
	"fmt"
	"time"
)

// ── Interfaces ───────────────────────────────────────────────────────────────

// HistoryProvider supplies historical time-series data for simulation replay.
// Satisfied by Prometheus QueryRange or synthetic test data.
type HistoryProvider interface {
	// FetchHistory returns ordered (timestamp, value) data points for a metric
	// query within the given time range at the requested step interval.
	FetchHistory(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]DataPoint, error)
}

// DecisionProvider supplies the actual decisions that were made during a time range.
type DecisionProvider interface {
	// FetchDecisions returns actual decisions made for the given services during [start, end].
	FetchDecisions(ctx context.Context, services []string, start, end time.Time) ([]HistoricalDecision, error)
}

// SolverFunc is the simulation-time solver. Given a snapshot, it returns a simulated action.
// This allows the caller to inject modified policy or SLO targets.
type SolverFunc func(snapshot SimulationSnapshot) SimulatedAction

// ── Data types ───────────────────────────────────────────────────────────────

// DataPoint represents a single (timestamp, value) sample.
type DataPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

// HistoricalDecision represents one actual decision from the Decision Journal.
type HistoricalDecision struct {
	ID          string    `json:"id"`
	Timestamp   time.Time `json:"timestamp"`
	Service     string    `json:"service"`
	Namespace   string    `json:"namespace"`
	Action      string    `json:"action"` // scale_up, scale_down, tune, no_action
	Replicas    int32     `json:"replicas"`
	CPUCores    float64   `json:"cpu_cores"`
	HourlyCost  float64   `json:"hourly_cost"`
	SLOBreached bool      `json:"slo_breached"`
}

// SimulationSnapshot captures the state at a single point in simulated time.
type SimulationSnapshot struct {
	Timestamp   time.Time `json:"timestamp"`
	Service     string    `json:"service"`
	CPUUsage    float64   `json:"cpu_usage"`
	MemoryUsage float64   `json:"memory_usage"`
	RequestRate float64   `json:"request_rate"`
	LatencyP99  float64   `json:"latency_p99"`
	ErrorRate   float64   `json:"error_rate"`
	Replicas    int32     `json:"replicas"`
}

// SimulatedAction is the solver's output at one simulation step.
type SimulatedAction struct {
	Action      string  `json:"action"` // scale_up, scale_down, tune, no_action
	Replicas    int32   `json:"replicas"`
	CPUCores    float64 `json:"cpu_cores"`
	HourlyCost  float64 `json:"hourly_cost"`
	SLOBreached bool    `json:"slo_breached"`
}

// SimulatedStep pairs one simulation snapshot with the action taken.
type SimulatedStep struct {
	Snapshot  SimulationSnapshot  `json:"snapshot"`
	Original  *HistoricalDecision `json:"original,omitempty"`
	Simulated SimulatedAction     `json:"simulated"`
}

// ── Request / Result ─────────────────────────────────────────────────────────

// SimulationRequest defines a what-if simulation run.
type SimulationRequest struct {
	ID       string        `json:"id"`
	Services []string      `json:"services"`
	Start    time.Time     `json:"start"`
	End      time.Time     `json:"end"`
	Step     time.Duration `json:"step"`

	// Human-readable description of what changed.
	Description string `json:"description,omitempty"`
}

// CostSummary holds cost aggregation for one side of the comparison.
type CostSummary struct {
	TotalHourlyCost float64 `json:"total_hourly_cost"`
	AvgHourlyCost   float64 `json:"avg_hourly_cost"`
	PeakHourlyCost  float64 `json:"peak_hourly_cost"`
}

// SimulationResult holds the full output of a what-if simulation.
type SimulationResult struct {
	ID          string    `json:"id"`
	Description string    `json:"description,omitempty"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Duration    string    `json:"duration"`

	// Per-step timeline.
	Timeline []SimulatedStep `json:"timeline"`

	// Aggregated comparison.
	OriginalCost     CostSummary `json:"original_cost"`
	SimulatedCost    CostSummary `json:"simulated_cost"`
	CostDeltaPercent float64     `json:"cost_delta_percent"`

	OriginalSLOBreaches  int `json:"original_slo_breaches"`
	SimulatedSLOBreaches int `json:"simulated_slo_breaches"`

	TotalSteps int `json:"total_steps"`
}

// ── Simulator ────────────────────────────────────────────────────────────────

// Simulator replays historical metrics with an alternative solver to compare outcomes.
type Simulator struct {
	history   HistoryProvider
	decisions DecisionProvider
	solver    SolverFunc
}

// NewSimulator creates a Simulator with the given providers and solver function.
func NewSimulator(history HistoryProvider, decisions DecisionProvider, solver SolverFunc) *Simulator {
	return &Simulator{
		history:   history,
		decisions: decisions,
		solver:    solver,
	}
}

// Run executes the simulation over the request's time range for all specified services.
func (s *Simulator) Run(ctx context.Context, req SimulationRequest) (*SimulationResult, error) {
	if len(req.Services) == 0 {
		return nil, fmt.Errorf("no services specified")
	}
	if req.End.Before(req.Start) || req.End.Equal(req.Start) {
		return nil, fmt.Errorf("end time must be after start time")
	}
	if req.Step <= 0 {
		req.Step = 5 * time.Minute
	}

	// Fetch actual decisions.
	actualDecisions, err := s.decisions.FetchDecisions(ctx, req.Services, req.Start, req.End)
	if err != nil {
		return nil, fmt.Errorf("fetching decisions: %w", err)
	}

	// Index actual decisions by (service, rounded timestamp) for lookup.
	decisionIndex := make(map[string]*HistoricalDecision, len(actualDecisions))
	for i := range actualDecisions {
		d := &actualDecisions[i]
		key := fmt.Sprintf("%s@%d", d.Service, d.Timestamp.Truncate(req.Step).Unix())
		decisionIndex[key] = d
	}

	// For each service, fetch CPU, latency, error rate, request rate history.
	var timeline []SimulatedStep

	for _, svc := range req.Services {
		cpuData, err := s.history.FetchHistory(ctx,
			fmt.Sprintf(`avg(rate(container_cpu_usage_seconds_total{service="%s"}[5m]))`, svc),
			req.Start, req.End, req.Step)
		if err != nil {
			return nil, fmt.Errorf("fetching CPU history for %s: %w", svc, err)
		}

		latencyData, err := s.history.FetchHistory(ctx,
			fmt.Sprintf(`histogram_quantile(0.99, rate(http_request_duration_seconds_bucket{service="%s"}[5m]))`, svc),
			req.Start, req.End, req.Step)
		if err != nil {
			// Non-fatal: latency data may not be available for all services.
			latencyData = nil
		}

		errorData, err := s.history.FetchHistory(ctx,
			fmt.Sprintf(`rate(http_requests_total{service="%s",code=~"5.."}[5m])`, svc),
			req.Start, req.End, req.Step)
		if err != nil {
			errorData = nil
		}

		rpsData, err := s.history.FetchHistory(ctx,
			fmt.Sprintf(`rate(http_requests_total{service="%s"}[5m])`, svc),
			req.Start, req.End, req.Step)
		if err != nil {
			rpsData = nil
		}

		// Build a latency/error/rps map by timestamp for alignment.
		latencyMap := indexByTime(latencyData)
		errorMap := indexByTime(errorData)
		rpsMap := indexByTime(rpsData)

		for _, dp := range cpuData {
			snap := SimulationSnapshot{
				Timestamp:   dp.Timestamp,
				Service:     svc,
				CPUUsage:    dp.Value,
				LatencyP99:  latencyMap[dp.Timestamp.Unix()],
				ErrorRate:   errorMap[dp.Timestamp.Unix()],
				RequestRate: rpsMap[dp.Timestamp.Unix()],
			}

			// Simulate with the alternative solver.
			simAction := s.solver(snap)

			step := SimulatedStep{
				Snapshot:  snap,
				Simulated: simAction,
			}

			// Match to actual decision if one exists.
			key := fmt.Sprintf("%s@%d", svc, dp.Timestamp.Truncate(req.Step).Unix())
			if orig, ok := decisionIndex[key]; ok {
				step.Original = orig
			}

			timeline = append(timeline, step)
		}
	}

	result := buildResult(req, timeline)
	return result, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func indexByTime(data []DataPoint) map[int64]float64 {
	m := make(map[int64]float64, len(data))
	for _, dp := range data {
		m[dp.Timestamp.Unix()] = dp.Value
	}
	return m
}

func buildResult(req SimulationRequest, timeline []SimulatedStep) *SimulationResult {
	result := &SimulationResult{
		ID:          req.ID,
		Description: req.Description,
		Start:       req.Start,
		End:         req.End,
		Duration:    req.End.Sub(req.Start).String(),
		Timeline:    timeline,
		TotalSteps:  len(timeline),
	}

	var origTotal, simTotal, origPeak, simPeak float64
	origCount, simCount := 0, 0

	for _, step := range timeline {
		// Simulated costs.
		simTotal += step.Simulated.HourlyCost
		simCount++
		if step.Simulated.HourlyCost > simPeak {
			simPeak = step.Simulated.HourlyCost
		}
		if step.Simulated.SLOBreached {
			result.SimulatedSLOBreaches++
		}

		// Original costs (from actual decisions).
		if step.Original != nil {
			origTotal += step.Original.HourlyCost
			origCount++
			if step.Original.HourlyCost > origPeak {
				origPeak = step.Original.HourlyCost
			}
			if step.Original.SLOBreached {
				result.OriginalSLOBreaches++
			}
		}
	}

	if simCount > 0 {
		result.SimulatedCost = CostSummary{
			TotalHourlyCost: simTotal,
			AvgHourlyCost:   simTotal / float64(simCount),
			PeakHourlyCost:  simPeak,
		}
	}
	if origCount > 0 {
		result.OriginalCost = CostSummary{
			TotalHourlyCost: origTotal,
			AvgHourlyCost:   origTotal / float64(origCount),
			PeakHourlyCost:  origPeak,
		}
		if origTotal > 0 {
			result.CostDeltaPercent = ((simTotal - origTotal) / origTotal) * 100
		}
	}

	return result
}
