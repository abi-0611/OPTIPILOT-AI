package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	tenantv1alpha1 "github.com/optipilot-ai/optipilot/api/tenant/v1alpha1"
)

// TenantProfileReconciler reconciles a TenantProfile object
type TenantProfileReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=tenant.optipilot.ai,resources=tenantprofiles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=tenant.optipilot.ai,resources=tenantprofiles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=tenant.optipilot.ai,resources=tenantprofiles/finalizers,verbs=update

// Reconcile implements the reconciliation loop for TenantProfile resources.
func (r *TenantProfileReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var tp tenantv1alpha1.TenantProfile
	if err := r.Get(ctx, req.NamespacedName, &tp); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("Reconciling TenantProfile", "name", tp.Name)

	// Phase 7 will implement tenant fair-share quota enforcement here.

	return ctrl.Result{}, nil
}

// SetupWithManager registers the controller with the manager.
func (r *TenantProfileReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&tenantv1alpha1.TenantProfile{}).
		Complete(r)
}
