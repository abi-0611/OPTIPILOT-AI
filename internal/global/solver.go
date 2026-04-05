package global

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"

	globalv1 "github.com/optipilot-ai/optipilot/api/global/v1alpha1"
	hubgrpc "github.com/optipilot-ai/optipilot/internal/global/grpc"
)

// ---------------------------------------------------------------------------
// Solver input / output types
// ---------------------------------------------------------------------------

// ClusterSnapshot is the solver's view of a single spoke cluster.
type ClusterSnapshot struct {
	Name                 string
	Provider             string
	Region               string
	Health               string
	TotalCores           float64
	UsedCores            float64
	TotalMemoryGiB       float64
	UsedMemoryGiB        float64
	NodeCount            int32
	SLOCompliancePercent float64
	HourlyCostUSD        float64
	CarbonIntensityGCO2  float64
	Labels               map[string]string
	Excluded             bool // never hibernate
}

// UtilizationPercent returns the CPU utilization as a percentage.
func (c *ClusterSnapshot) UtilizationPercent() float64 {
	if c.TotalCores == 0 {
		return 0
	}
	return (c.UsedCores / c.TotalCores) * 100
}

// FreeCores returns available CPU cores.
func (c *ClusterSnapshot) FreeCores() float64 {
	return c.TotalCores - c.UsedCores
}

// SolverInput bundles everything the global solver needs.
type SolverInput struct {
	Clusters []*ClusterSnapshot
	Policy   *globalv1.GlobalPolicySpec
}

// SolverResult contains the directives produced by one optimization cycle.
type SolverResult struct {
	Directives []hubgrpc.Directive
	Summary    string
	Timestamp  time.Time
}

// ---------------------------------------------------------------------------
// Scoring dimensions
// ---------------------------------------------------------------------------

// ClusterScore holds the per-dimension normalized scores for a cluster.
type ClusterScore struct {
	Name    string
	Latency float64 // lower is better → normalized: higher = better
	Cost    float64 // lower is better → normalized: higher = better
	Carbon  float64 // lower is better → normalized: higher = better
	SLO     float64 // higher is better → normalized: higher = better
	Total   float64 // weighted composite
}

// strategyWeights returns the dimension weights for a given traffic strategy.
func strategyWeights(s globalv1.TrafficStrategy) (latency, cost, carbon, slo float64) {
	switch s {
	case globalv1.StrategyLatency:
		return 0.50, 0.15, 0.10, 0.25
	case globalv1.StrategyCost:
		return 0.10, 0.50, 0.15, 0.25
	case globalv1.StrategyCarbon:
		return 0.10, 0.15, 0.50, 0.25
	case globalv1.StrategyBalance:
		return 0.25, 0.25, 0.25, 0.25
	default:
		return 0.25, 0.25, 0.25, 0.25
	}
}

// ---------------------------------------------------------------------------
// Global Solver
// ---------------------------------------------------------------------------

// GlobalSolver produces cross-cluster directives based on cluster state and policy.
type GlobalSolver struct {
	// NowFn is injectable for testing.
	NowFn func() time.Time
}

// NewGlobalSolver creates a solver with real clock.
func NewGlobalSolver() *GlobalSolver {
	return &GlobalSolver{NowFn: time.Now}
}

// Solve runs one global optimization pass.
func (s *GlobalSolver) Solve(input *SolverInput) (*SolverResult, error) {
	if input == nil || len(input.Clusters) == 0 {
		return &SolverResult{Summary: "no clusters", Timestamp: s.NowFn()}, nil
	}
	if input.Policy == nil {
		return &SolverResult{Summary: "no policy", Timestamp: s.NowFn()}, nil
	}

	result := &SolverResult{Timestamp: s.NowFn()}

	// Phase 1: Traffic weight optimization.
	if input.Policy.TrafficShifting != nil {
		directives := s.solveTrafficWeights(input)
		result.Directives = append(result.Directives, directives...)
	}

	// Phase 2: Hibernation / wake-up.
	if input.Policy.ClusterLifecycle != nil && input.Policy.ClusterLifecycle.HibernationEnabled {
		directives := s.solveLifecycle(input)
		result.Directives = append(result.Directives, directives...)
	}

	// Build summary.
	traffic, hibernate, wakeup := 0, 0, 0
	for _, d := range result.Directives {
		switch d.Type {
		case hubgrpc.DirectiveTrafficShift:
			traffic++
		case hubgrpc.DirectiveHibernate:
			hibernate++
		case hubgrpc.DirectiveWakeUp:
			wakeup++
		}
	}
	result.Summary = fmt.Sprintf("directives: %d total (%d traffic, %d hibernate, %d wakeup)",
		len(result.Directives), traffic, hibernate, wakeup)

	return result, nil
}

// ---------------------------------------------------------------------------
// Traffic weight solver
// ---------------------------------------------------------------------------

func (s *GlobalSolver) solveTrafficWeights(input *SolverInput) []hubgrpc.Directive {
	ts := input.Policy.TrafficShifting
	strategy := ts.Strategy
	if strategy == "" {
		strategy = globalv1.StrategyBalance
	}

	// Filter to healthy/degraded clusters that can receive traffic.
	eligible := s.eligibleForTraffic(input.Clusters, ts.MinDestinationSLOPercent)
	if len(eligible) < 2 {
		return nil // need at least 2 clusters for weight distribution
	}

	// Score each cluster.
	scores := s.scoreAll(eligible, strategy)

	// Convert scores to weights (proportional to total score).
	weights := scoresToWeights(scores, ts.MaxShiftPerCyclePercent)

	if len(weights) == 0 {
		return nil
	}

	return []hubgrpc.Directive{{
		ID:             uuid.New().String(),
		Type:           hubgrpc.DirectiveTrafficShift,
		TrafficWeights: weights,
		Reason:         fmt.Sprintf("strategy=%s across %d clusters", strategy, len(eligible)),
	}}
}

// eligibleForTraffic returns clusters that are healthy enough for traffic.
func (s *GlobalSolver) eligibleForTraffic(all []*ClusterSnapshot, minSLO float64) []*ClusterSnapshot {
	var out []*ClusterSnapshot
	for _, c := range all {
		if c.Health == string(globalv1.ClusterHibernating) || c.Health == string(globalv1.ClusterUnreachable) {
			continue
		}
		if minSLO > 0 && c.SLOCompliancePercent < minSLO {
			continue
		}
		out = append(out, c)
	}
	return out
}

// scoreAll computes normalized multi-dimensional scores for eligible clusters.
func (s *GlobalSolver) scoreAll(clusters []*ClusterSnapshot, strategy globalv1.TrafficStrategy) []ClusterScore {
	wLat, wCost, wCarbon, wSLO := strategyWeights(strategy)

	// Collect raw values.
	type raw struct {
		name   string
		cost   float64
		carbon float64
		slo    float64
	}
	raws := make([]raw, len(clusters))
	maxCost, maxCarbon := 0.0, 0.0
	for i, c := range clusters {
		raws[i] = raw{
			name:   c.Name,
			cost:   c.HourlyCostUSD,
			carbon: c.CarbonIntensityGCO2,
			slo:    c.SLOCompliancePercent,
		}
		if c.HourlyCostUSD > maxCost {
			maxCost = c.HourlyCostUSD
		}
		if c.CarbonIntensityGCO2 > maxCarbon {
			maxCarbon = c.CarbonIntensityGCO2
		}
	}

	scores := make([]ClusterScore, len(clusters))
	for i, r := range raws {
		// Cost: lower is better → invert.
		costNorm := 1.0
		if maxCost > 0 {
			costNorm = 1.0 - (r.cost / maxCost)
		}

		// Carbon: lower is better → invert.
		carbonNorm := 1.0
		if maxCarbon > 0 {
			carbonNorm = 1.0 - (r.carbon / maxCarbon)
		}

		// SLO: higher is better → normalize to [0,1].
		sloNorm := r.slo / 100.0
		if sloNorm > 1 {
			sloNorm = 1
		}

		// Latency: without real latency probes, use a capacity-based proxy —
		// clusters with more free capacity get a better "latency" score
		// (less contention → lower request latency).
		latencyNorm := 0.0
		if clusters[i].TotalCores > 0 {
			latencyNorm = clusters[i].FreeCores() / clusters[i].TotalCores
		}

		total := wLat*latencyNorm + wCost*costNorm + wCarbon*carbonNorm + wSLO*sloNorm

		scores[i] = ClusterScore{
			Name:    r.name,
			Latency: latencyNorm,
			Cost:    costNorm,
			Carbon:  carbonNorm,
			SLO:     sloNorm,
			Total:   total,
		}
	}

	return scores
}

// scoresToWeights converts scores to integer weights (summing to 100).
// maxShiftPercent caps the difference from equal distribution.
func scoresToWeights(scores []ClusterScore, maxShiftPercent int32) map[string]int32 {
	n := len(scores)
	if n == 0 {
		return nil
	}

	// Sum of all total scores.
	totalScore := 0.0
	for _, sc := range scores {
		totalScore += sc.Total
	}
	if totalScore == 0 {
		// All scores zero → equal weights.
		w := int32(100 / n)
		out := make(map[string]int32, n)
		for _, sc := range scores {
			out[sc.Name] = w
		}
		return out
	}

	// Proportional weights.
	equalShare := 100.0 / float64(n)
	maxShift := float64(maxShiftPercent)
	if maxShift <= 0 {
		maxShift = 25 // default 25% max shift
	}

	rawWeights := make(map[string]float64, n)
	for _, sc := range scores {
		proportional := (sc.Total / totalScore) * 100.0
		// Clamp shift from equal distribution.
		delta := proportional - equalShare
		if delta > maxShift {
			delta = maxShift
		} else if delta < -maxShift {
			delta = -maxShift
		}
		rawWeights[sc.Name] = equalShare + delta
	}

	// Round to ints, ensure sum = 100.
	out := make(map[string]int32, n)
	total := int32(0)
	for name, w := range rawWeights {
		rounded := int32(math.Round(w))
		if rounded < 1 {
			rounded = 1
		}
		out[name] = rounded
		total += rounded
	}

	// Adjust for rounding error — add/subtract from the highest-scored cluster.
	if total != 100 {
		// Find the best cluster.
		best := scores[0].Name
		bestScore := scores[0].Total
		for _, sc := range scores[1:] {
			if sc.Total > bestScore {
				best = sc.Name
				bestScore = sc.Total
			}
		}
		out[best] += (100 - total)
	}

	return out
}

// ---------------------------------------------------------------------------
// Lifecycle solver (hibernation / wake-up)
// ---------------------------------------------------------------------------

func (s *GlobalSolver) solveLifecycle(input *SolverInput) []hubgrpc.Directive {
	lc := input.Policy.ClusterLifecycle
	if lc == nil || !lc.HibernationEnabled {
		return nil
	}

	idleThreshold := float64(lc.IdleThresholdPercent)
	if idleThreshold <= 0 {
		idleThreshold = 10 // default 10%
	}
	minActive := int(lc.MinActiveClusters)
	if minActive <= 0 {
		minActive = 1
	}

	// Build exclusion set.
	excluded := make(map[string]bool, len(lc.ExcludedClusters))
	for _, name := range lc.ExcludedClusters {
		excluded[name] = true
	}

	// Count active (non-hibernating) clusters.
	active := 0
	for _, c := range input.Clusters {
		if c.Health != string(globalv1.ClusterHibernating) {
			active++
		}
	}

	var directives []hubgrpc.Directive

	// Sort clusters by utilization ascending for hibernation candidates.
	sorted := make([]*ClusterSnapshot, len(input.Clusters))
	copy(sorted, input.Clusters)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].UtilizationPercent() < sorted[j].UtilizationPercent()
	})

	for _, c := range sorted {
		if active <= minActive {
			break
		}
		if excluded[c.Name] || c.Excluded {
			continue
		}
		// Already hibernating — skip.
		if c.Health == string(globalv1.ClusterHibernating) {
			continue
		}
		// Unreachable — can't hibernate what we can't reach.
		if c.Health == string(globalv1.ClusterUnreachable) {
			continue
		}

		if c.UtilizationPercent() < idleThreshold {
			directives = append(directives, hubgrpc.Directive{
				ID:              uuid.New().String(),
				Type:            hubgrpc.DirectiveHibernate,
				ClusterName:     c.Name,
				LifecycleAction: "hibernate",
				Reason: fmt.Sprintf("utilization %.1f%% < threshold %.0f%%, active clusters %d > min %d",
					c.UtilizationPercent(), idleThreshold, active, minActive),
			})
			active--
		}
	}

	// Wake-up: if active count dropped below minimum (shouldn't happen with guard above,
	// but handle edge cases) OR if all active clusters are heavily loaded.
	highUtilThreshold := 80.0
	allHeavy := true
	for _, c := range input.Clusters {
		if c.Health == string(globalv1.ClusterHibernating) {
			continue
		}
		if c.UtilizationPercent() < highUtilThreshold {
			allHeavy = false
			break
		}
	}

	if allHeavy {
		// Find the best hibernating cluster to wake.
		for _, c := range input.Clusters {
			if c.Health != string(globalv1.ClusterHibernating) {
				continue
			}
			directives = append(directives, hubgrpc.Directive{
				ID:              uuid.New().String(),
				Type:            hubgrpc.DirectiveWakeUp,
				ClusterName:     c.Name,
				LifecycleAction: "wake_up",
				Reason:          "all active clusters above 80% utilization",
			})
			break // wake one at a time
		}
	}

	return directives
}

// ---------------------------------------------------------------------------
// Helpers for building snapshots from CRD data
// ---------------------------------------------------------------------------

// SnapshotFromProfile builds a ClusterSnapshot from a ClusterProfile CRD.
func SnapshotFromProfile(cp *globalv1.ClusterProfile) *ClusterSnapshot {
	snap := &ClusterSnapshot{
		Name:                 cp.Name,
		Provider:             string(cp.Spec.Provider),
		Region:               cp.Spec.Region,
		Health:               string(cp.Status.Health),
		SLOCompliancePercent: cp.Status.SLOCompliancePercent,
		HourlyCostUSD:        cp.Status.HourlyCostUSD,
		CarbonIntensityGCO2:  cp.Spec.CarbonIntensityGCO2PerKWh,
		Labels:               cp.Spec.Labels,
	}
	if cp.Status.Capacity != nil {
		snap.TotalCores = cp.Status.Capacity.TotalCores
		snap.UsedCores = cp.Status.Capacity.UsedCores
		snap.TotalMemoryGiB = cp.Status.Capacity.TotalMemoryGiB
		snap.UsedMemoryGiB = cp.Status.Capacity.UsedMemoryGiB
		snap.NodeCount = cp.Status.Capacity.NodeCount
	}
	return snap
}
