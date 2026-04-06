package engine

import (
	"fmt"
	"math"
	"math/rand"

	"github.com/optipilot-ai/optipilot/internal/cel"
)

// DefaultMaxCandidates is the default cap on total candidate plans per service.
const DefaultMaxCandidates = 100

// replicaMultipliers defines the set of scaling factors applied to the current replica count.
var replicaMultipliers = []float64{0.5, 0.75, 0.9, 1.0, 1.1, 1.25, 1.5, 2.0}

// spotRatioVariants defines the spot ratios to explore.
var spotRatioVariants = []float64{0.0, 0.3, 0.5, 0.8, 1.0}

// candidateKey is used for deduplication.
type candidateKey struct {
	Replicas      int64
	CPURequest    float64
	MemoryRequest float64
	SpotRatio     float64
}

// GenerateCandidates produces candidate scaling plans as the cartesian product of
// replica variants × resource variants × spot ratio variants.
// It deduplicates, prunes dominated candidates, and caps at maxCandidates.
func GenerateCandidates(input *SolverInput, maxCandidates int) []cel.CandidatePlan {
	if maxCandidates <= 0 {
		maxCandidates = DefaultMaxCandidates
	}

	currentReplicas := input.Current.Replicas
	if currentReplicas < 1 {
		currentReplicas = 1
	}

	// Build resource variants: current + right-sized (if available and different).
	type resourceVariant struct {
		cpu    float64
		memory float64
	}
	resourceVariants := []resourceVariant{
		{cpu: input.Current.CPURequest, memory: input.Current.MemoryRequest},
	}
	if input.RightSizedCPU != nil && input.RightSizedMemory != nil {
		rc := *input.RightSizedCPU
		rm := *input.RightSizedMemory
		if rc != input.Current.CPURequest || rm != input.Current.MemoryRequest {
			resourceVariants = append(resourceVariants, resourceVariant{cpu: rc, memory: rm})
		}
	}

	// Cartesian product.
	seen := make(map[candidateKey]struct{})
	var raw []cel.CandidatePlan

	for _, mult := range replicaMultipliers {
		replicas := int64(math.Round(float64(currentReplicas) * mult))
		if replicas < 1 {
			replicas = 1
		}

		for _, rv := range resourceVariants {
			for _, sr := range spotRatioVariants {
				key := candidateKey{
					Replicas:      replicas,
					CPURequest:    rv.cpu,
					MemoryRequest: rv.memory,
					SpotRatio:     sr,
				}
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}

				spotCount := int64(math.Round(float64(replicas) * sr))
				onDemand := replicas - spotCount

				plan := cel.CandidatePlan{
					Replicas:      replicas,
					CPURequest:    rv.cpu,
					MemoryRequest: rv.memory,
					SpotRatio:     sr,
					SpotCount:     spotCount,
					OnDemandCount: onDemand,
					InstanceTypes: input.InstanceTypes,
				}

				// Estimate cost using the first instance type (or default).
				instanceType := "m5.large"
				if len(input.InstanceTypes) > 0 {
					instanceType = input.InstanceTypes[0]
				}
				plan.EstimatedCost = estimateCost(plan, instanceType, input.Region)
				plan.EstimatedCarbon = estimateCarbon(plan.EstimatedCost, input.Region)

				raw = append(raw, plan)
			}
		}
	}

	// Prune dominated candidates.
	pruned := pruneDominated(raw)

	// Cap at maxCandidates with uniform sampling.
	if len(pruned) > maxCandidates {
		pruned = sampleUniform(pruned, maxCandidates)
	}

	return pruned
}

type instanceCapacity struct {
	CPUCores  float64
	MemoryGiB float64
}

var instanceCapacities = map[string]instanceCapacity{
	"m5.large":   {CPUCores: 2, MemoryGiB: 8},
	"m5.xlarge":  {CPUCores: 4, MemoryGiB: 16},
	"m5.2xlarge": {CPUCores: 8, MemoryGiB: 32},
	"c5.large":   {CPUCores: 2, MemoryGiB: 4},
	"c5.xlarge":  {CPUCores: 4, MemoryGiB: 8},
	"r5.large":   {CPUCores: 2, MemoryGiB: 16},
}

var defaultInstanceCapacity = instanceCapacity{CPUCores: 2, MemoryGiB: 8}

// estimateCost computes hourly cost blending spot and on-demand rates.
// It approximates workload cost as the reserved share of an instance price so
// right-sized candidates become economically distinguishable from the current state.
func estimateCost(plan cel.CandidatePlan, instanceType, region string) float64 {
	resourceShare := planResourceShare(plan, instanceType)
	onDemandRate := cel.CostRateFunc(instanceType, region, false)
	spotRate := cel.CostRateFunc(instanceType, region, true)
	return float64(plan.OnDemandCount)*onDemandRate*resourceShare + float64(plan.SpotCount)*spotRate*resourceShare
}

func planResourceShare(plan cel.CandidatePlan, instanceType string) float64 {
	if plan.CPURequest <= 0 && plan.MemoryRequest <= 0 {
		return 1.0
	}

	capacity, ok := instanceCapacities[instanceType]
	if !ok {
		capacity = defaultInstanceCapacity
	}

	cpuShare := 0.0
	if capacity.CPUCores > 0 && plan.CPURequest > 0 {
		cpuShare = plan.CPURequest / capacity.CPUCores
	}

	memoryShare := 0.0
	if capacity.MemoryGiB > 0 && plan.MemoryRequest > 0 {
		memoryShare = plan.MemoryRequest / capacity.MemoryGiB
	}

	share := math.Max(cpuShare, memoryShare)
	if share <= 0 {
		return 1.0
	}
	return share
}

// estimateCarbon computes gCO2/hr using cost × carbon intensity × PUE factor.
const pueFactor = 1.2

func estimateCarbon(hourlyCost float64, region string) float64 {
	return hourlyCost * cel.CarbonIntensityFunc(region) * pueFactor
}

// pruneDominated removes candidates that are strictly dominated.
// A dominates B if A has <= replicas AND <= cost AND <= spotRatio AND at least one is strictly less.
// (Lower replicas, cost, spot ratio = better in a dominance sense for pruning.)
func pruneDominated(candidates []cel.CandidatePlan) []cel.CandidatePlan {
	n := len(candidates)
	dominated := make([]bool, n)

	for i := 0; i < n; i++ {
		if dominated[i] {
			continue
		}
		for j := 0; j < n; j++ {
			if i == j || dominated[j] {
				continue
			}
			if dominates(candidates[i], candidates[j]) {
				dominated[j] = true
			}
		}
	}

	result := make([]cel.CandidatePlan, 0, n)
	for i, d := range dominated {
		if !d {
			result = append(result, candidates[i])
		}
	}
	return result
}

// dominates returns true if a dominates b, meaning b should be pruned.
// Per the spec: "if candidate A has higher replicas AND higher cost AND worse spot ratio
// than candidate B, remove A." In other words, b is dominated when it uses MORE replicas,
// costs MORE, and takes MORE spot risk than a — strictly worse on every dimension.
func dominates(a, b cel.CandidatePlan) bool {
	return a.Replicas < b.Replicas && a.EstimatedCost < b.EstimatedCost && a.SpotRatio < b.SpotRatio
}

// sampleUniform uniformly samples n candidates from the slice.
func sampleUniform(candidates []cel.CandidatePlan, n int) []cel.CandidatePlan {
	if n >= len(candidates) {
		return candidates
	}

	// Deterministic seed based on candidate count for reproducibility in tests.
	rng := rand.New(rand.NewSource(int64(len(candidates))))
	perm := rng.Perm(len(candidates))

	result := make([]cel.CandidatePlan, n)
	for i := 0; i < n; i++ {
		result[i] = candidates[perm[i]]
	}
	return result
}

// CandidateDebugString returns a human-readable summary for logging.
func CandidateDebugString(p cel.CandidatePlan) string {
	return fmt.Sprintf("replicas=%d cpu=%.3f mem=%.3f spot=%.0f%% cost=$%.4f/hr carbon=%.1fgCO2/hr",
		p.Replicas, p.CPURequest, p.MemoryRequest, p.SpotRatio*100, p.EstimatedCost, p.EstimatedCarbon)
}

// ── Forecast-driven candidate injection ───────────────────────────────────────

const (
	// PreWarmingChangeThreshold is the ChangePercent above which pre-warming
	// candidates are injected (default 15% — proactive before SLO pain).
	PreWarmingChangeThreshold = 15.0

	// SpotRiskThreshold is the SpotRiskScore above which spot-reduction
	// candidates are injected (>0.6 interruption probability).
	SpotRiskThreshold = 0.6
)

// preWarmingMultipliers defines the scaling factors for pre-warming candidates.
var preWarmingMultipliers = []float64{1.3, 1.5}

// spotReductionRatios defines spot ratios to explore when spot risk is high.
var spotReductionRatios = []float64{0.0, 0.3}

// PreWarmingCandidates generates additional higher-replica candidates when the
// forecast predicts a significant demand increase. These candidates are added
// after the standard pruning step to ensure they survive into scoring.
func PreWarmingCandidates(input *SolverInput) []cel.CandidatePlan {
	currentReplicas := input.Current.Replicas
	if currentReplicas < 1 {
		currentReplicas = 1
	}

	instanceType := "m5.large"
	if len(input.InstanceTypes) > 0 {
		instanceType = input.InstanceTypes[0]
	}

	var candidates []cel.CandidatePlan
	for _, mult := range preWarmingMultipliers {
		replicas := int64(math.Round(float64(currentReplicas) * mult))
		if replicas <= currentReplicas {
			continue // skip if rounding didn't increase
		}

		spotCount := int64(math.Round(float64(replicas) * input.Current.SpotRatio))
		onDemand := replicas - spotCount

		plan := cel.CandidatePlan{
			Replicas:      replicas,
			CPURequest:    input.Current.CPURequest,
			MemoryRequest: input.Current.MemoryRequest,
			SpotRatio:     input.Current.SpotRatio,
			SpotCount:     spotCount,
			OnDemandCount: onDemand,
			InstanceTypes: input.InstanceTypes,
		}
		plan.EstimatedCost = estimateCost(plan, instanceType, input.Region)
		plan.EstimatedCarbon = estimateCarbon(plan.EstimatedCost, input.Region)
		candidates = append(candidates, plan)
	}
	return candidates
}

// SpotReductionCandidates generates candidates that reduce the spot ratio when
// the forecast indicates high spot interruption risk (SpotRiskScore > 0.6).
// Only generates candidates with ratios strictly less than the current ratio.
func SpotReductionCandidates(input *SolverInput) []cel.CandidatePlan {
	currentReplicas := input.Current.Replicas
	if currentReplicas < 1 {
		currentReplicas = 1
	}

	instanceType := "m5.large"
	if len(input.InstanceTypes) > 0 {
		instanceType = input.InstanceTypes[0]
	}

	var candidates []cel.CandidatePlan
	for _, sr := range spotReductionRatios {
		if sr >= input.Current.SpotRatio {
			continue // only add candidates that reduce spot exposure
		}

		spotCount := int64(math.Round(float64(currentReplicas) * sr))
		onDemand := currentReplicas - spotCount

		plan := cel.CandidatePlan{
			Replicas:      currentReplicas,
			CPURequest:    input.Current.CPURequest,
			MemoryRequest: input.Current.MemoryRequest,
			SpotRatio:     sr,
			SpotCount:     spotCount,
			OnDemandCount: onDemand,
			InstanceTypes: input.InstanceTypes,
		}
		plan.EstimatedCost = estimateCost(plan, instanceType, input.Region)
		plan.EstimatedCarbon = estimateCarbon(plan.EstimatedCost, input.Region)
		candidates = append(candidates, plan)
	}
	return candidates
}
