// Package spoke implements the spoke agent that runs in each workload cluster
// and communicates with the OptiPilot hub via gRPC.
package spoke

import (
	"context"
	"time"

	hubgrpc "github.com/optipilot-ai/optipilot/internal/global/grpc"
)

// StatusCollector gathers local cluster metrics for heartbeat reports.
// Implementations typically query the Kubernetes metrics API or Prometheus.
type StatusCollector interface {
	// Collect returns the current cluster status snapshot.
	Collect(ctx context.Context) (*hubgrpc.ClusterStatusReport, error)
}

// DirectiveHandler processes directives received from the hub.
type DirectiveHandler interface {
	// Handle is called for each directive fetched from the hub.
	Handle(ctx context.Context, d hubgrpc.Directive) error
}

// RegistrationInfo holds the static identity of this spoke cluster.
type RegistrationInfo struct {
	ClusterName         string
	Provider            string
	Region              string
	Endpoint            string
	CarbonIntensityGCO2 float64
	Labels              map[string]string
	Capabilities        *hubgrpc.Capabilities
	CostProfile         *hubgrpc.CostProfileMsg
}

// StaticCollector is a simple StatusCollector that returns fixed values.
// Useful for testing or as a placeholder before real metrics integration.
type StaticCollector struct {
	ClusterName          string
	TotalCores           float64
	UsedCores            float64
	TotalMemoryGiB       float64
	UsedMemoryGiB        float64
	NodeCount            int32
	SLOCompliancePercent float64
	HourlyCostUSD        float64
	Health               string
}

func (s *StaticCollector) Collect(_ context.Context) (*hubgrpc.ClusterStatusReport, error) {
	return &hubgrpc.ClusterStatusReport{
		ClusterName:          s.ClusterName,
		TotalCores:           s.TotalCores,
		UsedCores:            s.UsedCores,
		TotalMemoryGiB:       s.TotalMemoryGiB,
		UsedMemoryGiB:        s.UsedMemoryGiB,
		NodeCount:            s.NodeCount,
		SLOCompliancePercent: s.SLOCompliancePercent,
		HourlyCostUSD:        s.HourlyCostUSD,
		Health:               s.Health,
		Timestamp:            time.Now(),
	}, nil
}

// LogDirectiveHandler is a DirectiveHandler that records directives for inspection.
// Useful for testing.
type LogDirectiveHandler struct {
	Handled []hubgrpc.Directive
}

func (h *LogDirectiveHandler) Handle(_ context.Context, d hubgrpc.Directive) error {
	h.Handled = append(h.Handled, d)
	return nil
}
