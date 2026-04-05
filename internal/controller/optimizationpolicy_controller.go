package controller

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	policyv1alpha1 "github.com/optipilot-ai/optipilot/api/policy/v1alpha1"
	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/cel"
)

// OptimizationPolicyReconciler reconciles an OptimizationPolicy object.
type OptimizationPolicyReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	PolicyEngine *cel.PolicyEngine
	Recorder     record.EventRecorder
}

// +kubebuilder:rbac:groups=policy.optipilot.ai,resources=optimizationpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy.optipilot.ai,resources=optimizationpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=policy.optipilot.ai,resources=optimizationpolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups=slo.optipilot.ai,resources=serviceobjectives,verbs=get;list;watch

// Reconcile compiles CEL constraints and updates policy status.
func (r *OptimizationPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var policy policyv1alpha1.OptimizationPolicy
	if err := r.Get(ctx, req.NamespacedName, &policy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.Info("Reconciling OptimizationPolicy", "name", policy.Name, "namespace", policy.Namespace)

	// Skip CEL compilation if no engine is wired (e.g. in unit tests).
	if r.PolicyEngine != nil {
		if err := r.PolicyEngine.Compile(&policy); err != nil {
			errMsg := fmt.Sprintf("CEL compilation failed: %v", err)
			policy.Status.CELCompilationStatus = "Failed: " + err.Error()
			apimeta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
				Type:               "CELCompiled",
				Status:             metav1.ConditionFalse,
				Reason:             "CompilationFailed",
				Message:            errMsg,
				ObservedGeneration: policy.Generation,
			})
			if r.Recorder != nil {
				r.Recorder.Event(&policy, corev1.EventTypeWarning, "CELCompilationFailed", errMsg)
			}
		} else {
			policy.Status.CELCompilationStatus = "OK"
			apimeta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
				Type:               "CELCompiled",
				Status:             metav1.ConditionTrue,
				Reason:             "CompilationSucceeded",
				Message:            "All constraints compiled successfully",
				ObservedGeneration: policy.Generation,
			})
		}
	}

	// Count matched ServiceObjectives.
	matched, err := r.countMatchedServices(ctx, &policy)
	if err == nil {
		policy.Status.MatchedServices = matched
	}

	policy.Status.ObservedGeneration = policy.Generation
	if err := r.Status().Update(ctx, &policy); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// FindPoliciesForService returns all OptimizationPolicies whose selector matches
// the ServiceObjective's labels, sorted descending by priority (highest first).
func (r *OptimizationPolicyReconciler) FindPoliciesForService(ctx context.Context, so *slov1alpha1.ServiceObjective) ([]policyv1alpha1.OptimizationPolicy, error) {
	var list policyv1alpha1.OptimizationPolicyList
	if err := r.List(ctx, &list, client.InNamespace(so.Namespace)); err != nil {
		return nil, err
	}

	soLabels := labels.Set(so.Labels)
	var matched []policyv1alpha1.OptimizationPolicy
	for _, p := range list.Items {
		if p.Spec.Selector == nil {
			// nil selector matches everything in the namespace.
			matched = append(matched, p)
			continue
		}
		sel, err := metav1.LabelSelectorAsSelector(p.Spec.Selector)
		if err != nil {
			continue
		}
		if sel.Matches(soLabels) {
			matched = append(matched, p)
		}
	}

	// Sort by priority descending (higher priority wins).
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].Spec.Priority > matched[j].Spec.Priority
	})
	return matched, nil
}

// countMatchedServices returns the number of ServiceObjectives that match this policy.
func (r *OptimizationPolicyReconciler) countMatchedServices(ctx context.Context, policy *policyv1alpha1.OptimizationPolicy) (int32, error) {
	var soList slov1alpha1.ServiceObjectiveList
	if err := r.List(ctx, &soList, client.InNamespace(policy.Namespace)); err != nil {
		return 0, err
	}

	if policy.Spec.Selector == nil {
		return int32(len(soList.Items)), nil
	}
	sel, err := metav1.LabelSelectorAsSelector(policy.Spec.Selector)
	if err != nil {
		return 0, err
	}

	var count int32
	for _, so := range soList.Items {
		if sel.Matches(labels.Set(so.Labels)) {
			count++
		}
	}
	return count, nil
}

// SetupWithManager registers the controller with the manager.
func (r *OptimizationPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&policyv1alpha1.OptimizationPolicy{}).
		Complete(r)
}
