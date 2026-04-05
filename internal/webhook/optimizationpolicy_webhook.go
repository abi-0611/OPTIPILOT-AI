package webhook

import (
	"context"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	policyv1alpha1 "github.com/optipilot-ai/optipilot/api/policy/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/cel"
)

// OptimizationPolicyWebhook validates OptimizationPolicy resources.
// It compiles CEL constraints at admission time so that invalid expressions
// are rejected with a clear error message before the CR is persisted.
type OptimizationPolicyWebhook struct {
	engine *cel.PolicyEngine
}

// NewOptimizationPolicyWebhook creates a webhook backed by a fresh PolicyEngine.
func NewOptimizationPolicyWebhook() (*OptimizationPolicyWebhook, error) {
	engine, err := cel.NewPolicyEngine()
	if err != nil {
		return nil, fmt.Errorf("creating policy engine for webhook: %w", err)
	}
	return &OptimizationPolicyWebhook{engine: engine}, nil
}

// SetupWithManager registers the webhook handlers with the controller-runtime manager.
func (w *OptimizationPolicyWebhook) SetupWithManager(mgr ctrl.Manager) error {
	mgr.GetWebhookServer().Register(
		"/validate-policy-optipilot-ai-v1alpha1-optimizationpolicy",
		&webhook.Admission{Handler: w},
	)
	return nil
}

// Handle implements admission.Handler by delegating to ValidateCreate/ValidateUpdate.
func (w *OptimizationPolicyWebhook) Handle(ctx context.Context, req admission.Request) admission.Response {
	policy := &policyv1alpha1.OptimizationPolicy{}

	scheme := runtime.NewScheme()
	_ = policyv1alpha1.AddToScheme(scheme)
	decoder := admission.NewDecoder(scheme)

	if err := decoder.Decode(req, policy); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	var warnings admission.Warnings
	var err error

	switch req.Operation {
	case admissionv1.Create:
		warnings, err = w.ValidateCreate(ctx, policy)
	case admissionv1.Update:
		oldPolicy := &policyv1alpha1.OptimizationPolicy{}
		if decodeErr := decoder.DecodeRaw(req.OldObject, oldPolicy); decodeErr != nil {
			return admission.Errored(http.StatusBadRequest, decodeErr)
		}
		warnings, err = w.ValidateUpdate(ctx, oldPolicy, policy)
	case admissionv1.Delete:
		warnings, err = w.ValidateDelete(ctx, policy)
	}

	if err != nil {
		return admission.Denied(err.Error())
	}
	resp := admission.Allowed("validation passed")
	resp.Warnings = warnings
	return resp
}

// ValidateCreate validates a new OptimizationPolicy.
func (w *OptimizationPolicyWebhook) ValidateCreate(_ context.Context, policy *policyv1alpha1.OptimizationPolicy) (admission.Warnings, error) {
	// 1. Compile CEL constraints — rejects any expression that is syntactically
	//    invalid or returns a non-bool type.
	if err := w.engine.Compile(policy); err != nil {
		return nil, field.Invalid(
			field.NewPath("spec").Child("constraints"),
			policy.Spec.Constraints,
			fmt.Sprintf("CEL compilation failed: %v", err),
		)
	}

	var warnings admission.Warnings

	// 2. Validate objective weights — warn if the sum exceeds 1.0 (they will be
	//    normalized at solve time, but the user may have made a mistake).
	totalWeight := 0.0
	for _, obj := range policy.Spec.Objectives {
		totalWeight += obj.Weight
	}
	if totalWeight > 1.0 {
		warnings = append(warnings, fmt.Sprintf(
			"objective weights sum to %.2f (> 1.0); they will be normalised at solve time",
			totalWeight,
		))
	}

	// 3. Warn if scale-down aggressiveness is high.
	if policy.Spec.ScalingBehavior != nil {
		if sd := policy.Spec.ScalingBehavior.ScaleDown; sd != nil && sd.MaxPercent > 50 {
			warnings = append(warnings,
				fmt.Sprintf("scaleDown.maxPercent=%d is aggressive (>50%%); consider a lower value", sd.MaxPercent),
			)
		}
	}

	return warnings, nil
}

// ValidateUpdate validates an update to a policy — CEL constraints are re-compiled.
func (w *OptimizationPolicyWebhook) ValidateUpdate(ctx context.Context, _ *policyv1alpha1.OptimizationPolicy, newPolicy *policyv1alpha1.OptimizationPolicy) (admission.Warnings, error) {
	return w.ValidateCreate(ctx, newPolicy)
}

// ValidateDelete is a no-op for OptimizationPolicy.
func (w *OptimizationPolicyWebhook) ValidateDelete(_ context.Context, _ *policyv1alpha1.OptimizationPolicy) (admission.Warnings, error) {
	return nil, nil
}
