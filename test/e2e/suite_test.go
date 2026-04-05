//go:build e2e

// Package e2e_test contains end-to-end tests for the OptiPilot AI system.
//
// These tests require kind, kubectl, and helm to be present in PATH.
// They create a temporary kind cluster named "optipilot-e2e", install the
// system via Helm, and exercise the full control plane against a real
// Kubernetes API server.
//
// Run with:
//
//	go test -tags e2e ./test/e2e/... -timeout 20m -v
//
// To reuse an existing cluster across multiple runs the cluster is NOT
// deleted on teardown. Remove it manually when done:
//
//	kind delete cluster --name optipilot-e2e
package e2e_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"

	kruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	policyv1alpha1 "github.com/optipilot-ai/optipilot/api/policy/v1alpha1"
	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
	tenantv1alpha1 "github.com/optipilot-ai/optipilot/api/tenant/v1alpha1"
	tuningv1alpha1 "github.com/optipilot-ai/optipilot/api/tuning/v1alpha1"
)

// ── Global constants ─────────────────────────────────────────────────────────

const (
	e2eClusterName = "optipilot-e2e"
	helmRelease    = "optipilot"
	helmNamespace  = "optipilot-system"
	testNamespace  = "optipilot-e2e"

	// Local ports used by kubectl port-forward during tests.
	localAPIPort     = 19090
	localMetricsPort = 19080
)

// ── Package-level state ──────────────────────────────────────────────────────

var (
	testScheme     = kruntime.NewScheme()
	testClient     client.Client
	kubeconfigPath string

	// setupCompleted is set to true only when TestMain finishes suite setup
	// successfully. Individual tests call requireSetup(t) which calls t.Skip
	// when false.
	setupCompleted bool
)

// ── Scheme registration (runs before TestMain) ────────────────────────────────

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(testScheme))
	utilruntime.Must(slov1alpha1.AddToScheme(testScheme))
	utilruntime.Must(policyv1alpha1.AddToScheme(testScheme))
	utilruntime.Must(tenantv1alpha1.AddToScheme(testScheme))
	utilruntime.Must(tuningv1alpha1.AddToScheme(testScheme))
}

// ── TestMain ─────────────────────────────────────────────────────────────────

// TestMain wires up the test cluster before any test function runs and tears
// down disposable resources afterwards.
func TestMain(m *testing.M) {
	if err := checkPrerequisites(); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: E2E prerequisites not met: %v\n", err)
		fmt.Fprintln(os.Stderr, "      Install kind, kubectl, and helm then re-run.")
		os.Exit(0) // not a failure
	}

	if err := setupSuite(); err != nil {
		fmt.Fprintf(os.Stderr, "E2E suite setup failed: %v\n", err)
		teardownSuite()
		os.Exit(1)
	}

	code := m.Run()
	teardownSuite()
	os.Exit(code)
}

// ── Suite lifecycle ───────────────────────────────────────────────────────────

func checkPrerequisites() error {
	for _, bin := range []string{"kind", "kubectl", "helm"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("%s not found in PATH", bin)
		}
	}
	return nil
}

func setupSuite() error {
	ctx := context.Background()

	// Reuse an existing cluster if already present; otherwise create one.
	existing, _ := exec.CommandContext(ctx, "kind", "get", "clusters").Output()
	if !bytes.Contains(existing, []byte(e2eClusterName)) {
		fmt.Fprintln(os.Stderr, ">>> creating kind cluster", e2eClusterName)
		cmd := exec.CommandContext(ctx, "kind", "create", "cluster",
			"--name", e2eClusterName, "--wait", "120s")
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("kind create cluster: %w", err)
		}
	} else {
		fmt.Fprintln(os.Stderr, ">>> reusing existing kind cluster", e2eClusterName)
	}

	// Write kubeconfig to a temp file so we can pass it explicitly to all tools.
	raw, err := exec.CommandContext(ctx,
		"kind", "get", "kubeconfig", "--name", e2eClusterName).Output()
	if err != nil {
		return fmt.Errorf("kind get kubeconfig: %w", err)
	}
	f, err := os.CreateTemp("", "optipilot-e2e-*.yaml")
	if err != nil {
		return fmt.Errorf("create kubeconfig tmpfile: %w", err)
	}
	if _, err = f.Write(raw); err != nil {
		return fmt.Errorf("write kubeconfig: %w", err)
	}
	_ = f.Close()
	kubeconfigPath = f.Name()

	// Build controller-runtime client against the kind cluster.
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("build rest.Config: %w", err)
	}
	testClient, err = client.New(cfg, client.Options{Scheme: testScheme})
	if err != nil {
		return fmt.Errorf("create k8s client: %w", err)
	}

	// Install CRDs before the Helm chart (crds/ directory is pre-install).
	if err := kubectl(ctx, "apply", "-f", "../../helm/optipilot/crds/"); err != nil {
		return fmt.Errorf("apply CRDs: %w", err)
	}

	// Install OptiPilot via Helm (dry-run mode; no real Prometheus required).
	if err := helmInstall(ctx); err != nil {
		return fmt.Errorf("helm install: %w", err)
	}

	// Wait until the manager pod is ready before running any tests.
	if err := kubectl(ctx, "wait", "pod",
		"--for=condition=Ready",
		"--selector=app.kubernetes.io/name=cluster-agent,app.kubernetes.io/instance="+helmRelease,
		"--namespace="+helmNamespace,
		"--timeout=120s"); err != nil {
		return fmt.Errorf("wait for manager pod: %w", err)
	}

	// Create an isolated namespace for test CRs.
	kubectl(ctx, "create", "namespace", testNamespace) // ignore error if already exists

	setupCompleted = true
	fmt.Fprintln(os.Stderr, ">>> E2E suite ready")
	return nil
}

func teardownSuite() {
	if kubeconfigPath == "" {
		return
	}
	ctx := context.Background()
	kubectl(ctx, "delete", "namespace", testNamespace, "--ignore-not-found")
	_ = os.Remove(kubeconfigPath)
	// The kind cluster is intentionally NOT deleted so it can be reused.
	// Use: kind delete cluster --name optipilot-e2e
}
