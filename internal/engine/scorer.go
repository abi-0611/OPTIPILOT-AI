package engine

import (
	"math"

	policyv1alpha1 "github.com/optipilot-ai/optipilot/api/policy/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/cel"
)

// Scorer computes multi-dimensional scores for candidate plans.
type Scorer struct {
	input      *SolverInput
	objectives []policyv1alpha1.PolicyObjective
}

// NewScorer creates a scorer bound to the given solver input and policy objectives.
func NewScorer(input *SolverInput, objectives []policyv1alpha1.PolicyObjective) *Scorer {
	return &Scorer{input: input, objectives: objectives}
}

// ScoreAll scores a batch of candidates, normalizing cost and carbon across the batch.
// Returns ScoredCandidates with all dimension scores and the weighted aggregate.
func (s *Scorer) ScoreAll(candidates []cel.CandidatePlan) []ScoredCandidate {
	if len(candidates) == 0 {
		return nil
	}

	// First pass: compute raw cost and carbon for normalization bounds.
	costs := make([]float64, len(candidates))
	carbons := make([]float64, len(candidates))
	minCost, maxCost := math.MaxFloat64, 0.0
	minCarbon, maxCarbon := math.MaxFloat64, 0.0

	for i, c := range candidates {
		costs[i] = c.EstimatedCost
		carbons[i] = c.EstimatedCarbon

		if costs[i] < minCost {
			minCost = costs[i]
		}
		if costs[i] > maxCost {
			maxCost = costs[i]
		}
		if carbons[i] < minCarbon {
			minCarbon = carbons[i]
		}
		if carbons[i] > maxCarbon {
			maxCarbon = carbons[i]
		}
	}

	// Second pass: score each candidate.
	scored := make([]ScoredCandidate, len(candidates))
	for i, c := range candidates {
		score := CandidateScore{
			SLO:      s.scoreSLO(c),
			Cost:     scoreCost(costs[i], minCost, maxCost),
			Carbon:   scoreCarbon(carbons[i], minCarbon, maxCarbon),
			Fairness: s.scoreFairness(c),
		}
		score.Weighted = s.computeWeighted(score)

		scored[i] = ScoredCandidate{
			Plan:   c,
			Score:  score,
			Viable: true, // constraints haven't been applied yet
		}
	}

	return scored
}

// scoreSLO predicts SLO compliance based on capacity ratio.
func (s *Scorer) scoreSLO(c cel.CandidatePlan) float64 {
	if !s.input.SLO.Compliant && hasObservedSLOSignal(s.input.SLO) {
		return s.scoreDegradedSLO(c)
	}

	demand := s.currentDemand()
	if demand <= 0 {
		return 1.0 // no demand, any candidate meets SLO
	}

	capacityRatio := float64(c.Replicas) * c.CPURequest / demand

	switch {
	case capacityRatio >= 1.2:
		return 1.0
	case capacityRatio >= 1.0:
		return 0.8
	case capacityRatio >= 0.8:
		return 0.4
	default:
		return 0.0
	}
}

func (s *Scorer) scoreDegradedSLO(c cel.CandidatePlan) float64 {
	currentReplicas := s.input.Current.Replicas
	if currentReplicas < 1 {
		currentReplicas = 1
	}

	severityBonus := 0.0
	switch {
	case s.input.SLO.BurnRate >= 10:
		severityBonus = 0.2
	case s.input.SLO.BurnRate >= 1:
		severityBonus = 0.1
	}

	if c.Replicas < currentReplicas {
		return 0.0
	}

	if c.Replicas == currentReplicas {
		score := 0.2 + severityBonus
		if score > 0.4 {
			return 0.4
		}
		return score
	}

	improvement := float64(c.Replicas-currentReplicas) / float64(currentReplicas)
	score := 0.6 + math.Min(improvement, 1.0)*0.3 + severityBonus
	if score > 1.0 {
		return 1.0
	}
	return score
}

func hasObservedSLOSignal(status cel.SLOStatus) bool {
	return status.BurnRate > 0 ||
		status.BudgetRemaining > 0 ||
		status.LatencyP99 > 0 ||
		status.ErrorRate > 0 ||
		status.Availability > 0 ||
		status.Throughput > 0
}

// currentDemand returns the demand to score against.
// Uses forecast predicted demand if available, otherwise current CPU usage.
func (s *Scorer) currentDemand() float64 {
	if s.input.Forecast != nil && s.input.Forecast.PredictedRPS > 0 {
		// Use forecast: scale current usage by predicted change.
		changeRatio := 1.0 + s.input.Forecast.ChangePercent/100.0
		return s.input.Current.CPUUsage * changeRatio
	}
	return s.input.Current.CPUUsage
}

// scoreCost normalizes cost to [0, 1] where 1 = cheapest.
func scoreCost(cost, minCost, maxCost float64) float64 {
	return 1.0 - normalize(cost, minCost, maxCost)
}

// scoreCarbon normalizes carbon to [0, 1] where 1 = greenest.
func scoreCarbon(carbon, minCarbon, maxCarbon float64) float64 {
	return 1.0 - normalize(carbon, minCarbon, maxCarbon)
}

// scoreFairness scores based on tenant quota adherence.
func (s *Scorer) scoreFairness(c cel.CandidatePlan) float64 {
	if s.input.Tenant == nil || s.input.Tenant.GuaranteedCores <= 0 {
		return 1.0 // no tenant, fairness not applicable
	}

	// Delta in cores from this candidate vs current.
	delta := float64(c.Replicas)*c.CPURequest - float64(s.input.Current.Replicas)*s.input.Current.CPURequest
	projectedUsage := s.input.Tenant.CurrentCores + delta

	switch {
	case projectedUsage <= s.input.Tenant.GuaranteedCores:
		return 1.0
	case projectedUsage <= s.input.Tenant.MaxCores:
		return 0.7
	default:
		return 0.2
	}
}

// computeWeighted applies policy objective weights to dimension scores.
// weighted = Σ (weight_i × score_i × direction_sign)
// For "maximize" objectives, direction_sign = +1 (score already 0–1 where 1=best).
// For "minimize" objectives, direction_sign = +1 too because scores are already
// inverted (e.g., cost_score = 1 - normalized_cost, so higher = cheaper).
func (s *Scorer) computeWeighted(score CandidateScore) float64 {
	if len(s.objectives) == 0 {
		// Default: equal weight to all four.
		return (score.SLO + score.Cost + score.Carbon + score.Fairness) / 4.0
	}

	totalWeight := 0.0
	for _, o := range s.objectives {
		totalWeight += o.Weight
	}
	if totalWeight <= 0 {
		totalWeight = 1.0
	}

	weighted := 0.0
	for _, o := range s.objectives {
		normalizedWeight := o.Weight / totalWeight
		dimScore := dimensionScore(score, o.Name)
		weighted += normalizedWeight * dimScore
	}
	return weighted
}

// dimensionScore maps an objective name to the corresponding score dimension.
func dimensionScore(score CandidateScore, name string) float64 {
	switch name {
	case "slo", "slo_compliance":
		return score.SLO
	case "cost":
		return score.Cost
	case "carbon":
		return score.Carbon
	case "fairness":
		return score.Fairness
	default:
		return 0.0
	}
}

// normalize maps a value to [0, 1] given min and max bounds.
// Returns 0.0 if min == max (all candidates equal on this dimension).
func normalize(value, min, max float64) float64 {
	if max <= min {
		return 0.0
	}
	n := (value - min) / (max - min)
	if n < 0 {
		return 0.0
	}
	if n > 1 {
		return 1.0
	}
	return n
}
