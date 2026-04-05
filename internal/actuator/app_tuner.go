package actuator

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/optipilot-ai/optipilot/internal/engine"
)

// AnnotationTuningBoundsMin defines the min bound for a ConfigMap key.
// Format: "optipilot.ai/min/{key}" = "2"
const annotationBoundsMinPrefix = "optipilot.ai/min/"

// annotationBoundsMaxPrefix defines the max bound for a ConfigMap key.
const annotationBoundsMaxPrefix = "optipilot.ai/max/"

// AppTuner reads a tuning ConfigMap for a service and applies TuningParams from a
// ScalingAction within configured bounds. Optionally triggers a Deployment rolling
// restart when AnnotationRestartOnTuning is present on the Deployment.
//
// ConfigMap lookup order:
//  1. Explicit ConfigMapName field
//  2. Convention: "{serviceName}-config" in the same namespace
//
// Bounds are stored as annotations on the ConfigMap:
//
//	optipilot.ai/min/{key}: "2"
//	optipilot.ai/max/{key}: "32"
type AppTuner struct {
	Client        client.Client
	ConfigMapName string // optional; defaults to "{ref.Name}-config"
}

// NewAppTuner creates an AppTuner.
func NewAppTuner(c client.Client) *AppTuner {
	return &AppTuner{Client: c}
}

// CanApply returns true for tune actions that carry TuningParams.
func (a *AppTuner) CanApply(action engine.ScalingAction) bool {
	return action.Type == engine.ActionTune && len(action.TuningParams) > 0
}

// Apply reads the target ConfigMap, clamps each param value within its bounds,
// patches the ConfigMap, and optionally triggers a rolling restart.
func (a *AppTuner) Apply(ctx context.Context, ref ServiceRef, action engine.ScalingAction, opts ActuationOptions) (ActuationResult, error) {
	cm, err := a.findConfigMap(ctx, ref)
	if err != nil {
		return ActuationResult{}, fmt.Errorf("app tuner: finding ConfigMap: %w", err)
	}
	if cm == nil {
		return ActuationResult{Applied: false}, nil
	}

	// Snapshot current data for rollback.
	prevData := make(map[string]string, len(cm.Data))
	for k, v := range cm.Data {
		prevData[k] = v
	}
	prevJSON, _ := json.Marshal(configMapSnapshot{Data: prevData})

	// Build updated data with clamped values.
	newData := make(map[string]string, len(cm.Data))
	for k, v := range cm.Data {
		newData[k] = v
	}

	var changes []ChangeRecord
	for key, proposed := range action.TuningParams {
		clamped := clampStringValue(proposed, boundFor(cm.Annotations, annotationBoundsMinPrefix+key), boundFor(cm.Annotations, annotationBoundsMaxPrefix+key))
		old := newData[key]
		newData[key] = clamped
		changes = append(changes, ChangeRecord{
			Resource:  fmt.Sprintf("ConfigMap/%s", cm.Name),
			Field:     key,
			OldValue:  old,
			NewValue:  clamped,
			Timestamp: time.Now().UTC(),
		})
	}

	if opts.DryRun {
		return ActuationResult{
			Applied:       false,
			PreviousState: string(prevJSON),
			Changes:       changes,
		}, nil
	}

	patch := client.MergeFrom(cm.DeepCopy())
	// Store previous state for rollback.
	if cm.Annotations == nil {
		cm.Annotations = make(map[string]string)
	}
	cm.Annotations[AnnotationPreviousState] = string(prevJSON)
	cm.Data = newData

	if err := a.Client.Patch(ctx, cm, patch); err != nil {
		return ActuationResult{}, fmt.Errorf("patching ConfigMap: %w", err)
	}

	// Optionally trigger rolling restart on the Deployment.
	if err := a.maybeRestartDeployment(ctx, ref); err != nil {
		return ActuationResult{}, fmt.Errorf("triggering rolling restart: %w", err)
	}

	return ActuationResult{
		Applied:       true,
		PreviousState: string(prevJSON),
		Changes:       changes,
	}, nil
}

// Rollback restores the ConfigMap data from the AnnotationPreviousState annotation.
func (a *AppTuner) Rollback(ctx context.Context, ref ServiceRef) error {
	cm, err := a.findConfigMap(ctx, ref)
	if err != nil || cm == nil {
		return err
	}

	prev, ok := cm.Annotations[AnnotationPreviousState]
	if !ok || prev == "" {
		return nil
	}

	var snapshot configMapSnapshot
	if err := json.Unmarshal([]byte(prev), &snapshot); err != nil {
		return fmt.Errorf("parsing ConfigMap previous-state: %w", err)
	}

	patch := client.MergeFrom(cm.DeepCopy())
	cm.Data = snapshot.Data
	delete(cm.Annotations, AnnotationPreviousState)
	return a.Client.Patch(ctx, cm, patch)
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

func (a *AppTuner) findConfigMap(ctx context.Context, ref ServiceRef) (*corev1.ConfigMap, error) {
	name := a.ConfigMapName
	if name == "" {
		name = ref.Name + "-config"
	}

	var cm corev1.ConfigMap
	if err := a.Client.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: name}, &cm); err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &cm, nil
}

// maybeRestartDeployment triggers a rolling restart if the Deployment carries
// the AnnotationRestartOnTuning annotation.
func (a *AppTuner) maybeRestartDeployment(ctx context.Context, ref ServiceRef) error {
	var dep appsv1.Deployment
	if err := a.Client.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, &dep); err != nil {
		if isNotFound(err) {
			return nil
		}
		return err
	}

	if dep.Annotations[AnnotationRestartOnTuning] != "true" {
		return nil
	}

	patch := client.MergeFrom(dep.DeepCopy())
	if dep.Spec.Template.Annotations == nil {
		dep.Spec.Template.Annotations = make(map[string]string)
	}
	dep.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().UTC().Format(time.RFC3339)

	return a.Client.Patch(ctx, &dep, patch)
}

// clampStringValue parses proposed as a float64 and clamps it within [minStr, maxStr].
// If proposed is non-numeric, it is returned as-is. If bounds are empty, no clamp applied.
func clampStringValue(proposed, minStr, maxStr string) string {
	v, err := strconv.ParseFloat(strings.TrimSpace(proposed), 64)
	if err != nil {
		return proposed // non-numeric values are passed through
	}
	if minStr != "" {
		if minVal, err := strconv.ParseFloat(strings.TrimSpace(minStr), 64); err == nil && v < minVal {
			v = minVal
		}
	}
	if maxStr != "" {
		if maxVal, err := strconv.ParseFloat(strings.TrimSpace(maxStr), 64); err == nil && v > maxVal {
			v = maxVal
		}
	}
	// Preserve integer format if value has no fractional part.
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// boundFor extracts a bound value from an annotation map given a fully-qualified key.
func boundFor(annotations map[string]string, key string) string {
	if annotations == nil {
		return ""
	}
	return annotations[key]
}

// isNotFound returns true for Kubernetes not-found errors without importing errors package.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "not found")
}

type configMapSnapshot struct {
	Data map[string]string `json:"data"`
}
