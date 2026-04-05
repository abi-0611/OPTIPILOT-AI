package main

import (
	"flag"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	policyv1alpha1 "github.com/optipilot-ai/optipilot/api/policy/v1alpha1"
	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
	tenantv1alpha1 "github.com/optipilot-ai/optipilot/api/tenant/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/api"
	"github.com/optipilot-ai/optipilot/internal/cel"
	"github.com/optipilot-ai/optipilot/internal/controller"
	"github.com/optipilot-ai/optipilot/internal/engine"
	"github.com/optipilot-ai/optipilot/internal/explainability"
	"github.com/optipilot-ai/optipilot/internal/global/spoke"
	"github.com/optipilot-ai/optipilot/internal/metrics"
	"github.com/optipilot-ai/optipilot/internal/slo"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")

	// Build-time version information injected by -ldflags.
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(slov1alpha1.AddToScheme(scheme))
	utilruntime.Must(policyv1alpha1.AddToScheme(scheme))
	utilruntime.Must(tenantv1alpha1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var prometheusURL string
	var optimizerInterval time.Duration
	var journalPath string
	var apiAddr string
	var hubEndpoint string
	var clusterName string
	var clusterProvider string
	var clusterRegion string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080",
		"The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081",
		"The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager.")
	flag.StringVar(&prometheusURL, "prometheus-url", "http://prometheus-operated.monitoring.svc.cluster.local:9090",
		"Base URL of the Prometheus instance used for SLO evaluation.")
	flag.DurationVar(&optimizerInterval, "optimizer-interval", 30*time.Second,
		"Interval between optimization cycles.")
	flag.StringVar(&journalPath, "journal-path", "/var/lib/optipilot/decisions.db",
		"Path to the SQLite decision journal database.")
	flag.StringVar(&apiAddr, "api-addr", ":8090",
		"Address for the OptiPilot REST API + dashboard server.")
	flag.StringVar(&hubEndpoint, "hub-endpoint", "",
		"gRPC address of the OptiPilot hub (e.g. hub.optipilot-system:50051). Empty disables spoke agent.")
	flag.StringVar(&clusterName, "cluster-name", "",
		"Name of this cluster for hub registration. Required when --hub-endpoint is set.")
	flag.StringVar(&clusterProvider, "cluster-provider", "other",
		"Cloud provider of this cluster (aws, gcp, azure, on-prem, other).")
	flag.StringVar(&clusterRegion, "cluster-region", "",
		"Region of this cluster.")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog.Info("starting OptiPilot manager", "version", version, "commit", commit, "buildDate", buildDate)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "optipilot-leader.optipilot.ai",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controller.ServiceObjectiveReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Evaluator: slo.NewSLOEvaluator(
			metrics.NewHTTPPrometheusClient(prometheusURL, 10*time.Second, 5*time.Second),
			slo.NewPromQLBuilderFromAnnotations(nil, "", ""),
		),
		Recorder: mgr.GetEventRecorderFor("serviceobjective-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ServiceObjective")
		os.Exit(1)
	}

	policyEngine, err := cel.NewPolicyEngine()
	if err != nil {
		setupLog.Error(err, "unable to create CEL policy engine")
		os.Exit(1)
	}

	if err = (&controller.OptimizationPolicyReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		PolicyEngine: policyEngine,
		Recorder:     mgr.GetEventRecorderFor("optimizationpolicy-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "OptimizationPolicy")
		os.Exit(1)
	}

	if err = (&controller.TenantProfileReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "TenantProfile")
		os.Exit(1)
	}

	// Decision Journal.
	journal, err := explainability.NewJournal(journalPath)
	if err != nil {
		setupLog.Error(err, "unable to create decision journal")
		os.Exit(1)
	}

	// Optimizer Controller (periodic loop).
	policyRecon := &controller.OptimizationPolicyReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		PolicyEngine: policyEngine,
		Recorder:     mgr.GetEventRecorderFor("optimizationpolicy-controller"),
	}

	optimizerCtrl := &controller.OptimizerController{
		Client:      mgr.GetClient(),
		Interval:    optimizerInterval,
		Solver:      engine.NewSolver(policyEngine, engine.DefaultMaxCandidates),
		PolicyRecon: policyRecon,
		Journal:     journal,
		Recorder:    mgr.GetEventRecorderFor("optimizer-controller"),
	}
	if err := mgr.Add(optimizerCtrl); err != nil {
		setupLog.Error(err, "unable to register optimizer controller")
		os.Exit(1)
	}

	// Create signal context once — reused by both the API server and the manager.
	// ctrl.SetupSignalHandler must only be called once per process.
	ctx := ctrl.SetupSignalHandler()

	// REST API + Dashboard server.
	// All three handler types implement api.RouteRegistrar via RegisterRoutes(*http.ServeMux).
	decisionsHandler := api.NewDecisionsAPIHandler(journal)
	apiServer := api.NewServer(apiAddr, dashboardFS, decisionsHandler)
	go func() {
		setupLog.Info("starting API server", "addr", apiAddr)
		if err := apiServer.Start(ctx); err != nil {
			setupLog.Error(err, "API server exited")
		}
	}()

	// Spoke agent — register with hub and send heartbeats.
	if hubEndpoint != "" {
		if clusterName == "" {
			setupLog.Error(nil, "--cluster-name is required when --hub-endpoint is set")
			os.Exit(1)
		}
		spokeAgent := spoke.NewSpokeAgent(hubEndpoint,
			spoke.RegistrationInfo{
				ClusterName: clusterName,
				Provider:    clusterProvider,
				Region:      clusterRegion,
			},
			&spoke.StaticCollector{
				ClusterName: clusterName,
				Health:      "Healthy",
			},
			&spoke.LogDirectiveHandler{},
		)
		if err := mgr.Add(spokeAgent); err != nil {
			setupLog.Error(err, "unable to register spoke agent")
			os.Exit(1)
		}
		setupLog.Info("spoke agent enabled", "hub", hubEndpoint, "cluster", clusterName)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
