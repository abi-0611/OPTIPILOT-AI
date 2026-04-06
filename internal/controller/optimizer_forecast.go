package controller

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"time"

	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/cel"
	"github.com/optipilot-ai/optipilot/internal/engine"
	"github.com/optipilot-ai/optipilot/internal/forecaster"
	prommetrics "github.com/optipilot-ai/optipilot/internal/metrics"
	ctrl "sigs.k8s.io/controller-runtime"
)

const forecastMetricLabel = "cpu_cores_rate"

// Defaults for proactive, near–real-time scaling (tunable via flags / Helm).
const (
	defaultForecastLookback  = 90 * time.Minute
	defaultForecastStep      = time.Minute
	defaultForecastMinPoints = 6
)

func (o *OptimizerController) effectiveForecastLookback() time.Duration {
	if o.ForecastLookback > 0 {
		return o.ForecastLookback
	}
	return defaultForecastLookback
}

func (o *OptimizerController) effectiveForecastStep() time.Duration {
	if o.ForecastStep > 0 {
		return o.ForecastStep
	}
	return defaultForecastStep
}

func (o *OptimizerController) effectiveForecastMinPoints() int {
	if o.ForecastMinPoints > 0 {
		return o.ForecastMinPoints
	}
	return defaultForecastMinPoints
}

// attachForecast fills input.Forecast using the ML service when configured, otherwise
// a Prometheus-derived trend heuristic so pre-warming still works without the ML pod.
func (o *OptimizerController) attachForecast(ctx context.Context, input *engine.SolverInput, so *slov1alpha1.ServiceObjective) {
	logger := ctrl.Log.WithName("optimizer").WithValues("namespace", so.Namespace, "service", so.Spec.TargetRef.Name)

	if so.Spec.TargetRef.Kind != "Deployment" {
		prommetrics.ForecastAttachmentOutcomes.WithLabelValues("skipped_not_deployment").Inc()
		return
	}
	if o.PromClient == nil {
		prommetrics.ForecastAttachmentOutcomes.WithLabelValues("skipped_no_prometheus").Inc()
		return
	}

	minPts := o.effectiveForecastMinPoints()

	depName := so.Spec.TargetRef.Name
	history, err := o.fetchDemandHistory(ctx, so.Namespace, depName)
	if err != nil {
		prommetrics.ForecastAttachmentOutcomes.WithLabelValues("skipped_query_error").Inc()
		logger.Info("forecast skipped: prometheus query failed", "error", err.Error())
		return
	}
	if len(history) < minPts {
		prommetrics.ForecastAttachmentOutcomes.WithLabelValues("skipped_insufficient_history").Inc()
		logger.Info("forecast skipped: not enough history yet for proactive signal",
			"have_points", len(history), "need_points", minPts,
			"hint", "wait for prometheus to collect CPU series at forecast-step resolution")
		return
	}

	serviceKey := fmt.Sprintf("%s/%s", so.Namespace, depName)

	var fc *cel.ForecastResult
	if o.Forecaster != nil {
		var mlErr error
		fc, mlErr = o.Forecaster.ForecastDemand(ctx, serviceKey, forecastMetricLabel, history)
		if mlErr != nil {
			logger.V(1).Info("ml forecast failed, using heuristic", "error", mlErr.Error())
			fc = nil
		}
	}
	if fc == nil {
		fc = heuristicForecastFromHistory(history, minPts)
	}
	input.Forecast = fc
	prommetrics.ForecastAttachmentOutcomes.WithLabelValues("applied").Inc()
	logger.V(1).Info("forecast attached for proactive scaling",
		"changePercent", fc.ChangePercent,
		"confidence", fc.Confidence,
		"predictedRPS", fc.PredictedRPS)
}

func deploymentCPURateQuery(namespace, deploymentName string) string {
	return fmt.Sprintf(
		`sum(rate(container_cpu_usage_seconds_total{namespace="%s",pod=~"%s-.*",container!="",container!="POD"}[5m]))`,
		namespace,
		regexp.QuoteMeta(deploymentName),
	)
}

func (o *OptimizerController) fetchDemandHistory(ctx context.Context, namespace, deploymentName string) ([]forecaster.MetricPoint, error) {
	end := time.Now().UTC()
	start := end.Add(-o.effectiveForecastLookback())

	points, err := o.PromClient.QueryRange(ctx, deploymentCPURateQuery(namespace, deploymentName), start, end, o.effectiveForecastStep())
	if err != nil {
		if err == prommetrics.ErrNoData {
			return nil, nil
		}
		return nil, err
	}
	if len(points) == 0 {
		return nil, nil
	}

	sort.Slice(points, func(i, j int) bool {
		return points[i].Timestamp.Before(points[j].Timestamp)
	})

	out := make([]forecaster.MetricPoint, 0, len(points))
	for _, p := range points {
		out = append(out, forecaster.MetricPoint{
			DS: p.Timestamp.UTC().Format(time.RFC3339),
			Y:  p.Value,
		})
	}
	return out, nil
}

func heuristicForecastFromHistory(h []forecaster.MetricPoint, minPoints int) *cel.ForecastResult {
	if minPoints <= 0 {
		minPoints = defaultForecastMinPoints
	}
	if len(h) < minPoints {
		return nil
	}
	mid := len(h) / 2
	var sumOld, sumNew float64
	for i := 0; i < mid; i++ {
		sumOld += h[i].Y
	}
	for i := mid; i < len(h); i++ {
		sumNew += h[i].Y
	}
	nOld := float64(mid)
	nNew := float64(len(h) - mid)
	avgOld := sumOld / nOld
	avgNew := sumNew / nNew

	var chg float64
	switch {
	case avgOld < 1e-9 && avgNew > 1e-9:
		chg = 100
	case avgOld < 1e-9:
		chg = 0
	default:
		chg = (avgNew - avgOld) / avgOld * 100
	}
	if chg > 500 {
		chg = 500
	}
	if chg < -90 {
		chg = -90
	}

	return &cel.ForecastResult{
		PredictedRPS:  avgNew,
		ChangePercent: chg,
		Confidence:    0.45,
	}
}
