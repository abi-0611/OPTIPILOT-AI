package engine

import (
	"math"

	"github.com/optipilot-ai/optipilot/internal/cel"
)

// FindParetoFront returns the set of non-dominated candidates.
// A candidate A dominates B if A is >= B on ALL four objective dimensions
// and strictly > B on at least one.
func FindParetoFront(candidates []ScoredCandidate) []ScoredCandidate {
	if len(candidates) == 0 {
		return nil
	}
	if len(candidates) == 1 {
		return candidates
	}

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
			if scoreDominates(candidates[i].Score, candidates[j].Score) {
				dominated[j] = true
			}
		}
	}

	front := make([]ScoredCandidate, 0)
	for i, d := range dominated {
		if !d {
			front = append(front, candidates[i])
		}
	}
	return front
}

// scoreDominates returns true if score a dominates score b:
// a >= b on all 4 dimensions AND a > b on at least one.
func scoreDominates(a, b CandidateScore) bool {
	if a.SLO < b.SLO || a.Cost < b.Cost || a.Carbon < b.Carbon || a.Fairness < b.Fairness {
		return false
	}
	// All >=, now check at least one strict >
	return a.SLO > b.SLO || a.Cost > b.Cost || a.Carbon > b.Carbon || a.Fairness > b.Fairness
}

// SelectBest picks the candidate from the Pareto front with the highest weighted score.
// If multiple candidates have the same weighted score, it picks the one closest to
// currentState (minimize disruption).
func SelectBest(front []ScoredCandidate, currentState cel.CurrentState) ScoredCandidate {
	if len(front) == 1 {
		return front[0]
	}

	best := front[0]
	bestDist := disruption(best.Plan, currentState)

	for _, c := range front[1:] {
		if c.Score.Weighted > best.Score.Weighted {
			best = c
			bestDist = disruption(c.Plan, currentState)
		} else if c.Score.Weighted == best.Score.Weighted {
			d := disruption(c.Plan, currentState)
			if d < bestDist {
				best = c
				bestDist = d
			}
		}
	}
	return best
}

// disruption measures how different a candidate is from the current state.
// Lower = less disruptive. Uses normalized Euclidean distance across key dimensions.
func disruption(plan cel.CandidatePlan, current cel.CurrentState) float64 {
	replicaDelta := float64(plan.Replicas - current.Replicas)
	cpuDelta := plan.CPURequest - current.CPURequest
	spotDelta := plan.SpotRatio - current.SpotRatio
	return math.Sqrt(replicaDelta*replicaDelta + cpuDelta*cpuDelta + spotDelta*spotDelta)
}
