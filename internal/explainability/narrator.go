package explainability

import (
	"fmt"
	"strings"

	"github.com/optipilot-ai/optipilot/internal/engine"
)

// NarrateDecision generates a human-readable 3–5 sentence explanation of a
// DecisionRecord. Handles: scale_up, scale_down, tune, no_action, dry_run, and
// rollback (detected by trigger containing "rollback").
func NarrateDecision(r engine.DecisionRecord) string {
	if isRollback(r) {
		return narrateRollback(r)
	}
	if r.DryRun {
		return narrateDryRun(r)
	}
	switch r.ActionType {
	case engine.ActionNoAction:
		return narrateNoAction(r)
	case engine.ActionScaleUp:
		return narrateScaleUp(r)
	case engine.ActionScaleDown:
		return narrateScaleDown(r)
	case engine.ActionTune:
		return narrateTune(r)
	default:
		return narrateGeneric(r)
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func isRollback(r engine.DecisionRecord) bool {
	return strings.Contains(strings.ToLower(r.Trigger), "rollback")
}

func timeStr(r engine.DecisionRecord) string {
	return r.Timestamp.UTC().Format("15:04 UTC")
}

func sloSentence(r engine.DecisionRecord) string {
	slo := r.SLOStatus
	if slo.BurnRate > 0 {
		return fmt.Sprintf("SLO burn rate was %.1fx with %.0f%% error budget remaining.",
			slo.BurnRate, slo.BudgetRemaining*100)
	}
	if slo.Compliant {
		return "All SLOs were compliant."
	}
	return "SLO compliance was at risk."
}

func forecastSentence(r engine.DecisionRecord) string {
	if r.ForecastState == nil || r.ForecastState.ChangePercent == 0 {
		return ""
	}
	dir := "increase"
	pct := r.ForecastState.ChangePercent
	if pct < 0 {
		dir = "decrease"
		pct = -pct
	}
	return fmt.Sprintf("Forecast predicted a %.0f%% traffic %s.", pct, dir)
}

func candidateSentence(r engine.DecisionRecord) string {
	total := len(r.Candidates)
	if total == 0 {
		return ""
	}
	viable := 0
	for _, c := range r.Candidates {
		if c.Viable {
			viable++
		}
	}
	filtered := total - viable
	if filtered > 0 {
		return fmt.Sprintf("%d candidate plans were evaluated; %d were filtered by policy constraints.",
			total, filtered)
	}
	return fmt.Sprintf("%d candidate plans were evaluated.", total)
}

func scoreSentence(r engine.DecisionRecord) string {
	a := r.SelectedAction
	parts := []string{}
	// Find the selected candidate's scores.
	for _, c := range r.Candidates {
		if c.Plan.Replicas == int64(a.TargetReplica) && c.Viable {
			parts = append(parts,
				fmt.Sprintf("SLO %.2f", c.Score.SLO),
				fmt.Sprintf("Cost %.2f", c.Score.Cost),
				fmt.Sprintf("Carbon %.2f", c.Score.Carbon),
			)
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("Trade-offs: %s.", strings.Join(parts, ", "))
}

func confidenceSentence(r engine.DecisionRecord) string {
	return fmt.Sprintf("Confidence: %.0f%%.", r.Confidence*100)
}

func selectedPlanSentence(r engine.DecisionRecord) string {
	a := r.SelectedAction
	parts := []string{fmt.Sprintf("%d replicas", a.TargetReplica)}
	if a.CPURequest > 0 {
		parts = append(parts, fmt.Sprintf("%.2f CPU cores", a.CPURequest))
	}
	// Find estimated cost from candidate if available.
	for _, c := range r.Candidates {
		if c.Plan.Replicas == int64(a.TargetReplica) && c.Viable {
			if c.Plan.EstimatedCost > 0 {
				parts = append(parts, fmt.Sprintf("$%.2f/hr", c.Plan.EstimatedCost))
			}
			if c.Plan.SpotCount > 0 || c.Plan.OnDemandCount > 0 {
				parts = append(parts, fmt.Sprintf("%d spot, %d on-demand", c.Plan.SpotCount, c.Plan.OnDemandCount))
			}
			break
		}
	}
	return fmt.Sprintf("Selected plan: %s.", strings.Join(parts, ", "))
}

func reasonSentence(r engine.DecisionRecord) string {
	if r.SelectedAction.Reason != "" {
		return fmt.Sprintf("Reason: %s.", r.SelectedAction.Reason)
	}
	return ""
}

func joinSentences(parts ...string) string {
	var nonEmpty []string
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return strings.Join(nonEmpty, " ")
}

// ── Narrators by type ────────────────────────────────────────────────────────

func narrateScaleUp(r engine.DecisionRecord) string {
	return joinSentences(
		fmt.Sprintf("At %s, %s was scaled up from %d to %d replicas.",
			timeStr(r), r.Service,
			r.CurrentState.Replicas, r.SelectedAction.TargetReplica),
		reasonSentence(r),
		sloSentence(r),
		forecastSentence(r),
		candidateSentence(r),
		selectedPlanSentence(r),
		scoreSentence(r),
		confidenceSentence(r),
	)
}

func narrateScaleDown(r engine.DecisionRecord) string {
	return joinSentences(
		fmt.Sprintf("At %s, %s was scaled down from %d to %d replicas.",
			timeStr(r), r.Service,
			r.CurrentState.Replicas, r.SelectedAction.TargetReplica),
		reasonSentence(r),
		sloSentence(r),
		forecastSentence(r),
		candidateSentence(r),
		selectedPlanSentence(r),
		confidenceSentence(r),
	)
}

func narrateNoAction(r engine.DecisionRecord) string {
	return joinSentences(
		fmt.Sprintf("At %s, no scaling action was taken for %s.", timeStr(r), r.Service),
		reasonSentence(r),
		sloSentence(r),
		forecastSentence(r),
		confidenceSentence(r),
	)
}

func narrateTune(r engine.DecisionRecord) string {
	paramCount := len(r.SelectedAction.TuningParams)
	paramStr := ""
	if paramCount > 0 {
		keys := make([]string, 0, paramCount)
		for k := range r.SelectedAction.TuningParams {
			keys = append(keys, k)
		}
		paramStr = fmt.Sprintf("%d tuning parameter(s) adjusted: %s.", paramCount, strings.Join(keys, ", "))
	}
	return joinSentences(
		fmt.Sprintf("At %s, %s was tuned without replica changes.", timeStr(r), r.Service),
		reasonSentence(r),
		paramStr,
		sloSentence(r),
		confidenceSentence(r),
	)
}

func narrateDryRun(r engine.DecisionRecord) string {
	actionDesc := "would have taken no action"
	switch r.ActionType {
	case engine.ActionScaleUp:
		actionDesc = fmt.Sprintf("would have scaled up from %d to %d replicas",
			r.CurrentState.Replicas, r.SelectedAction.TargetReplica)
	case engine.ActionScaleDown:
		actionDesc = fmt.Sprintf("would have scaled down from %d to %d replicas",
			r.CurrentState.Replicas, r.SelectedAction.TargetReplica)
	case engine.ActionTune:
		actionDesc = "would have applied tuning changes"
	}
	return joinSentences(
		fmt.Sprintf("[DRY RUN] At %s, %s %s.", timeStr(r), r.Service, actionDesc),
		reasonSentence(r),
		sloSentence(r),
		candidateSentence(r),
		fmt.Sprintf("No changes were applied because dry-run mode was active."),
		confidenceSentence(r),
	)
}

func narrateRollback(r engine.DecisionRecord) string {
	return joinSentences(
		fmt.Sprintf("At %s, a rollback was triggered for %s.", timeStr(r), r.Service),
		reasonSentence(r),
		fmt.Sprintf("The service was returned to %d replicas with %.2f CPU cores.",
			r.SelectedAction.TargetReplica, r.SelectedAction.CPURequest),
		sloSentence(r),
		confidenceSentence(r),
	)
}

func narrateGeneric(r engine.DecisionRecord) string {
	return joinSentences(
		fmt.Sprintf("At %s, an optimization decision was made for %s (action: %s).",
			timeStr(r), r.Service, string(r.ActionType)),
		reasonSentence(r),
		sloSentence(r),
		confidenceSentence(r),
	)
}
