package hubgrpc

import "context"

// OptiPilotHubService defines the gRPC service interface exposed by the hub.
// Spoke agents call these methods to register, report status, and get directives.
type OptiPilotHubService interface {
	// RegisterCluster is called once by each spoke agent on startup.
	RegisterCluster(ctx context.Context, req *RegisterClusterRequest) (*RegisterClusterResponse, error)

	// ReportStatus is called periodically (heartbeat) by spoke agents.
	ReportStatus(ctx context.Context, report *ClusterStatusReport) (*StatusAck, error)

	// GetDirective is called by a spoke to fetch pending directives from the hub.
	GetDirective(ctx context.Context, req *GetDirectiveRequest) (*GetDirectiveResponse, error)

	// RequestTrafficShift is called by a spoke to request traffic redistribution.
	RequestTrafficShift(ctx context.Context, req *TrafficShiftRequest) (*TrafficShiftResponse, error)
}
