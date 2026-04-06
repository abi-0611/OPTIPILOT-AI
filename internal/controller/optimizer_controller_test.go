package controller

import (
	"math"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	slov1alpha1 "github.com/optipilot-ai/optipilot/api/slo/v1alpha1"
	"github.com/optipilot-ai/optipilot/internal/cel"
	"github.com/optipilot-ai/optipilot/internal/engine"
)

func TestBuildSLOStatus(t *testing.T) {
	so := &slov1alpha1.ServiceObjective{
		Status: slov1alpha1.ServiceObjectiveStatus{
			BudgetRemaining: "45.5%",
			CurrentBurn:     map[string]string{"availability": "1.2", "latency": "0.8"},
			Conditions: []metav1.Condition{
				{Type: conditionSLOCompliant, Status: metav1.ConditionTrue},
			},
		},
	}

	status := buildSLOStatus(so)
	if !status.Compliant {
		t.Error("expected compliant=true")
	}
	if math.Abs(status.BudgetRemaining-0.455) > 0.001 {
		t.Errorf("budget = %f, want ~0.455", status.BudgetRemaining)
	}
	if status.BurnRate != 1.2 {
		t.Errorf("burn rate = %f, want 1.2", status.BurnRate)
	}
}

func TestServiceObjectiveReadyForOptimization(t *testing.T) {
	tests := []struct {
		name       string
		conditions []metav1.Condition
		want       bool
	}{
		{
			name: "ready when target found and SLO known",
			conditions: []metav1.Condition{
				{Type: conditionTargetFound, Status: metav1.ConditionTrue},
				{Type: conditionSLOCompliant, Status: metav1.ConditionFalse},
			},
			want: true,
		},
		{
			name: "not ready when SLO unknown",
			conditions: []metav1.Condition{
				{Type: conditionTargetFound, Status: metav1.ConditionTrue},
				{Type: conditionSLOCompliant, Status: metav1.ConditionUnknown},
			},
			want: false,
		},
		{
			name:       "not ready when no conditions",
			conditions: nil,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			so := &slov1alpha1.ServiceObjective{
				Status: slov1alpha1.ServiceObjectiveStatus{Conditions: tt.conditions},
			}
			got := serviceObjectiveReadyForOptimization(so)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRightSizingComputation(t *testing.T) {
	// Simulate what buildSolverInput does for right-sizing.
	tests := []struct {
		name       string
		current    cel.CurrentState
		wantCPU    *float64
		wantMemory *float64
	}{
		{
			name: "over-provisioned CPU should recommend lower",
			current: cel.CurrentState{
				Replicas:      4,
				CPURequest:    0.5, // 500m per pod
				CPUUsage:      0.4, // 400m total across 4 pods = 100m/pod
				MemoryRequest: 1.0, // 1 GiB per pod
				MemoryUsage:   3.2, // 3.2 GiB total = 0.8 GiB/pod
			},
			wantCPU:    float64Ptr(0.13), // 0.1 * 1.3 = 0.13
			wantMemory: nil,              // 0.8 * 1.3 = 1.04, within 20% of 1.0
		},
		{
			name: "under-provisioned memory should recommend higher",
			current: cel.CurrentState{
				Replicas:      2,
				CPURequest:    0.25,
				CPUUsage:      0.4,  // 0.2/pod → 0.26 right-sized, within 20% of 0.25
				MemoryRequest: 0.25, // 256Mi
				MemoryUsage:   1.0,  // 0.5 GiB/pod → 0.65 right-sized
			},
			wantCPU:    nil,
			wantMemory: float64Ptr(0.65), // 0.5 * 1.3 = 0.65
		},
		{
			name: "zero usage should not right-size",
			current: cel.CurrentState{
				Replicas:      2,
				CPURequest:    0.1,
				CPUUsage:      0,
				MemoryRequest: 0.25,
				MemoryUsage:   0,
			},
			wantCPU:    nil,
			wantMemory: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := engine.SolverInput{Current: tt.current}

			// Replicate the right-sizing logic from buildSolverInput
			if input.Current.CPUUsage > 0 && input.Current.Replicas > 0 {
				perReplicaCPU := (input.Current.CPUUsage / float64(input.Current.Replicas)) * 1.3
				if perReplicaCPU < 0.01 {
					perReplicaCPU = 0.01
				}
				if math.Abs(perReplicaCPU-input.Current.CPURequest) > input.Current.CPURequest*0.2 {
					input.RightSizedCPU = &perReplicaCPU
				}
			}
			if input.Current.MemoryUsage > 0 && input.Current.Replicas > 0 {
				perReplicaMemory := (input.Current.MemoryUsage / float64(input.Current.Replicas)) * 1.3
				if perReplicaMemory < 0.03125 {
					perReplicaMemory = 0.03125
				}
				if math.Abs(perReplicaMemory-input.Current.MemoryRequest) > input.Current.MemoryRequest*0.2 {
					input.RightSizedMemory = &perReplicaMemory
				}
			}

			if tt.wantCPU == nil && input.RightSizedCPU != nil {
				t.Errorf("expected nil RightSizedCPU, got %f", *input.RightSizedCPU)
			}
			if tt.wantCPU != nil {
				if input.RightSizedCPU == nil {
					t.Fatal("expected non-nil RightSizedCPU")
				}
				if math.Abs(*input.RightSizedCPU-*tt.wantCPU) > 0.01 {
					t.Errorf("RightSizedCPU = %f, want ~%f", *input.RightSizedCPU, *tt.wantCPU)
				}
			}

			if tt.wantMemory == nil && input.RightSizedMemory != nil {
				t.Errorf("expected nil RightSizedMemory, got %f", *input.RightSizedMemory)
			}
			if tt.wantMemory != nil {
				if input.RightSizedMemory == nil {
					t.Fatal("expected non-nil RightSizedMemory")
				}
				if math.Abs(*input.RightSizedMemory-*tt.wantMemory) > 0.01 {
					t.Errorf("RightSizedMemory = %f, want ~%f", *input.RightSizedMemory, *tt.wantMemory)
				}
			}
		})
	}
}

func TestParsePercentOrZero(t *testing.T) {
	tests := []struct {
		raw  string
		want float64
	}{
		{"45.5%", 0.455},
		{"0%", 0},
		{"100%", 1.0},
		{"", 0},
		{"invalid", 0},
	}
	for _, tt := range tests {
		got := parsePercentOrZero(tt.raw)
		if math.Abs(got-tt.want) > 0.001 {
			t.Errorf("parsePercentOrZero(%q) = %f, want %f", tt.raw, got, tt.want)
		}
	}
}

func float64Ptr(v float64) *float64 { return &v }
