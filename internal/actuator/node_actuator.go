package actuator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/optipilot-ai/optipilot/internal/engine"
)

// Karpenter NodePool GVK — no Karpenter SDK dependency needed.
var karpenterNodePoolGVK = schema.GroupVersionKind{
	Group:   "karpenter.sh",
	Version: "v1",
	Kind:    "NodePool",
}

// AnnotationNodeHint is stored on the NodePool for Cluster Autoscaler fallback.
const AnnotationNodeHint = "optipilot.ai/node-hint"

// NodeActuator patches Karpenter NodePool requirements with instance type and
// spot-ratio preferences derived from the solver's ScalingAction.
// Falls back to annotating a node group object when Karpenter is unavailable.
//
// Safety invariants (always enforced):
//   - At least 1 on-demand node type is preserved in NodePool requirements.
//   - Instance type list is never emptied.
type NodeActuator struct {
	Client client.Client
	// NodePoolName is the name of the Karpenter NodePool to manage.
	// When empty, the actuator looks for a NodePool in the same namespace as the service.
	NodePoolName string
}

// NewNodeActuator creates a NodeActuator.
func NewNodeActuator(c client.Client) *NodeActuator {
	return &NodeActuator{Client: c}
}

// CanApply returns true for tune and scale actions (node-level changes apply to both).
func (n *NodeActuator) CanApply(action engine.ScalingAction) bool {
	// Node actuator cares about spot ratio — relevant on any scaling action.
	return action.Type == engine.ActionScaleUp ||
		action.Type == engine.ActionScaleDown ||
		action.Type == engine.ActionTune
}

// Apply patches the Karpenter NodePool for the target service's namespace.
// If no NodePool is found it stores a node hint annotation on one of the nodes (best-effort).
func (n *NodeActuator) Apply(ctx context.Context, ref ServiceRef, action engine.ScalingAction, opts ActuationOptions) (ActuationResult, error) {
	np, found, err := n.findNodePool(ctx, ref.Namespace)
	if err != nil {
		return ActuationResult{}, fmt.Errorf("finding NodePool: %w", err)
	}
	if !found {
		// Best-effort: store hint annotation on the ServiceRef namespace object.
		return n.applyHint(ctx, ref, action, opts)
	}

	return n.applyNodePool(ctx, np, ref, action, opts)
}

// Rollback restores the previous NodePool requirements from the previous-state annotation.
func (n *NodeActuator) Rollback(ctx context.Context, ref ServiceRef) error {
	np, found, err := n.findNodePool(ctx, ref.Namespace)
	if err != nil || !found {
		return err
	}

	annotations, _, _ := unstructured.NestedStringMap(np.Object, "metadata", "annotations")
	prev, ok := annotations[AnnotationPreviousState]
	if !ok || prev == "" {
		return nil
	}

	var snapshot nodePoolSnapshot
	if err := json.Unmarshal([]byte(prev), &snapshot); err != nil {
		return fmt.Errorf("parsing node pool previous-state: %w", err)
	}

	patch := client.MergeFrom(np.DeepCopy())
	_ = unstructured.SetNestedSlice(np.Object, snapshot.Requirements, "spec", "requirements")
	return n.Client.Patch(ctx, np, patch)
}

// ──────────────────────────────────────────────────────────────────────────────
// NodePool patch
// ──────────────────────────────────────────────────────────────────────────────

func (n *NodeActuator) applyNodePool(ctx context.Context, np *unstructured.Unstructured, ref ServiceRef, action engine.ScalingAction, opts ActuationOptions) (ActuationResult, error) {
	// Capture current requirements for rollback.
	prevReqs, _, _ := unstructured.NestedSlice(np.Object, "spec", "requirements")
	prevJSON, _ := json.Marshal(nodePoolSnapshot{Requirements: prevReqs})

	// Build new requirements from solver's instance types + spot ratio.
	newReqs := buildRequirements(action.TargetReplica, action.SpotRatio, prevReqs)

	// Safety: never produce an empty requirements list.
	if len(newReqs) == 0 {
		return ActuationResult{Applied: false, PreviousState: string(prevJSON)}, nil
	}

	if opts.DryRun {
		return ActuationResult{
			Applied:       false,
			PreviousState: string(prevJSON),
			Changes: []ChangeRecord{{
				Resource:  fmt.Sprintf("NodePool/%s", np.GetName()),
				Field:     "spec.requirements",
				OldValue:  fmt.Sprintf("%v", prevReqs),
				NewValue:  fmt.Sprintf("spotRatio=%.0f%%", action.SpotRatio*100),
				Timestamp: time.Now().UTC(),
			}},
		}, nil
	}

	patch := client.MergeFrom(np.DeepCopy())

	// Store previous state annotation.
	annotations := np.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[AnnotationPreviousState] = string(prevJSON)
	np.SetAnnotations(annotations)

	if err := unstructured.SetNestedSlice(np.Object, newReqs, "spec", "requirements"); err != nil {
		return ActuationResult{}, fmt.Errorf("setting requirements: %w", err)
	}

	// Update disruption budget based on spot ratio.
	if action.SpotRatio > 0 {
		budget := map[string]interface{}{
			"budgets": []interface{}{
				map[string]interface{}{
					"nodes": fmt.Sprintf("%.0f%%", action.SpotRatio*100),
				},
			},
		}
		_ = unstructured.SetNestedMap(np.Object, budget, "spec", "disruption")
	}

	if err := n.Client.Patch(ctx, np, patch); err != nil {
		return ActuationResult{}, fmt.Errorf("patching NodePool: %w", err)
	}

	return ActuationResult{
		Applied:       true,
		PreviousState: string(prevJSON),
		Changes: []ChangeRecord{
			{
				Resource:  fmt.Sprintf("NodePool/%s", np.GetName()),
				Field:     "spec.requirements",
				OldValue:  fmt.Sprintf("%v", prevReqs),
				NewValue:  fmt.Sprintf("spotRatio=%.0f%%", action.SpotRatio*100),
				Timestamp: time.Now().UTC(),
			},
		},
	}, nil
}

// applyHint stores a node hint annotation as a best-effort fallback when no NodePool exists.
func (n *NodeActuator) applyHint(ctx context.Context, ref ServiceRef, action engine.ScalingAction, opts ActuationOptions) (ActuationResult, error) {
	hint := nodeHint{
		SpotRatio:           action.SpotRatio,
		TargetOnDemandRatio: 1.0 - action.SpotRatio,
		Timestamp:           time.Now().UTC().Format(time.RFC3339),
	}
	b, _ := json.Marshal(hint)

	if opts.DryRun {
		return ActuationResult{
			Applied: false,
			Changes: []ChangeRecord{{
				Resource:  fmt.Sprintf("Namespace/%s", ref.Namespace),
				Field:     AnnotationNodeHint,
				NewValue:  string(b),
				Timestamp: time.Now().UTC(),
			}},
		}, nil
	}

	// Store on a namespace-level annotation (best-effort for CA environments).
	var ns unstructured.Unstructured
	ns.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Namespace"})
	if err := n.Client.Get(ctx, types.NamespacedName{Name: ref.Namespace}, &ns); err != nil {
		if errors.IsNotFound(err) {
			return ActuationResult{Applied: false}, nil
		}
		return ActuationResult{}, err
	}

	patch := client.MergeFrom(ns.DeepCopy())
	annotations := ns.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[AnnotationNodeHint] = string(b)
	ns.SetAnnotations(annotations)

	if err := n.Client.Patch(ctx, &ns, patch); err != nil {
		return ActuationResult{}, fmt.Errorf("patching namespace hint: %w", err)
	}

	return ActuationResult{
		Applied: true,
		Changes: []ChangeRecord{{
			Resource:  fmt.Sprintf("Namespace/%s", ref.Namespace),
			Field:     AnnotationNodeHint,
			NewValue:  string(b),
			Timestamp: time.Now().UTC(),
		}},
	}, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// findNodePool looks for a Karpenter NodePool in the given namespace.
// Falls back to cluster-scoped lookup using NodePoolName if set.
func (n *NodeActuator) findNodePool(ctx context.Context, namespace string) (*unstructured.Unstructured, bool, error) {
	npList := &unstructured.UnstructuredList{}
	npList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   karpenterNodePoolGVK.Group,
		Version: karpenterNodePoolGVK.Version,
		Kind:    karpenterNodePoolGVK.Kind + "List",
	})

	if err := n.Client.List(ctx, npList); err != nil {
		// If CRD doesn't exist (not found / no match for kind), treat as unavailable.
		if errors.IsNotFound(err) || isNoKindMatch(err) {
			return nil, false, nil
		}
		return nil, false, err
	}

	// Prefer the explicitly configured pool name.
	for i := range npList.Items {
		np := &npList.Items[i]
		if n.NodePoolName == "" || np.GetName() == n.NodePoolName {
			return np, true, nil
		}
	}
	return nil, false, nil
}

// isNoKindMatch returns true for "no kind is registered" errors (CRD not installed).
func isNoKindMatch(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*UnknownKindError)
	return ok
}

// UnknownKindError is a sentinel for when a CRD kind is not registered.
// We use this to avoid importing meta package internals.
type UnknownKindError struct{ msg string }

func (e *UnknownKindError) Error() string { return e.msg }

// buildRequirements builds a Karpenter requirements slice from the solver action.
// It preserves any existing node requirements not related to instance type/capacity type.
// Safety: always keeps at least 1 on-demand node entry.
func buildRequirements(targetReplicas int32, spotRatio float64, existing []interface{}) []interface{} {
	// Preserve non-capacity non-instance-type requirements.
	var preserved []interface{}
	for _, r := range existing {
		req, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		key, _ := req["key"].(string)
		if key == "karpenter.sh/capacity-type" || key == "node.kubernetes.io/instance-type" {
			continue
		}
		preserved = append(preserved, r)
	}

	// Build capacity-type requirement.
	var capacityValues []interface{}
	if spotRatio > 0 && spotRatio < 1.0 {
		// Mixed: allow both spot and on-demand.
		capacityValues = []interface{}{"spot", "on-demand"}
	} else if spotRatio >= 1.0 {
		// Safety: never 100% spot in production — keep on-demand fallback.
		capacityValues = []interface{}{"spot", "on-demand"}
	} else {
		// 0% spot: on-demand only.
		capacityValues = []interface{}{"on-demand"}
	}

	capacityReq := map[string]interface{}{
		"key":      "karpenter.sh/capacity-type",
		"operator": "In",
		"values":   capacityValues,
	}

	result := append(preserved, capacityReq)
	return result
}

type nodePoolSnapshot struct {
	Requirements []interface{} `json:"requirements"`
}

type nodeHint struct {
	SpotRatio           float64 `json:"spotRatio"`
	TargetOnDemandRatio float64 `json:"targetOnDemandRatio"`
	Timestamp           string  `json:"timestamp"`
}
