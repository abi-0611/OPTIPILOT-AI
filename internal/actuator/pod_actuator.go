package actuator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/optipilot-ai/optipilot/internal/engine"
)

// PodActuator scales workloads via HPA (preferred) or direct Deployment patch.
// It supports three modes, selected automatically:
//  1. HPA — when a HorizontalPodAutoscaler for the target exists
//  2. Direct — when no HPA exists, patch Deployment spec.replicas directly
//  3. VPA annotation — store resource recommendation in annotation (no VPA CRD dep)
type PodActuator struct {
	Client client.Client
	// MinReplicas is the floor enforced on every scaling action (default: 1).
	MinReplicas int32
}

// NewPodActuator creates a PodActuator with the given client.
func NewPodActuator(c client.Client) *PodActuator {
	return &PodActuator{Client: c, MinReplicas: 1}
}

// CanApply returns true for scale_up, scale_down, and tune actions.
func (p *PodActuator) CanApply(action engine.ScalingAction) bool {
	return action.Type == engine.ActionScaleUp ||
		action.Type == engine.ActionScaleDown ||
		action.Type == engine.ActionTune
}

// Apply scales the target workload. It tries HPA first; falls back to direct Deployment patch.
func (p *PodActuator) Apply(ctx context.Context, ref ServiceRef, action engine.ScalingAction, opts ActuationOptions) (ActuationResult, error) {
	target := int32(action.TargetReplica)

	// Enforce minimum replicas.
	min := p.MinReplicas
	if min < 1 {
		min = 1
	}
	if target < min {
		target = min
	}

	// Try HPA first.
	hpaResult, ok, err := p.applyHPA(ctx, ref, target, action, opts)
	if err != nil {
		return ActuationResult{}, err
	}
	if ok {
		// Also store VPA resource recommendation in annotation.
		if action.Type == engine.ActionTune {
			_ = p.annotateVPA(ctx, ref, action)
		}
		return hpaResult, nil
	}

	// Fall back to direct Deployment scaling.
	return p.applyDeployment(ctx, ref, target, action, opts)
}

// Rollback restores the previous state from the annotation on the Deployment.
func (p *PodActuator) Rollback(ctx context.Context, ref ServiceRef) error {
	var deploy appsv1.Deployment
	if err := p.Client.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, &deploy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("getting deployment for rollback: %w", err)
	}

	prev, ok := deploy.Annotations[AnnotationPreviousState]
	if !ok || prev == "" {
		return nil // nothing to rollback
	}

	var snapshot deploymentSnapshot
	if err := json.Unmarshal([]byte(prev), &snapshot); err != nil {
		return fmt.Errorf("parsing previous-state annotation: %w", err)
	}

	patch := client.MergeFrom(deploy.DeepCopy())
	deploy.Spec.Replicas = &snapshot.Replicas
	if err := p.Client.Patch(ctx, &deploy, patch); err != nil {
		return fmt.Errorf("rollback patch: %w", err)
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// HPA mode
// ──────────────────────────────────────────────────────────────────────────────

func (p *PodActuator) applyHPA(ctx context.Context, ref ServiceRef, target int32, action engine.ScalingAction, opts ActuationOptions) (ActuationResult, bool, error) {
	var hpaList autoscalingv2.HorizontalPodAutoscalerList
	if err := p.Client.List(ctx, &hpaList, client.InNamespace(ref.Namespace)); err != nil {
		return ActuationResult{}, false, fmt.Errorf("listing HPAs: %w", err)
	}

	// Find the HPA that targets our workload.
	var hpa *autoscalingv2.HorizontalPodAutoscaler
	for i := range hpaList.Items {
		h := &hpaList.Items[i]
		if h.Spec.ScaleTargetRef.Name == ref.Name &&
			(h.Spec.ScaleTargetRef.Kind == ref.Kind || ref.Kind == "") {
			hpa = h
			break
		}
	}
	if hpa == nil {
		return ActuationResult{}, false, nil
	}

	// Capture previous state.
	prevMin := int32(1)
	if hpa.Spec.MinReplicas != nil {
		prevMin = *hpa.Spec.MinReplicas
	}
	prevMax := hpa.Spec.MaxReplicas

	prevJSON, _ := json.Marshal(hpaSnapshot{MinReplicas: prevMin, MaxReplicas: prevMax})

	if opts.DryRun {
		return ActuationResult{
			Applied:       false,
			PreviousState: string(prevJSON),
			Changes: []ChangeRecord{
				{
					Resource:  fmt.Sprintf("HPA/%s", hpa.Name),
					Field:     "spec.maxReplicas",
					OldValue:  fmt.Sprintf("%d", prevMax),
					NewValue:  fmt.Sprintf("%d", target),
					Timestamp: time.Now().UTC(),
				},
			},
		}, true, nil
	}

	// Enforce maxPercent if set in scaling behavior.
	target = applyMaxChange(prevMax, target, opts.MaxChange)

	patch := client.MergeFrom(hpa.DeepCopy())
	hpa.Spec.MaxReplicas = target
	hpa.Spec.MinReplicas = &target
	// Store previous state for rollback in an annotation on the HPA.
	if hpa.Annotations == nil {
		hpa.Annotations = make(map[string]string)
	}
	hpa.Annotations[AnnotationPreviousState] = string(prevJSON)

	if err := p.Client.Patch(ctx, hpa, patch); err != nil {
		return ActuationResult{}, false, fmt.Errorf("patching HPA: %w", err)
	}

	return ActuationResult{
		Applied:       true,
		PreviousState: string(prevJSON),
		Changes: []ChangeRecord{
			{
				Resource:  fmt.Sprintf("HPA/%s", hpa.Name),
				Field:     "spec.minReplicas",
				OldValue:  fmt.Sprintf("%d", prevMin),
				NewValue:  fmt.Sprintf("%d", target),
				Timestamp: time.Now().UTC(),
			},
			{
				Resource:  fmt.Sprintf("HPA/%s", hpa.Name),
				Field:     "spec.maxReplicas",
				OldValue:  fmt.Sprintf("%d", prevMax),
				NewValue:  fmt.Sprintf("%d", target),
				Timestamp: time.Now().UTC(),
			},
		},
	}, true, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Direct Deployment mode
// ──────────────────────────────────────────────────────────────────────────────

func (p *PodActuator) applyDeployment(ctx context.Context, ref ServiceRef, target int32, action engine.ScalingAction, opts ActuationOptions) (ActuationResult, error) {
	var deploy appsv1.Deployment
	if err := p.Client.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, &deploy); err != nil {
		return ActuationResult{}, fmt.Errorf("getting deployment: %w", err)
	}

	prevReplicas := int32(1)
	if deploy.Spec.Replicas != nil {
		prevReplicas = *deploy.Spec.Replicas
	}

	prevJSON, _ := json.Marshal(deploymentSnapshot{Replicas: prevReplicas})

	if opts.DryRun {
		return ActuationResult{
			Applied:       false,
			PreviousState: string(prevJSON),
			Changes: []ChangeRecord{
				{
					Resource:  fmt.Sprintf("Deployment/%s", deploy.Name),
					Field:     "spec.replicas",
					OldValue:  fmt.Sprintf("%d", prevReplicas),
					NewValue:  fmt.Sprintf("%d", target),
					Timestamp: time.Now().UTC(),
				},
			},
		}, nil
	}

	// Enforce maxPercent change guard.
	target = applyMaxChange(prevReplicas, target, opts.MaxChange)

	patch := client.MergeFrom(deploy.DeepCopy())
	deploy.Spec.Replicas = &target

	// Persist previous state and resource requests in annotations.
	if deploy.Annotations == nil {
		deploy.Annotations = make(map[string]string)
	}
	deploy.Annotations[AnnotationPreviousState] = string(prevJSON)

	// Update resource requests on all containers if the action includes CPU/memory changes.
	if action.CPURequest > 0 || action.MemoryRequest > 0 {
		for i := range deploy.Spec.Template.Spec.Containers {
			c := &deploy.Spec.Template.Spec.Containers[i]
			if c.Resources.Requests == nil {
				c.Resources.Requests = corev1.ResourceList{}
			}
			if action.CPURequest > 0 {
				c.Resources.Requests[corev1.ResourceCPU] = *resource.NewMilliQuantity(
					int64(action.CPURequest*1000), resource.DecimalSI)
			}
			if action.MemoryRequest > 0 {
				c.Resources.Requests[corev1.ResourceMemory] = *resource.NewQuantity(
					int64(action.MemoryRequest*1024*1024*1024), resource.BinarySI)
			}
		}
	}

	if err := p.Client.Patch(ctx, &deploy, patch); err != nil {
		return ActuationResult{}, fmt.Errorf("patching deployment: %w", err)
	}

	return ActuationResult{
		Applied:       true,
		PreviousState: string(prevJSON),
		Changes: []ChangeRecord{
			{
				Resource:  fmt.Sprintf("Deployment/%s", deploy.Name),
				Field:     "spec.replicas",
				OldValue:  fmt.Sprintf("%d", prevReplicas),
				NewValue:  fmt.Sprintf("%d", target),
				Timestamp: time.Now().UTC(),
			},
		},
	}, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// VPA annotation mode
// ──────────────────────────────────────────────────────────────────────────────

func (p *PodActuator) annotateVPA(ctx context.Context, ref ServiceRef, action engine.ScalingAction) error {
	var deploy appsv1.Deployment
	if err := p.Client.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, &deploy); err != nil {
		return err
	}
	rec := vpaRecommendation{
		CPU:       fmt.Sprintf("%.0fm", action.CPURequest*1000),
		Memory:    fmt.Sprintf("%.0fMi", action.MemoryRequest*1024),
		Timestamp: metav1.Now(),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	patch := client.MergeFrom(deploy.DeepCopy())
	if deploy.Annotations == nil {
		deploy.Annotations = make(map[string]string)
	}
	deploy.Annotations[AnnotationVPARecommendation] = string(b)
	return p.Client.Patch(ctx, &deploy, patch)
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// applyMaxChange clamps the target to at most maxChangeFrac fractional change from current.
// 0 or negative maxChangeFrac means no limit.
func applyMaxChange(current, target int32, maxChangeFrac float64) int32 {
	if maxChangeFrac <= 0 || current == 0 {
		return target
	}
	maxDelta := int32(float64(current) * maxChangeFrac)
	if maxDelta < 1 {
		maxDelta = 1
	}
	if target > current+maxDelta {
		return current + maxDelta
	}
	if target < current-maxDelta {
		return current - maxDelta
	}
	return target
}

type deploymentSnapshot struct {
	Replicas int32 `json:"replicas"`
}

type hpaSnapshot struct {
	MinReplicas int32 `json:"minReplicas"`
	MaxReplicas int32 `json:"maxReplicas"`
}

type vpaRecommendation struct {
	CPU       string      `json:"cpu"`
	Memory    string      `json:"memory"`
	Timestamp metav1.Time `json:"timestamp"`
}
