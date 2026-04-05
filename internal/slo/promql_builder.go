package slo

import (
	"fmt"

	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
)

// Annotation keys for metric discovery on target workloads.
const (
	AnnotationMetricsPrefix = "optipilot.ai/metrics-prefix"
	AnnotationMetricsLabels = "optipilot.ai/metrics-labels"
)

// queryTemplates maps each MetricType to its PromQL template.
// Placeholders: %s = metric-prefix, %s = labels, %s = window
// For multi-metric templates additional placeholders repeat in the same order.
var queryTemplates = map[slov1alpha1.MetricType]string{
	slov1alpha1.MetricLatencyP99: `histogram_quantile(0.99, sum(rate(%s_bucket{%s}[%s])) by (le))`,
	slov1alpha1.MetricLatencyP95: `histogram_quantile(0.95, sum(rate(%s_bucket{%s}[%s])) by (le))`,
	slov1alpha1.MetricLatencyP90: `histogram_quantile(0.90, sum(rate(%s_bucket{%s}[%s])) by (le))`,
	slov1alpha1.MetricLatencyP50: `histogram_quantile(0.50, sum(rate(%s_bucket{%s}[%s])) by (le))`,
	// Error- and availability-rate templates use 6 substitutions: prefix,labels,window x2
	slov1alpha1.MetricErrorRate:    `sum(rate(%s_errors_total{%s}[%s])) / sum(rate(%s_requests_total{%s}[%s]))`,
	slov1alpha1.MetricAvailability: `1 - (sum(rate(%s_errors_total{%s}[%s])) / sum(rate(%s_requests_total{%s}[%s])))`,
	slov1alpha1.MetricThroughput:   `sum(rate(%s_requests_total{%s}[%s]))`,
}

// PromQLBuilder constructs PromQL queries for a specific workload.
type PromQLBuilder struct {
	// MetricPrefix is the base metric name (e.g., "http_request_duration_seconds").
	MetricPrefix string
	// Labels are the label selectors (e.g., `namespace="ecommerce",service="checkout"`).
	Labels string
}

// NewPromQLBuilderFromAnnotations creates a builder by inspecting workload annotations.
// deploymentName and namespace are used as fallback when no annotations are present.
func NewPromQLBuilderFromAnnotations(annotations map[string]string, deploymentName, namespace string) *PromQLBuilder {
	prefix, ok := annotations[AnnotationMetricsPrefix]
	if !ok || prefix == "" {
		// Convention: use deployment name with underscores replacing hyphens.
		prefix = sanitizeName(deploymentName)
	}

	labels := annotations[AnnotationMetricsLabels]
	if labels == "" {
		labels = fmt.Sprintf(`namespace="%s"`, namespace)
	}

	return &PromQLBuilder{
		MetricPrefix: prefix,
		Labels:       labels,
	}
}

// BuildQuery generates a PromQL expression for the given metric type and window.
// For custom metrics pass an empty window — the query is returned as-is from customQuery.
func (b *PromQLBuilder) BuildQuery(metric slov1alpha1.MetricType, window string, customQuery string) (string, error) {
	if metric == slov1alpha1.MetricCustom {
		if customQuery == "" {
			return "", fmt.Errorf("custom metric requires a non-empty customQuery")
		}
		return customQuery, nil
	}

	tmpl, ok := queryTemplates[metric]
	if !ok {
		return "", fmt.Errorf("no PromQL template for metric type %q", metric)
	}

	p := b.MetricPrefix
	l := b.Labels
	w := window

	switch metric {
	case slov1alpha1.MetricErrorRate, slov1alpha1.MetricAvailability:
		return fmt.Sprintf(tmpl, p, l, w, p, l, w), nil
	default:
		return fmt.Sprintf(tmpl, p, l, w), nil
	}
}

// sanitizeName replaces hyphens with underscores for metric naming conventions.
func sanitizeName(s string) string {
	result := make([]byte, len(s))
	for i := range s {
		if s[i] == '-' {
			result[i] = '_'
		} else {
			result[i] = s[i]
		}
	}
	return string(result)
}
