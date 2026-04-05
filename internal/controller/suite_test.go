package controller

import (
	"context"
	"path/filepath"
	goruntime "runtime"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	policyv1alpha1 "github.com/optipilot-ai/optipilot/api/policy/v1alpha1"
	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
	tenantv1alpha1 "github.com/optipilot-ai/optipilot/api/tenant/v1alpha1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	ctx, cancel = context.WithCancel(context.TODO())

	_, filename, _, _ := goruntime.Caller(0)

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join(filepath.Dir(filename), "..", "..", "config", "crd", "bases"),
		},
		ErrorIfCRDPathMissing: false,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	scheme := kruntime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(slov1alpha1.AddToScheme(scheme))
	utilruntime.Must(policyv1alpha1.AddToScheme(scheme))
	utilruntime.Must(tenantv1alpha1.AddToScheme(scheme))

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	Expect((&ServiceObjectiveReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("serviceobjective-controller"),
		// Evaluator is intentionally nil in unit tests; Prometheus is not available.
	}).SetupWithManager(mgr)).To(Succeed())

	Expect((&OptimizationPolicyReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("optimizationpolicy-controller"),
		// PolicyEngine intentionally nil in envtest — CEL compilation is tested in internal/cel.
	}).SetupWithManager(mgr)).To(Succeed())

	Expect((&TenantProfileReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).To(Succeed())
	}()
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).To(Succeed())
})
