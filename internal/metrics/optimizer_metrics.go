package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

// ForecastAttachmentOutcomes counts how often the forecast step completes per outcome.
// result: applied | skipped_not_deployment | skipped_insufficient_history | skipped_query_error | skipped_no_prometheus
var ForecastAttachmentOutcomes = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "optipilot",
		Subsystem: "optimizer",
		Name:      "forecast_attachment_total",
		Help:      "Forecast pipeline: applied (forecast set) or skipped with reason",
	},
	[]string{"result"},
)

func init() {
	ctrlmetrics.Registry.MustRegister(ForecastAttachmentOutcomes)
}
