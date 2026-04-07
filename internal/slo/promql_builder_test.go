package slo_test

import (
	"testing"

	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/slo"
)

func TestPromQLBuilder_BuildQuery_LatencyP99(t *testing.T) {
	b := &slo.PromQLBuilder{MetricPrefix: "http_request_duration_seconds", Labels: `namespace="ecommerce"`}
	q, err := b.BuildQuery(slov1alpha1.MetricLatencyP99, "5m", "")
	if err != nil {
		t.Fatal(err)
	}
	expected := `histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket{namespace="ecommerce"}[5m])) by (le))`
	if q != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, q)
	}
}

func TestPromQLBuilder_BuildQuery_LatencyP95(t *testing.T) {
	b := &slo.PromQLBuilder{MetricPrefix: "grpc_server_handling_seconds", Labels: `job="api"`}
	q, err := b.BuildQuery(slov1alpha1.MetricLatencyP95, "10m", "")
	if err != nil {
		t.Fatal(err)
	}
	expected := `histogram_quantile(0.95, sum(rate(grpc_server_handling_seconds_bucket{job="api"}[10m])) by (le))`
	if q != expected {
		t.Errorf("unexpected query: %s", q)
	}
}

func TestPromQLBuilder_BuildQuery_LatencyP90(t *testing.T) {
	b := &slo.PromQLBuilder{MetricPrefix: "http_request_duration_seconds", Labels: `service="checkout"`}
	q, err := b.BuildQuery(slov1alpha1.MetricLatencyP90, "1m", "")
	if err != nil {
		t.Fatal(err)
	}
	expected := `histogram_quantile(0.90, sum(rate(http_request_duration_seconds_bucket{service="checkout"}[1m])) by (le))`
	if q != expected {
		t.Errorf("unexpected query: %s", q)
	}
}

func TestPromQLBuilder_BuildQuery_LatencyP50(t *testing.T) {
	b := &slo.PromQLBuilder{MetricPrefix: "http_request_duration_seconds", Labels: `service="checkout"`}
	q, err := b.BuildQuery(slov1alpha1.MetricLatencyP50, "5m", "")
	if err != nil {
		t.Fatal(err)
	}
	expected := `histogram_quantile(0.50, sum(rate(http_request_duration_seconds_bucket{service="checkout"}[5m])) by (le))`
	if q != expected {
		t.Errorf("unexpected query: %s", q)
	}
}

func TestPromQLBuilder_BuildQuery_ErrorRate(t *testing.T) {
	b := &slo.PromQLBuilder{MetricPrefix: "myapp", Labels: `namespace="prod"`}
	q, err := b.BuildQuery(slov1alpha1.MetricErrorRate, "5m", "")
	if err != nil {
		t.Fatal(err)
	}
	expected := `sum(rate(myapp_errors_total{namespace="prod"}[5m])) / sum(rate(myapp_requests_total{namespace="prod"}[5m]))`
	if q != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, q)
	}
}

func TestPromQLBuilder_BuildQuery_Availability(t *testing.T) {
	b := &slo.PromQLBuilder{MetricPrefix: "myapp", Labels: `namespace="prod"`}
	q, err := b.BuildQuery(slov1alpha1.MetricAvailability, "5m", "")
	if err != nil {
		t.Fatal(err)
	}
	expected := `1 - (sum(rate(myapp_errors_total{namespace="prod"}[5m])) / sum(rate(myapp_requests_total{namespace="prod"}[5m])))`
	if q != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, q)
	}
}

func TestPromQLBuilder_BuildQuery_OverrideExistingMetric(t *testing.T) {
	b := &slo.PromQLBuilder{MetricPrefix: "ignored", Labels: `namespace="prod"`}
	customQ := `histogram_quantile(0.99, sum(rate(http_request_duration_highr_seconds_bucket{namespace="test",service="api"}[5m])) by (le))`
	q, err := b.BuildQuery(slov1alpha1.MetricLatencyP99, "5m", customQ)
	if err != nil {
		t.Fatal(err)
	}
	if q != customQ {
		t.Errorf("expected custom query override, got: %s", q)
	}
}

func TestPromQLBuilder_BuildQuery_Throughput(t *testing.T) {
	b := &slo.PromQLBuilder{MetricPrefix: "myapp", Labels: `namespace="prod"`}
	q, err := b.BuildQuery(slov1alpha1.MetricThroughput, "5m", "")
	if err != nil {
		t.Fatal(err)
	}
	expected := `sum(rate(myapp_requests_total{namespace="prod"}[5m]))`
	if q != expected {
		t.Errorf("unexpected query: %s", q)
	}
}

func TestPromQLBuilder_BuildQuery_Custom(t *testing.T) {
	b := &slo.PromQLBuilder{MetricPrefix: "ignored", Labels: "ignored"}
	customQ := `sum(rate(http_errors_total{app="checkout"}[5m]))`
	q, err := b.BuildQuery(slov1alpha1.MetricCustom, "5m", customQ)
	if err != nil {
		t.Fatal(err)
	}
	if q != customQ {
		t.Errorf("expected custom query passthrough, got: %s", q)
	}
}

func TestPromQLBuilder_BuildQuery_Custom_NoQuery(t *testing.T) {
	b := &slo.PromQLBuilder{MetricPrefix: "myapp", Labels: `ns="x"`}
	_, err := b.BuildQuery(slov1alpha1.MetricCustom, "5m", "")
	if err == nil {
		t.Error("expected error for custom metric with empty customQuery")
	}
}

func TestNewPromQLBuilderFromAnnotations_WithAnnotations(t *testing.T) {
	annotations := map[string]string{
		"optipilot.ai/metrics-prefix": "custom_prefix",
		"optipilot.ai/metrics-labels": `job="my-job"`,
	}
	b := slo.NewPromQLBuilderFromAnnotations(annotations, "checkout", "ecommerce")
	if b.MetricPrefix != "custom_prefix" {
		t.Errorf("expected custom_prefix, got %s", b.MetricPrefix)
	}
	if b.Labels != `job="my-job"` {
		t.Errorf("expected job label, got %s", b.Labels)
	}
}

func TestNewPromQLBuilderFromAnnotations_Defaults(t *testing.T) {
	b := slo.NewPromQLBuilderFromAnnotations(nil, "checkout-service", "ecommerce")
	if b.MetricPrefix != "checkout_service" {
		t.Errorf("expected checkout_service (hyphen→underscore), got %s", b.MetricPrefix)
	}
	if b.Labels != `namespace="ecommerce"` {
		t.Errorf("expected namespace label fallback, got %s", b.Labels)
	}
}
