package hubgrpc

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding"
)

func init() {
	// Register the JSON codec so both client and server can use it.
	encoding.RegisterCodec(JSONCodec{})
}

// SpokeClient is a gRPC client that connects to the hub's OptiPilotHub service.
type SpokeClient struct {
	conn *grpc.ClientConn
}

// NewSpokeClient dials the hub at addr. If tlsCreds is nil, insecure transport is used.
func NewSpokeClient(addr string, tlsCreds credentials.TransportCredentials) (*SpokeClient, error) {
	var opts []grpc.DialOption
	if tlsCreds != nil {
		opts = append(opts, grpc.WithTransportCredentials(tlsCreds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	opts = append(opts, grpc.WithDefaultCallOptions(grpc.CallContentSubtype("json")))

	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("hubgrpc: dial %s: %w", addr, err)
	}
	return &SpokeClient{conn: conn}, nil
}

// RegisterCluster sends a registration request to the hub.
func (c *SpokeClient) RegisterCluster(ctx context.Context, req *RegisterClusterRequest) (*RegisterClusterResponse, error) {
	resp := new(RegisterClusterResponse)
	if err := c.conn.Invoke(ctx, methodDesc("RegisterCluster"), req, resp); err != nil {
		return nil, fmt.Errorf("RegisterCluster: %w", err)
	}
	return resp, nil
}

// ReportStatus sends a heartbeat / status report to the hub.
func (c *SpokeClient) ReportStatus(ctx context.Context, report *ClusterStatusReport) (*StatusAck, error) {
	resp := new(StatusAck)
	if err := c.conn.Invoke(ctx, methodDesc("ReportStatus"), report, resp); err != nil {
		return nil, fmt.Errorf("ReportStatus: %w", err)
	}
	return resp, nil
}

// GetDirective asks the hub for any pending directives for this cluster.
func (c *SpokeClient) GetDirective(ctx context.Context, clusterName string) (*GetDirectiveResponse, error) {
	resp := new(GetDirectiveResponse)
	req := &GetDirectiveRequest{ClusterName: clusterName}
	if err := c.conn.Invoke(ctx, methodDesc("GetDirective"), req, resp); err != nil {
		return nil, fmt.Errorf("GetDirective: %w", err)
	}
	return resp, nil
}

// RequestTrafficShift asks the hub to redistribute traffic.
func (c *SpokeClient) RequestTrafficShift(ctx context.Context, req *TrafficShiftRequest) (*TrafficShiftResponse, error) {
	resp := new(TrafficShiftResponse)
	if err := c.conn.Invoke(ctx, methodDesc("RequestTrafficShift"), req, resp); err != nil {
		return nil, fmt.Errorf("RequestTrafficShift: %w", err)
	}
	return resp, nil
}

// Close shuts down the underlying connection.
func (c *SpokeClient) Close() error {
	return c.conn.Close()
}
