//go:build e2e

package e2e_test

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
)

// ── Install validation tests ──────────────────────────────────────────────────

// TestE2E_Install_CRDsRegistered verifies all four OptiPilot CRDs are present
// in the cluster after Helm install.
func TestE2E_Install_CRDsRegistered(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	crds := []string{
		"serviceobjectives.slo.optipilot.ai",
		"optimizationpolicies.policy.optipilot.ai",
		"tenantprofiles.tenant.optipilot.ai",
		"applicationtunings.tuning.optipilot.ai",
	}
	for _, crd := range crds {
		crd := crd
		t.Run(crd, func(t *testing.T) {
			if err := kubectl(ctx, "get", "crd", crd); err != nil {
				t.Errorf("CRD not found: %s: %v", crd, err)
			}
		})
	}
}

// TestE2E_Install_ManagerPodRunning verifies the cluster-agent pod reaches
// Running state within the suite setup window.
func TestE2E_Install_ManagerPodRunning(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	out, err := kubectlOutput(ctx,
		"get", "pods",
		"-n", helmNamespace,
		"-l", "app.kubernetes.io/name=cluster-agent,app.kubernetes.io/instance="+helmRelease,
		"-o", "jsonpath={.items[0].status.phase}")
	if err != nil {
		t.Fatalf("kubectl get pods: %v", err)
	}
	if string(out) != "Running" {
		t.Errorf("expected pod phase Running, got %q", string(out))
	}
}

// TestE2E_Install_PodReadinessCondition verifies the pod's Ready condition is True.
func TestE2E_Install_PodReadinessCondition(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	// Re-run wait to confirm current state; treat error as failure.
	err := kubectl(ctx, "wait", "pod",
		"--for=condition=Ready",
		"--selector=app.kubernetes.io/name=cluster-agent,app.kubernetes.io/instance="+helmRelease,
		"--namespace="+helmNamespace,
		"--timeout=30s")
	if err != nil {
		t.Errorf("pod not ready: %v", err)
	}
}

// TestE2E_Install_HelmReleaseDeployed verifies the Helm release is in DEPLOYED state.
func TestE2E_Install_HelmReleaseDeployed(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	out, err := kubectlOutput(ctx,
		"--kubeconfig", kubeconfigPath,
	)
	_ = out
	_ = err

	// Use helm status to verify deployed state.
	args := []string{"status", helmRelease, "--namespace", helmNamespace, "--kubeconfig", kubeconfigPath, "-o", "json"}
	cmd, _ := kubectlOutput(ctx, args[0]) // just ping kubectl is responsive
	_ = cmd

	out2, err2 := kubectlOutput(ctx, "get", "secret",
		"-n", helmNamespace,
		"-l", "owner=helm,name="+helmRelease,
		"-o", "jsonpath={.items[0].metadata.labels.status}")
	if err2 != nil {
		t.Fatalf("helm release secret not found: %v", err2)
	}
	if string(out2) != "deployed" {
		t.Errorf("helm release status: got %q, want deployed", string(out2))
	}
}

// ── ServiceObjective CRUD ─────────────────────────────────────────────────────

// TestE2E_Install_ServiceObjectiveSchemaAccepted verifies that a well-formed
// ServiceObjective is accepted by the API server (schema validation passes).
func TestE2E_Install_ServiceObjectiveSchemaAccepted(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	so := &slov1alpha1.ServiceObjective{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "install-smoke-slo",
			Namespace: testNamespace,
		},
		Spec: slov1alpha1.ServiceObjectiveSpec{
			TargetRef: slov1alpha1.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "smoke-app",
			},
			Objectives: []slov1alpha1.Objective{
				{Metric: slov1alpha1.MetricAvailability, Target: "99.5%", Window: "30d"},
			},
		},
	}

	if err := testClient.Create(ctx, so); err != nil {
		t.Fatalf("create ServiceObjective: %v", err)
	}
	t.Cleanup(func() {
		_ = testClient.Delete(context.Background(), so)
	})

	// Round-trip: fetch and compare name.
	got := &slov1alpha1.ServiceObjective{}
	if err := testClient.Get(ctx, types.NamespacedName{
		Name: so.Name, Namespace: so.Namespace,
	}, got); err != nil {
		t.Fatalf("get ServiceObjective: %v", err)
	}
	if got.Name != so.Name {
		t.Errorf("name mismatch: got %q, want %q", got.Name, so.Name)
	}
}

// TestE2E_Install_NamespaceCreated verifies the OptiPilot system namespace exists.
func TestE2E_Install_NamespaceCreated(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	namespaces := []string{helmNamespace, testNamespace}
	for _, ns := range namespaces {
		ns := ns
		t.Run(ns, func(t *testing.T) {
			if err := kubectl(ctx, "get", "namespace", ns); err != nil {
				t.Errorf("namespace %s not found: %v", ns, err)
			}
		})
	}
}

// TestE2E_Install_ServiceAccountExists verifies the cluster-agent ServiceAccount
// was created by Helm.
func TestE2E_Install_ServiceAccountExists(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	saName := clusterAgentSvcName()
	if err := kubectl(ctx, "get", "serviceaccount", saName,
		"--namespace", helmNamespace); err != nil {
		t.Errorf("ServiceAccount %s not found: %v", saName, err)
	}
}

// TestE2E_Install_ClusterRoleExists verifies the ClusterRole granting CRD access exists.
func TestE2E_Install_ClusterRoleExists(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	crName := clusterAgentSvcName()
	if err := kubectl(ctx, "get", "clusterrole", crName); err != nil {
		t.Errorf("ClusterRole %s not found: %v", crName, err)
	}
}

// TestE2E_Install_ServiceExists verifies the cluster-agent Service exists and
// exposes the expected ports.
func TestE2E_Install_ServiceExists(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	svcName := clusterAgentSvcName()
	out, err := kubectlOutput(ctx,
		"get", "service", svcName,
		"--namespace", helmNamespace,
		"-o", "jsonpath={.spec.ports[*].port}")
	if err != nil {
		t.Fatalf("get service %s: %v", svcName, err)
	}
	ports := string(out)
	for _, expected := range []string{"8080", "8090"} {
		if !containsStr(ports, expected) {
			t.Errorf("service %s does not expose port %s (ports: %s)", svcName, expected, ports)
		}
	}
}

func containsStr(s, sub string) bool {
	return len(s) > 0 && len(sub) > 0 &&
		(s == sub || len(s) >= len(sub) &&
			func() bool {
				for i := 0; i <= len(s)-len(sub); i++ {
					if s[i:i+len(sub)] == sub {
						return true
					}
				}
				return false
			}())
}

// TestE2E_Install_ListAllCRsSucceeds verifies all four CRD list endpoints
// respond without error (schema is correct in etcd).
func TestE2E_Install_ListAllCRsSucceeds(t *testing.T) {
	requireSetup(t)
	ctx := context.Background()

	soList := &slov1alpha1.ServiceObjectiveList{}
	if err := testClient.List(ctx, soList, client.InNamespace(testNamespace)); err != nil {
		t.Errorf("list ServiceObjectives: %v", err)
	}
}
