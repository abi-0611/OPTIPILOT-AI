package simulator

import (
	"context"
	"fmt"
	"time"
)

// ── SLO-Cost Curve Types ─────────────────────────────────────────────────────

// SLOCurveRequest defines the parameters for an SLO-cost curve sweep.
type SLOCurveRequest struct {
	Service string        `json:"service"`
	Start   time.Time     `json:"start"`
	End     time.Time     `json:"end"`
	Step    time.Duration `json:"step"`

	// SLO parameter to sweep. Interpretation depends on SLOMetric.
	SLOMetric string  `json:"slo_metric"` // e.g. "latency_p99", "error_rate", "availability"
	MinTarget float64 `json:"min_target"` // e.g. 0.050 (50ms)
	MaxTarget float64 `json:"max_target"` // e.g. 0.500 (500ms)
	Steps     int     `json:"steps"`      // number of sweep points (default 10)
}

// CurvePoint represents one data point on the SLO-cost curve.
type CurvePoint struct {
	SLOTarget              float64 `json:"slo_target"`
	ProjectedMonthlyCost   float64 `json:"projected_monthly_cost"`
	ProjectedCompliancePct float64 `json:"projected_compliance_percent"`
	AvgReplicas            float64 `json:"avg_replicas"`
	SLOBreaches            int     `json:"slo_breaches"`
	TotalSteps             int     `json:"total_steps"`
}

// ── SLO-Cost Curve Solver Factory ────────────────────────────────────────────

// SLOCurveSolverFactory creates a SolverFunc parameterized by an SLO target.
// The returned solver should behave differently depending on how tight/loose the target is.
type SLOCurveSolverFactory func(sloMetric string, sloTarget float64) SolverFunc

// ── SLO-Cost Curve Generator ─────────────────────────────────────────────────

// SLOCurveGenerator sweeps SLO targets and produces cost-compliance curves.
type SLOCurveGenerator struct {
	history       HistoryProvider
	decisions     DecisionProvider
	solverFactory SLOCurveSolverFactory
}

// NewSLOCurveGenerator creates a new generator.
func NewSLOCurveGenerator(history HistoryProvider, decisions DecisionProvider, factory SLOCurveSolverFactory) *SLOCurveGenerator {
	return &SLOCurveGenerator{
		history:       history,
		decisions:     decisions,
		solverFactory: factory,
	}
}

// Generate runs the SLO-cost sweep and returns one CurvePoint per SLO target step.
func (g *SLOCurveGenerator) Generate(ctx context.Context, req SLOCurveRequest) ([]CurvePoint, error) {
	if req.Service == "" {
		return nil, fmt.Errorf("service is required")
	}
	if req.End.Before(req.Start) || req.End.Equal(req.Start) {
		return nil, fmt.Errorf("end time must be after start time")
	}
	if req.Steps <= 0 {
		req.Steps = 10
	}
	if req.Step <= 0 {
		req.Step = 5 * time.Minute
	}
	if req.Steps == 1 {
		// Single-step: allow min == max.
		if req.MinTarget > req.MaxTarget {
			return nil, fmt.Errorf("min_target must be less than or equal to max_target")
		}
	} else if req.MinTarget >= req.MaxTarget {
		return nil, fmt.Errorf("min_target must be less than max_target")
	}

	stepSize := 0.0
	if req.Steps > 1 {
		stepSize = (req.MaxTarget - req.MinTarget) / float64(req.Steps-1)
	}

	// Duration in hours for monthly cost projection.
	windowHours := req.End.Sub(req.Start).Hours()
	if windowHours <= 0 {
		windowHours = 1
	}
	hoursPerMonth := 730.0 // ~365.25*24/12

	points := make([]CurvePoint, 0, req.Steps)

	for i := 0; i < req.Steps; i++ {
		target := req.MinTarget + float64(i)*stepSize

		solver := g.solverFactory(req.SLOMetric, target)
		sim := NewSimulator(g.history, g.decisions, solver)

		result, err := sim.Run(ctx, SimulationRequest{
			ID:       fmt.Sprintf("slo-curve-%s-%d", req.Service, i),
			Services: []string{req.Service},
			Start:    req.Start,
			End:      req.End,
			Step:     req.Step,
		})
		if err != nil {
			return nil, fmt.Errorf("simulation at target %.4f: %w", target, err)
		}

		// Compute average replicas across timeline.
		avgReplicas := 0.0
		if len(result.Timeline) > 0 {
			totalReplicas := 0.0
			for _, step := range result.Timeline {
				totalReplicas += float64(step.Simulated.Replicas)
			}
			avgReplicas = totalReplicas / float64(len(result.Timeline))
		}

		// Compute compliance percentage.
		compliancePct := 100.0
		if result.TotalSteps > 0 {
			compliancePct = float64(result.TotalSteps-result.SimulatedSLOBreaches) / float64(result.TotalSteps) * 100
		}

		// Project hourly cost to monthly.
		monthlyCost := 0.0
		if result.SimulatedCost.AvgHourlyCost > 0 {
			monthlyCost = result.SimulatedCost.AvgHourlyCost * hoursPerMonth
		} else if windowHours > 0 && result.SimulatedCost.TotalHourlyCost > 0 {
			// Estimate from total: distribute evenly across hours.
			hourlyAvg := result.SimulatedCost.TotalHourlyCost / float64(result.TotalSteps)
			monthlyCost = hourlyAvg * hoursPerMonth
		}

		points = append(points, CurvePoint{
			SLOTarget:              target,
			ProjectedMonthlyCost:   monthlyCost,
			ProjectedCompliancePct: compliancePct,
			AvgReplicas:            avgReplicas,
			SLOBreaches:            result.SimulatedSLOBreaches,
			TotalSteps:             result.TotalSteps,
		})
	}

	return points, nil
}
