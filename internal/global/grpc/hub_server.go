package hubgrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

// ---------------------------------------------------------------------------
// gRPC service descriptor (manual — replaces protoc-generated code)
// ---------------------------------------------------------------------------

const serviceName = "optipilot.hub.v1.OptiPilotHub"

// methodDesc returns the full gRPC method name.
func methodDesc(method string) string { return "/" + serviceName + "/" + method }

// ---------------------------------------------------------------------------
// HubServer wraps a gRPC server and dispatches to an OptiPilotHubService.
// ---------------------------------------------------------------------------

// HubServer manages the gRPC listener, server lifecycle, and mTLS config.
type HubServer struct {
	addr    string
	svc     OptiPilotHubService
	gs      *grpc.Server
	mu      sync.Mutex
	stopped bool
}

// NewHubServer creates a HubServer. If tlsCreds is non-nil it enables mTLS.
func NewHubServer(addr string, svc OptiPilotHubService, tlsCreds credentials.TransportCredentials) *HubServer {
	var opts []grpc.ServerOption
	if tlsCreds != nil {
		opts = append(opts, grpc.Creds(tlsCreds))
	}
	gs := grpc.NewServer(opts...)

	hs := &HubServer{addr: addr, svc: svc, gs: gs}
	hs.registerMethods()
	return hs
}

// registerMethods wires each RPC method to the underlying service using a
// generic JSON-over-gRPC codec. This avoids the need for protoc-generated
// registration while remaining wire-compatible with any gRPC client that
// sends JSON-encoded payloads.
func (h *HubServer) registerMethods() {
	sd := grpc.ServiceDesc{
		ServiceName: serviceName,
		HandlerType: (*OptiPilotHubService)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "RegisterCluster", Handler: h.handleRegisterCluster},
			{MethodName: "ReportStatus", Handler: h.handleReportStatus},
			{MethodName: "GetDirective", Handler: h.handleGetDirective},
			{MethodName: "RequestTrafficShift", Handler: h.handleRequestTrafficShift},
		},
		Streams:  []grpc.StreamDesc{},
		Metadata: "optipilot_hub.proto",
	}
	h.gs.RegisterService(&sd, h.svc)
}

// Start begins listening. It blocks until the server is stopped or ctx is cancelled.
func (h *HubServer) Start(ctx context.Context) error {
	lis, err := net.Listen("tcp", h.addr)
	if err != nil {
		return fmt.Errorf("hubgrpc: listen %s: %w", h.addr, err)
	}
	// Capture the actual bound address (important when using port 0).
	h.mu.Lock()
	h.addr = lis.Addr().String()
	h.mu.Unlock()

	errCh := make(chan error, 1)
	go func() { errCh <- h.gs.Serve(lis) }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		h.mu.Lock()
		defer h.mu.Unlock()
		if !h.stopped {
			h.stopped = true
			h.gs.GracefulStop()
		}
		return nil
	}
}

// Stop forces immediate shutdown.
func (h *HubServer) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.stopped {
		h.stopped = true
		h.gs.Stop()
	}
}

// Addr returns the configured listen address.
func (h *HubServer) Addr() string { return h.addr }

// ---------------------------------------------------------------------------
// gRPC handler adapters (decode JSON, delegate to service, encode JSON)
// ---------------------------------------------------------------------------

func (h *HubServer) handleRegisterCluster(srv interface{}, ctx context.Context, dec func(interface{}) error, _ grpc.UnaryServerInterceptor) (interface{}, error) {
	req := new(RegisterClusterRequest)
	if err := dec(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode: %v", err)
	}
	return h.svc.RegisterCluster(ctx, req)
}

func (h *HubServer) handleReportStatus(srv interface{}, ctx context.Context, dec func(interface{}) error, _ grpc.UnaryServerInterceptor) (interface{}, error) {
	req := new(ClusterStatusReport)
	if err := dec(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode: %v", err)
	}
	return h.svc.ReportStatus(ctx, req)
}

func (h *HubServer) handleGetDirective(srv interface{}, ctx context.Context, dec func(interface{}) error, _ grpc.UnaryServerInterceptor) (interface{}, error) {
	req := new(GetDirectiveRequest)
	if err := dec(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode: %v", err)
	}
	return h.svc.GetDirective(ctx, req)
}

func (h *HubServer) handleRequestTrafficShift(srv interface{}, ctx context.Context, dec func(interface{}) error, _ grpc.UnaryServerInterceptor) (interface{}, error) {
	req := new(TrafficShiftRequest)
	if err := dec(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode: %v", err)
	}
	return h.svc.RequestTrafficShift(ctx, req)
}

// ---------------------------------------------------------------------------
// In-memory hub service for testing and early development
// ---------------------------------------------------------------------------

// MemoryHubService is a thread-safe in-memory implementation of OptiPilotHubService.
// It stores cluster registrations and status reports, and serves pre-loaded directives.
type MemoryHubService struct {
	mu           sync.RWMutex
	clusters     map[string]*RegisterClusterRequest
	lastStatus   map[string]*ClusterStatusReport
	directives   map[string][]Directive // clusterName → pending directives
	heartbeatTTL time.Duration
}

// NewMemoryHubService creates a MemoryHubService with a default 5-minute heartbeat TTL.
func NewMemoryHubService() *MemoryHubService {
	return &MemoryHubService{
		clusters:     make(map[string]*RegisterClusterRequest),
		lastStatus:   make(map[string]*ClusterStatusReport),
		directives:   make(map[string][]Directive),
		heartbeatTTL: 5 * time.Minute,
	}
}

func (m *MemoryHubService) RegisterCluster(_ context.Context, req *RegisterClusterRequest) (*RegisterClusterResponse, error) {
	if req.ClusterName == "" {
		return nil, status.Error(codes.InvalidArgument, "clusterName is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clusters[req.ClusterName] = req
	return &RegisterClusterResponse{Accepted: true, HeartbeatIntervalS: 30, Message: "registered"}, nil
}

func (m *MemoryHubService) ReportStatus(_ context.Context, report *ClusterStatusReport) (*StatusAck, error) {
	if report.ClusterName == "" {
		return nil, status.Error(codes.InvalidArgument, "clusterName is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.clusters[report.ClusterName]; !ok {
		return nil, status.Errorf(codes.NotFound, "cluster %q not registered", report.ClusterName)
	}
	if report.Timestamp.IsZero() {
		report.Timestamp = time.Now()
	}
	m.lastStatus[report.ClusterName] = report
	return &StatusAck{Received: true, Message: "ok"}, nil
}

func (m *MemoryHubService) GetDirective(_ context.Context, req *GetDirectiveRequest) (*GetDirectiveResponse, error) {
	if req.ClusterName == "" {
		return nil, status.Error(codes.InvalidArgument, "clusterName is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	pending := m.directives[req.ClusterName]
	// Drain — once fetched, directives are consumed.
	delete(m.directives, req.ClusterName)
	if pending == nil {
		pending = []Directive{}
	}
	return &GetDirectiveResponse{Directives: pending}, nil
}

func (m *MemoryHubService) RequestTrafficShift(_ context.Context, req *TrafficShiftRequest) (*TrafficShiftResponse, error) {
	if req.ClusterName == "" || req.Service == "" {
		return nil, status.Error(codes.InvalidArgument, "clusterName and service are required")
	}
	// In-memory: just accept.
	return &TrafficShiftResponse{Accepted: true, Message: "acknowledged"}, nil
}

// EnqueueDirective adds a directive for a specific cluster.
func (m *MemoryHubService) EnqueueDirective(clusterName string, d Directive) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.directives[clusterName] = append(m.directives[clusterName], d)
}

// GetRegistered returns names of all registered clusters.
func (m *MemoryHubService) GetRegistered() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.clusters))
	for n := range m.clusters {
		names = append(names, n)
	}
	return names
}

// GetLastStatus returns the most recent status for a cluster, or nil.
func (m *MemoryHubService) GetLastStatus(clusterName string) *ClusterStatusReport {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastStatus[clusterName]
}

// IsHealthy returns true if the cluster's last heartbeat is within the TTL.
func (m *MemoryHubService) IsHealthy(clusterName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st, ok := m.lastStatus[clusterName]
	if !ok {
		return false
	}
	return time.Since(st.Timestamp) <= m.heartbeatTTL
}

// ---------------------------------------------------------------------------
// JSON codec for gRPC (replaces proto codec)
// ---------------------------------------------------------------------------

// JSONCodec implements grpc encoding.Codec using JSON.
type JSONCodec struct{}

func (JSONCodec) Marshal(v interface{}) ([]byte, error)      { return json.Marshal(v) }
func (JSONCodec) Unmarshal(data []byte, v interface{}) error { return json.Unmarshal(data, v) }
func (JSONCodec) Name() string                               { return "json" }
