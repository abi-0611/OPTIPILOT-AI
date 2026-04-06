package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	tenantv1alpha1 "github.com/optipilot-ai/optipilot/api/tenant/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/tenant"
)

// TenantProfileReconciler reconciles a TenantProfile object
type TenantProfileReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// TenantManager receives a full sync of all TenantProfile CRs on each reconcile.
	// When nil (e.g. in envtest without wiring), Reconcile falls back to logging only.
	TenantManager *tenant.Manager
}

// +kubebuilder:rbac:groups=tenant.optipilot.ai,resources=tenantprofiles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=tenant.optipilot.ai,resources=tenantprofiles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=tenant.optipilot.ai,resources=tenantprofiles/finalizers,verbs=update

// Reconcile implements the reconciliation loop for TenantProfile resources.
func (r *TenantProfileReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if r.TenantManager != nil {
		var list tenantv1alpha1.TenantProfileList
		if err := r.List(ctx, &list); err != nil {
			return ctrl.Result{}, err
		}
		profiles := make([]tenantv1alpha1.TenantProfile, len(list.Items))
		copy(profiles, list.Items)
		r.TenantManager.UpdateProfiles(profiles)
		logger.Info("synced TenantProfiles to tenant manager", "count", len(profiles))
		return ctrl.Result{}, nil
	}

	var tp tenantv1alpha1.TenantProfile
	if err := r.Get(ctx, req.NamespacedName, &tp); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("Reconciling TenantProfile", "name", tp.Name)

	return ctrl.Result{}, nil
}

// SetupWithManager registers the controller with the manager.
func (r *TenantProfileReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&tenantv1alpha1.TenantProfile{}).
		Complete(r)
}
