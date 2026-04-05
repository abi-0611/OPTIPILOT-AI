package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
	ctrlmetrics "github.com/optipilot-ai/optipilot/internal/metrics"
	"github.com/optipilot-ai/optipilot/internal/slo"
)

const (
	conditionSLOCompliant    = "SLOCompliant"
	conditionBudgetExhausted = "BudgetExhausted"
	conditionTargetFound     = "TargetFound"
	defaultEvalInterval      = 30 * time.Second
	exhaustionThreshold      = 0.05 // budget remaining < 5% → exhausted
)

// ServiceObjectiveReconciler reconciles a ServiceObjective object
type ServiceObjectiveReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Evaluator *slo.SLOEvaluator
	Recorder  record.EventRecorder
}

// +kubebuilder:rbac:groups=slo.optipilot.ai,resources=serviceobjectives,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=slo.optipilot.ai,resources=serviceobjectives/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=slo.optipilot.ai,resources=serviceobjectives/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile implements the reconciliation loop for ServiceObjective resources.
func (r *ServiceObjectiveReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var so slov1alpha1.ServiceObjective
	if err := r.Get(ctx, req.NamespacedName, &so); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("Reconciling ServiceObjective", "name", so.Name, "namespace", so.Namespace)

	// Verify the target workload exists.
	if !r.targetExists(ctx, &so) {
		apimeta.SetStatusCondition(&so.Status.Conditions, metav1.Condition{
			Type:               conditionTargetFound,
			Status:             metav1.ConditionFalse,
			Reason:             "NotFound",
			Message:            fmt.Sprintf("%s %q not found", so.Spec.TargetRef.Kind, so.Spec.TargetRef.Name),
			ObservedGeneration: so.Generation,
		})
		if err := r.Status().Update(ctx, &so); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: evalInterval(&so)}, nil
	}

	apimeta.SetStatusCondition(&so.Status.Conditions, metav1.Condition{
		Type:               conditionTargetFound,
		Status:             metav1.ConditionTrue,
		Reason:             "Found",
		Message:            fmt.Sprintf("%s %q is present", so.Spec.TargetRef.Kind, so.Spec.TargetRef.Name),
		ObservedGeneration: so.Generation,
	})

	// Skip evaluation when no evaluator is wired (e.g., unit tests without Prometheus).
	if r.Evaluator == nil {
		if err := r.Status().Update(ctx, &so); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: evalInterval(&so)}, nil
	}

	start := time.Now()
	result, evalErr := r.Evaluator.Evaluate(ctx, &so)
	duration := time.Since(start)

	ctrlmetrics.SLOEvaluationDuration.WithLabelValues(so.Namespace, so.Name).Observe(duration.Seconds())

	if evalErr != nil {
		logger.Error(evalErr, "SLO evaluation failed", "name", so.Name)
		ctrlmetrics.SLOEvaluationErrors.WithLabelValues(so.Namespace, so.Name, "evaluation_failed").Inc()
		if r.Recorder != nil {
			r.Recorder.Event(&so, corev1.EventTypeWarning, "EvaluationFailed", evalErr.Error())
		}
		apimeta.SetStatusCondition(&so.Status.Conditions, metav1.Condition{
			Type:               conditionSLOCompliant,
			Status:             metav1.ConditionUnknown,
			Reason:             "EvaluationFailed",
			Message:            evalErr.Error(),
			ObservedGeneration: so.Generation,
		})
		if err := r.Status().Update(ctx, &so); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: evalInterval(&so)}, nil
	}

	// Update status from evaluation result.
	now := metav1.Now()
	so.Status.LastEvaluation = &now
	so.Status.ObservedGeneration = so.Generation
	so.Status.BudgetRemaining = fmt.Sprintf("%.2f%%", result.BudgetRemaining*100)

	burnMap := make(map[string]string, len(result.Objectives))
	for _, obj := range result.Objectives {
		burnMap[string(obj.Metric)] = fmt.Sprintf("%.4f", obj.BurnRate)
		ctrlmetrics.SLOBurnRate.WithLabelValues(so.Namespace, so.Name, string(obj.Metric)).Set(obj.BurnRate)
	}
	so.Status.CurrentBurn = burnMap

	ctrlmetrics.SLOBudgetRemaining.WithLabelValues(so.Namespace, so.Name).Set(result.BudgetRemaining)

	// SLOCompliant condition.
	if result.AllCompliant {
		ctrlmetrics.SLOCompliant.WithLabelValues(so.Namespace, so.Name).Set(1)
		apimeta.SetStatusCondition(&so.Status.Conditions, metav1.Condition{
			Type:               conditionSLOCompliant,
			Status:             metav1.ConditionTrue,
			Reason:             "WithinTarget",
			Message:            "All objectives are within their targets",
			ObservedGeneration: so.Generation,
		})
	} else {
		ctrlmetrics.SLOCompliant.WithLabelValues(so.Namespace, so.Name).Set(0)
		violatingMetrics := collectViolatingMetrics(result)
		msg := fmt.Sprintf("Objectives violating targets: %s", strings.Join(violatingMetrics, ", "))
		if r.Recorder != nil {
			r.Recorder.Event(&so, corev1.EventTypeWarning, "SLOViolation", msg)
		}
		apimeta.SetStatusCondition(&so.Status.Conditions, metav1.Condition{
			Type:               conditionSLOCompliant,
			Status:             metav1.ConditionFalse,
			Reason:             "TargetViolation",
			Message:            msg,
			ObservedGeneration: so.Generation,
		})
	}

	// BudgetExhausted condition.
	if result.BudgetRemaining < exhaustionThreshold {
		msg := fmt.Sprintf("Error budget nearly exhausted: %.2f%% remaining", result.BudgetRemaining*100)
		if r.Recorder != nil {
			r.Recorder.Event(&so, corev1.EventTypeWarning, "BudgetExhausted", msg)
		}
		apimeta.SetStatusCondition(&so.Status.Conditions, metav1.Condition{
			Type:               conditionBudgetExhausted,
			Status:             metav1.ConditionTrue,
			Reason:             "BudgetLow",
			Message:            msg,
			ObservedGeneration: so.Generation,
		})
	} else {
		apimeta.SetStatusCondition(&so.Status.Conditions, metav1.Condition{
			Type:               conditionBudgetExhausted,
			Status:             metav1.ConditionFalse,
			Reason:             "BudgetAdequate",
			Message:            fmt.Sprintf("%.2f%% budget remaining", result.BudgetRemaining*100),
			ObservedGeneration: so.Generation,
		})
	}

	// Check burn rate alert thresholds.
	r.checkBurnRateAlerts(ctx, &so, result)

	if err := r.Status().Update(ctx, &so); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: evalInterval(&so)}, nil
}

// targetExists checks whether the TargetRef workload is present in the cluster.
func (r *ServiceObjectiveReconciler) targetExists(ctx context.Context, so *slov1alpha1.ServiceObjective) bool {
	ref := so.Spec.TargetRef
	switch ref.Kind {
	case "Deployment":
		var dep appsv1.Deployment
		err := r.Get(ctx, types.NamespacedName{Namespace: so.Namespace, Name: ref.Name}, &dep)
		return err == nil || !apierrors.IsNotFound(err)
	default:
		// For other kinds, optimistically assume the target exists.
		return true
	}
}

// checkBurnRateAlerts emits Events when burn rate thresholds are crossed.
func (r *ServiceObjectiveReconciler) checkBurnRateAlerts(ctx context.Context, so *slov1alpha1.ServiceObjective, result *slo.SLOEvaluationResult) {
	if so.Spec.ErrorBudget == nil || r.Recorder == nil {
		return
	}
	for _, alert := range so.Spec.ErrorBudget.BurnRateAlerts {
		for _, obj := range result.Objectives {
			if obj.BurnRate >= alert.Factor {
				msg := fmt.Sprintf("[%s] Burn rate %.2f >= threshold %.2f for metric %s (window: %s/%s)",
					alert.Severity, obj.BurnRate, alert.Factor, obj.Metric, alert.ShortWindow, alert.LongWindow)
				eventType := corev1.EventTypeWarning
				if alert.Severity == "info" {
					eventType = corev1.EventTypeNormal
				}
				r.Recorder.Event(so, eventType, "BurnRateAlert", msg)
			}
		}
	}
}

// collectViolatingMetrics returns metric names that are not compliant.
func collectViolatingMetrics(result *slo.SLOEvaluationResult) []string {
	var names []string
	for _, obj := range result.Objectives {
		if !obj.Compliant {
			names = append(names, string(obj.Metric))
		}
	}
	return names
}

// evalInterval returns the requeue interval from the spec or the default.
func evalInterval(so *slov1alpha1.ServiceObjective) time.Duration {
	raw := strings.TrimSpace(so.Spec.EvaluationInterval)
	if raw == "" {
		return defaultEvalInterval
	}
	if strings.HasSuffix(raw, "s") {
		if n, err := strconv.Atoi(strings.TrimSuffix(raw, "s")); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	if strings.HasSuffix(raw, "m") {
		if n, err := strconv.Atoi(strings.TrimSuffix(raw, "m")); err == nil {
			return time.Duration(n) * time.Minute
		}
	}
	return defaultEvalInterval
}

// SetupWithManager registers the controller with the manager.
func (r *ServiceObjectiveReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&slov1alpha1.ServiceObjective{}).
		Complete(r)
}
