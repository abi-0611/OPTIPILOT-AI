package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/engine"
	"github.com/optipilot-ai/optipilot/internal/forecaster"
	"github.com/optipilot-ai/optipilot/internal/metrics"
)

// fakeRangeProm implements metrics.PrometheusClient for tests — returns fixed range series.
type fakeRangeProm struct {
	points []metrics.DataPoint
	err    error
}

func (f *fakeRangeProm) Query(context.Context, string) (float64, error) {
	return 0, nil
}

func (f *fakeRangeProm) QueryRange(context.Context, string, time.Time, time.Time, time.Duration) ([]metrics.DataPoint, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.points, nil
}

func (f *fakeRangeProm) Healthy(context.Context) error { return nil }

func TestDeploymentCPURateQuery_EscapesPodRegex(t *testing.T) {
	q := deploymentCPURateQuery("ns", "my.app")
	if q == "" {
		t.Fatal("empty query")
	}
	// Deployment name with dot must be regex-escaped in pod=~ pattern
	if q != `sum(rate(container_cpu_usage_seconds_total{namespace="ns",pod=~"my\.app-.*",container!="",container!="POD"}[5m]))` {
		t.Fatalf("unexpected query: %s", q)
	}
}

func TestHeuristicForecastFromHistory_RisingTrend(t *testing.T) {
	// First half low, second half high → positive change %
	h := []forecaster.MetricPoint{
		{Y: 1}, {Y: 1}, {Y: 1}, {Y: 1},
		{Y: 2}, {Y: 2}, {Y: 2}, {Y: 2},
	}
	fc := heuristicForecastFromHistory(h, 4)
	if fc == nil {
		t.Fatal("expected forecast")
	}
	if fc.ChangePercent < 50 {
		t.Fatalf("expected large positive change, got %f", fc.ChangePercent)
	}
}

func TestHeuristicForecastFromHistory_TooShort(t *testing.T) {
	h := []forecaster.MetricPoint{{Y: 1}, {Y: 2}, {Y: 3}}
	if fc := heuristicForecastFromHistory(h, 4); fc != nil {
		t.Fatalf("expected nil, got %+v", fc)
	}
}

// TestAttachForecast_HeuristicExceedsPreWarmingThreshold verifies the full attachForecast path:
// Prometheus range → heuristic forecast → ChangePercent above solver PreWarmingChangeThreshold (15%).
func TestAttachForecast_HeuristicExceedsPreWarmingThreshold(t *testing.T) {
	ctx := context.Background()
	base := time.Now().UTC().Add(-2 * time.Hour)
	var pts []metrics.DataPoint
	for i := 0; i < 8; i++ {
		v := 0.1
		if i >= 4 {
			v = 0.5
		}
		pts = append(pts, metrics.DataPoint{
			Timestamp: base.Add(time.Duration(i) * 5 * time.Minute),
			Value:     v,
		})
	}

	o := &OptimizerController{PromClient: &fakeRangeProm{points: pts}}
	so := &slov1alpha1.ServiceObjective{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: slov1alpha1.ServiceObjectiveSpec{
			TargetRef: slov1alpha1.CrossVersionObjectReference{Name: "mydep", Kind: "Deployment"},
		},
	}
	input := &engine.SolverInput{Namespace: "default"}
	o.attachForecast(ctx, input, so)

	if input.Forecast == nil {
		t.Fatal("expected forecast (heuristic)")
	}
	if input.Forecast.ChangePercent <= 15 {
		t.Fatalf("expected change > 15%% for pre-warming candidates, got %v", input.Forecast.ChangePercent)
	}
}
