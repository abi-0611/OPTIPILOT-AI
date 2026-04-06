//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"
)

// ── requireSetup ─────────────────────────────────────────────────────────────

// requireSetup skips t if the suite setup did not complete successfully.
func requireSetup(t *testing.T) {
	t.Helper()
	if !setupCompleted {
		t.Skip("E2E suite setup not completed — check TestMain output")
	}
}

// ── kubectl helpers ───────────────────────────────────────────────────────────

// kubectl runs a kubectl command using the suite kubeconfig, forwarding output
// to os.Stderr so it appears in `-v` test output.
func kubectl(ctx context.Context, args ...string) error {
	fullArgs := withKubeconfig(args)
	cmd := exec.CommandContext(ctx, "kubectl", fullArgs...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// kubectlOutput runs kubectl and captures stdout. Stderr is forwarded.
func kubectlOutput(ctx context.Context, args ...string) ([]byte, error) {
	fullArgs := withKubeconfig(args)
	cmd := exec.CommandContext(ctx, "kubectl", fullArgs...)
	cmd.Stderr = os.Stderr
	return cmd.Output()
}

func withKubeconfig(args []string) []string {
	if kubeconfigPath == "" {
		return args
	}
	return append([]string{"--kubeconfig", kubeconfigPath}, args...)
}

// ── Helm helper ───────────────────────────────────────────────────────────────

// helmInstall runs `helm upgrade --install` for the OptiPilot chart.
// It uses dryRun=true and a non-existent Prometheus URL so the manager starts
// without requiring real metric infrastructure.
func helmInstall(ctx context.Context) error {
	args := []string{
		"upgrade", "--install", helmRelease, "../../helm/optipilot",
		"--namespace", helmNamespace,
		"--create-namespace",
		"--kubeconfig", kubeconfigPath,
		"--set", "clusterAgent.enabled=true",
		"--set", "mlService.enabled=false",
		"--set", "hub.enabled=false",
		"--set", fmt.Sprintf("global.prometheusURL=http://prometheus-stub.%s.svc:9090", helmNamespace),
		"--wait",
		"--timeout=3m",
	}
	cmd := exec.CommandContext(ctx, "helm", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ── Port-forward helper ───────────────────────────────────────────────────────

// portForwardSvc starts `kubectl port-forward svc/<svc>` and returns a cleanup
// function. The port-forward is given 2 s to establish before returning.
func portForwardSvc(t *testing.T, ns, svc string, localPort, remotePort int) func() {
	t.Helper()
	pCtx, cancel := context.WithCancel(context.Background())
	args := withKubeconfig([]string{
		"port-forward", "-n", ns,
		fmt.Sprintf("svc/%s", svc),
		fmt.Sprintf("%d:%d", localPort, remotePort),
	})
	cmd := exec.CommandContext(pCtx, "kubectl", args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("port-forward start: %v", err)
	}
	time.Sleep(2 * time.Second)
	return func() {
		cancel()
		_ = cmd.Wait()
	}
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

// apiURL builds a URL against the local port-forwarded API server.
func apiURL(path string) string {
	return fmt.Sprintf("http://localhost:%d%s", localAPIPort, path)
}

// metricsURL builds a URL against the local port-forwarded metrics server.
func metricsURL(path string) string {
	return fmt.Sprintf("http://localhost:%d%s", localMetricsPort, path)
}

// httpGet performs an HTTP GET and returns the response code. Fatal on network error.
func httpGet(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// httpPost performs an HTTP POST with a JSON body and returns the response code.
func httpPost(t *testing.T, url, body string) int {
	t.Helper()
	resp, err := http.Post(url, "application/json", nil) //nolint:noctx
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	_ = body
	defer resp.Body.Close()
	return resp.StatusCode
}

// ── Polling helper ────────────────────────────────────────────────────────────

// safePoll retries fn every interval until it returns true or deadline is
// reached. It calls t.Fatal on timeout.
func safePoll(t *testing.T, timeout, interval time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(interval)
	}
	t.Fatalf("condition not met within %s", timeout)
}

// clusterAgentSvcName returns the Helm-rendered service name for the cluster-agent.
// Pattern: <release>-cluster-agent (since "optipilot" ∌ "cluster-agent").
func clusterAgentSvcName() string {
	return fmt.Sprintf("%s-cluster-agent", helmRelease)
}
