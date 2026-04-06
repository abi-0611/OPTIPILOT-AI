package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
)

const sloCompliantCondition = "SLOCompliant"

// ServiceObjectivesHandler lists live ServiceObjective CRs and status for the dashboard.
type ServiceObjectivesHandler struct {
	Client client.Client
}

// NewServiceObjectivesHandler creates the handler.
func NewServiceObjectivesHandler(c client.Client) *ServiceObjectivesHandler {
	return &ServiceObjectivesHandler{Client: c}
}

// RegisterRoutes registers GET /api/v1/service-objectives.
func (h *ServiceObjectivesHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/service-objectives", h.handleList)
}

// ServiceObjectiveDTO is JSON for one ServiceObjective row.
type ServiceObjectiveDTO struct {
	Namespace              string               `json:"namespace"`
	Name                   string               `json:"name"`
	TargetName             string               `json:"targetName"`
	TargetKind             string               `json:"targetKind"`
	Compliant              *bool                `json:"compliant,omitempty"`
	BudgetRemainingPercent *float64             `json:"budgetRemainingPercent,omitempty"`
	LastEvaluation         *string              `json:"lastEvaluation,omitempty"`
	Objectives             []ObjectiveStatusDTO `json:"objectives"`
}

// ObjectiveStatusDTO combines spec + current burn from status.
type ObjectiveStatusDTO struct {
	Metric   string   `json:"metric"`
	Target   string   `json:"target"`
	Window   string   `json:"window,omitempty"`
	BurnRate *float64 `json:"burnRate,omitempty"`
}

func (h *ServiceObjectivesHandler) handleList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var list slov1alpha1.ServiceObjectiveList
	if err := h.Client.List(ctx, &list); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	out := make([]ServiceObjectiveDTO, 0, len(list.Items))
	for i := range list.Items {
		so := &list.Items[i]
		dto := ServiceObjectiveDTO{
			Namespace:  so.Namespace,
			Name:       so.Name,
			TargetName: so.Spec.TargetRef.Name,
			TargetKind: so.Spec.TargetRef.Kind,
		}
		if so.Status.LastEvaluation != nil {
			t := so.Status.LastEvaluation.Time.UTC().Format(time.RFC3339)
			dto.LastEvaluation = &t
		}
		if pct, ok := parseBudgetPercent(so.Status.BudgetRemaining); ok {
			dto.BudgetRemainingPercent = &pct
		}
		dto.Compliant = compliantFromConditions(so.Status.Conditions)

		for _, o := range so.Spec.Objectives {
			od := ObjectiveStatusDTO{
				Metric: string(o.Metric),
				Target: o.Target,
				Window: o.Window,
			}
			if so.Status.CurrentBurn != nil {
				if brStr, ok := so.Status.CurrentBurn[string(o.Metric)]; ok {
					if v, err := strconv.ParseFloat(strings.TrimSpace(brStr), 64); err == nil {
						od.BurnRate = &v
					}
				}
			}
			dto.Objectives = append(dto.Objectives, od)
		}
		out = append(out, dto)
	}
	writeJSON(w, out)
}

func parseBudgetPercent(s string) (float64, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(s, "%"))
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func compliantFromConditions(conds []metav1.Condition) *bool {
	for _, c := range conds {
		if c.Type != sloCompliantCondition {
			continue
		}
		switch c.Status {
		case metav1.ConditionTrue:
			t := true
			return &t
		case metav1.ConditionFalse:
			f := false
			return &f
		default:
			return nil
		}
	}
	return nil
}
