package explainability

import (
	"strings"
	"testing"
	"time"

	"github.com/optipilot-ai/optipilot/internal/cel"
	"github.com/optipilot-ai/optipilot/internal/engine"
)

func makeNarratorRecord(actionType engine.ActionType, trigger string, dryRun bool, fromReplicas int64, toReplicas int32) engine.DecisionRecord {
	return engine.DecisionRecord{
		ID:        "narr-001",
		Timestamp: time.Date(2026, 4, 5, 10, 30, 0, 0, time.UTC),
		Namespace: "production",
		Service:   "checkout-service",
		Trigger:   trigger,
		CurrentState: cel.CurrentState{
			Replicas:   fromReplicas,
			CPURequest: 0.5,
			CPUUsage:   0.4,
			HourlyCost: 8.50,
		},
		SLOStatus: cel.SLOStatus{
			Compliant:       false,
			BurnRate:        14.2,
			BudgetRemaining: 0.06,
		},
		Candidates: []engine.ScoredCandidate{
			{
				Plan: cel.CandidatePlan{
					Replicas:      int64(toReplicas),
					CPURequest:    0.5,
					SpotRatio:     0.625,
					SpotCount:     5,
					OnDemandCount: 3,
					EstimatedCost: 12.40,
				},
				Score: engine.CandidateScore{
					SLO:      0.95,
					Cost:     0.72,
					Carbon:   0.88,
					Fairness: 1.0,
					Weighted: 0.87,
				},
				Viable: true,
			},
			{
				Plan: cel.CandidatePlan{Replicas: 10, CPURequest: 0.5},
				Score: engine.CandidateScore{
					SLO: 0.99, Cost: 0.40, Carbon: 0.60, Weighted: 0.65,
				},
				Constraints: []engine.ConstraintResult{
					{Expr: "plan.spot_ratio <= 0.8", Reason: "max 80% spot", Hard: true, Passed: false},
				},
				Viable: false,
			},
		},
		SelectedAction: engine.ScalingAction{
			Type:          actionType,
			TargetReplica: toReplicas,
			CPURequest:    0.5,
			SpotRatio:     0.625,
			DryRun:        dryRun,
			Reason:        "SLO burn rate for latency_p99 reached 14.2x",
			Confidence:    0.87,
		},
		PolicyNames:      []string{"cost-optimizer", "slo-guardian"},
		ObjectiveWeights: map[string]float64{"slo": 0.4, "cost": 0.3, "carbon": 0.2, "fairness": 0.1},
		ActionType:       actionType,
		DryRun:           dryRun,
		Confidence:       0.87,
	}
}

// ── Test 1: Scale-up narrative ──────────────────────────────────────────────

func TestNarrator_ScaleUp(t *testing.T) {
	r := makeNarratorRecord(engine.ActionScaleUp, "slo-breach", false, 5, 8)
	r.ForecastState = &cel.ForecastResult{ChangePercent: 40, Confidence: 0.85}

	text := NarrateDecision(r)

	mustContain := []string{
		"10:30 UTC",
		"checkout-service",
		"scaled up",
		"5 to 8",
		"SLO burn rate",
		"14.2x",
		"40%",
		"traffic increase",
		"2 candidate plans",
		"1 were filtered",
		"Selected plan",
		"8 replicas",
		"$12.40/hr",
		"5 spot, 3 on-demand",
		"SLO 0.95",
		"Cost 0.72",
		"Carbon 0.88",
		"Confidence: 87%",
	}
	for _, want := range mustContain {
		if !strings.Contains(text, want) {
			t.Errorf("scale-up narrative missing %q\nGot: %s", want, text)
		}
	}
}

// ── Test 2: Scale-down narrative ─────────────────────────────────────────────

func TestNarrator_ScaleDown(t *testing.T) {
	r := makeNarratorRecord(engine.ActionScaleDown, "periodic", false, 10, 6)
	r.SLOStatus = cel.SLOStatus{Compliant: true, BudgetRemaining: 0.95}
	r.SelectedAction.Reason = "low utilization, excess capacity"
	r.Candidates = r.Candidates[:1] // 1 viable only
	r.Candidates[0].Plan.Replicas = 6

	text := NarrateDecision(r)

	mustContain := []string{
		"scaled down",
		"10 to 6",
		"low utilization",
		"All SLOs were compliant",
		"Confidence: 87%",
	}
	for _, want := range mustContain {
		if !strings.Contains(text, want) {
			t.Errorf("scale-down narrative missing %q\nGot: %s", want, text)
		}
	}
}

// ── Test 3: No-action narrative ──────────────────────────────────────────────

func TestNarrator_NoAction(t *testing.T) {
	r := makeNarratorRecord(engine.ActionNoAction, "periodic", false, 4, 4)
	r.SLOStatus = cel.SLOStatus{Compliant: true, BudgetRemaining: 0.90}
	r.SelectedAction.Reason = "system is stable, no scaling needed"
	r.Candidates = nil

	text := NarrateDecision(r)

	mustContain := []string{
		"no scaling action",
		"checkout-service",
		"stable",
		"All SLOs were compliant",
		"Confidence: 87%",
	}
	for _, want := range mustContain {
		if !strings.Contains(text, want) {
			t.Errorf("no-action narrative missing %q\nGot: %s", want, text)
		}
	}

	// Must NOT contain scale-up language.
	mustNotContain := []string{"scaled up", "scaled down", "replicas"}
	for _, bad := range mustNotContain {
		if strings.Contains(text, bad) {
			t.Errorf("no-action narrative should not contain %q\nGot: %s", bad, text)
		}
	}
}

// ── Test 4: Dry-run narrative ────────────────────────────────────────────────

func TestNarrator_DryRun(t *testing.T) {
	r := makeNarratorRecord(engine.ActionScaleUp, "slo-breach", true, 5, 8)

	text := NarrateDecision(r)

	mustContain := []string{
		"[DRY RUN]",
		"would have scaled up",
		"5 to 8",
		"dry-run mode was active",
		"Confidence: 87%",
	}
	for _, want := range mustContain {
		if !strings.Contains(text, want) {
			t.Errorf("dry-run narrative missing %q\nGot: %s", want, text)
		}
	}
}

// ── Test 5: Rollback narrative ───────────────────────────────────────────────

func TestNarrator_Rollback(t *testing.T) {
	r := makeNarratorRecord(engine.ActionScaleDown, "canary-rollback", false, 8, 5)
	r.SelectedAction.Reason = "canary detected elevated error rate after scale-up"

	text := NarrateDecision(r)

	mustContain := []string{
		"rollback was triggered",
		"checkout-service",
		"returned to 5 replicas",
		"0.50 CPU",
		"canary detected",
		"Confidence: 87%",
	}
	for _, want := range mustContain {
		if !strings.Contains(text, want) {
			t.Errorf("rollback narrative missing %q\nGot: %s", want, text)
		}
	}

	// Rollback should NOT say "scaled down".
	if strings.Contains(text, "scaled down") {
		t.Errorf("rollback should use rollback language, not 'scaled down'\nGot: %s", text)
	}
}

// ── Test 6: Tune narrative ───────────────────────────────────────────────────

func TestNarrator_Tune(t *testing.T) {
	r := makeNarratorRecord(engine.ActionTune, "periodic", false, 4, 4)
	r.SelectedAction.TuningParams = map[string]string{
		"GOMAXPROCS":          "8",
		"connection_pool_max": "200",
	}
	r.SelectedAction.Reason = "resource tuning for better efficiency"
	r.SLOStatus = cel.SLOStatus{Compliant: true, BudgetRemaining: 0.85}

	text := NarrateDecision(r)

	mustContain := []string{
		"tuned without replica changes",
		"checkout-service",
		"2 tuning parameter(s)",
		"resource tuning",
		"All SLOs were compliant",
		"Confidence: 87%",
	}
	for _, want := range mustContain {
		if !strings.Contains(text, want) {
			t.Errorf("tune narrative missing %q\nGot: %s", want, text)
		}
	}
}

// ── Test 7: Forecast decrease direction ──────────────────────────────────────

func TestNarrator_ForecastDecrease(t *testing.T) {
	r := makeNarratorRecord(engine.ActionScaleDown, "periodic", false, 10, 6)
	r.ForecastState = &cel.ForecastResult{ChangePercent: -30, Confidence: 0.75}
	r.SLOStatus = cel.SLOStatus{Compliant: true, BudgetRemaining: 0.90}
	r.Candidates = r.Candidates[:1]
	r.Candidates[0].Plan.Replicas = 6

	text := NarrateDecision(r)

	if !strings.Contains(text, "30% traffic decrease") {
		t.Errorf("expected '30%% traffic decrease', got: %s", text)
	}
}

// ── Test 8: No burn rate, SLO at risk ────────────────────────────────────────

func TestNarrator_SLOAtRisk(t *testing.T) {
	r := makeNarratorRecord(engine.ActionScaleUp, "slo-breach", false, 4, 6)
	r.SLOStatus = cel.SLOStatus{Compliant: false, BurnRate: 0, BudgetRemaining: 0.02}

	text := NarrateDecision(r)

	if !strings.Contains(text, "SLO compliance was at risk") {
		t.Errorf("expected 'SLO compliance was at risk'\nGot: %s", text)
	}
}

// ── Test 9: No candidates ────────────────────────────────────────────────────

func TestNarrator_NoCandidates(t *testing.T) {
	r := makeNarratorRecord(engine.ActionNoAction, "periodic", false, 4, 4)
	r.Candidates = nil
	r.SLOStatus = cel.SLOStatus{Compliant: true, BudgetRemaining: 0.95}

	text := NarrateDecision(r)

	if strings.Contains(text, "candidate plans") {
		t.Errorf("no-candidate narrative should not mention candidate plans\nGot: %s", text)
	}
}

// ── Test 10: Dry-run no-action ───────────────────────────────────────────────

func TestNarrator_DryRunNoAction(t *testing.T) {
	r := makeNarratorRecord(engine.ActionNoAction, "periodic", true, 4, 4)
	r.SLOStatus = cel.SLOStatus{Compliant: true, BudgetRemaining: 0.95}

	text := NarrateDecision(r)

	if !strings.Contains(text, "[DRY RUN]") {
		t.Errorf("missing [DRY RUN] prefix\nGot: %s", text)
	}
	if !strings.Contains(text, "would have taken no action") {
		t.Errorf("expected dry-run no-action language\nGot: %s", text)
	}
}
