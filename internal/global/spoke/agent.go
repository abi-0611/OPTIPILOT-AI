package spoke

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"google.golang.org/grpc/credentials"

	hubgrpc "github.com/optipilot-ai/optipilot/internal/global/grpc"
)

// AgentState represents the lifecycle state of the spoke agent.
type AgentState string

const (
	StateDisconnected AgentState = "disconnected"
	StateRegistered   AgentState = "registered"
	StateRunning      AgentState = "running"
	StateStopped      AgentState = "stopped"
)

// AgentOption configures a SpokeAgent.
type AgentOption func(*SpokeAgent)

// WithHeartbeatInterval overrides the default 30-second heartbeat interval.
func WithHeartbeatInterval(d time.Duration) AgentOption {
	return func(a *SpokeAgent) { a.heartbeatInterval = d }
}

// WithDirectivePollInterval overrides the default 10-second directive poll interval.
func WithDirectivePollInterval(d time.Duration) AgentOption {
	return func(a *SpokeAgent) { a.directivePollInterval = d }
}

// WithTLSCredentials sets mTLS transport credentials for the hub connection.
func WithTLSCredentials(creds credentials.TransportCredentials) AgentOption {
	return func(a *SpokeAgent) { a.tlsCreds = creds }
}

// WithLogger sets a logger for the agent.
func WithLogger(l logr.Logger) AgentOption {
	return func(a *SpokeAgent) { a.log = l }
}

// WithNowFunc injects a clock for testing.
func WithNowFunc(fn func() time.Time) AgentOption {
	return func(a *SpokeAgent) { a.nowFn = fn }
}

// SpokeAgent manages the lifecycle of a spoke cluster's connection to the hub:
// registration, periodic heartbeats, and directive polling.
// It implements controller-runtime's manager.Runnable interface via Start(ctx).
type SpokeAgent struct {
	hubAddr  string
	info     RegistrationInfo
	collect  StatusCollector
	handler  DirectiveHandler
	tlsCreds credentials.TransportCredentials
	log      logr.Logger

	heartbeatInterval     time.Duration
	directivePollInterval time.Duration
	nowFn                 func() time.Time

	mu             sync.RWMutex
	state          AgentState
	lastHeartbeat  time.Time
	heartbeatCount int64
	directiveCount int64
	lastErr        error
	client         *hubgrpc.SpokeClient
}

// NewSpokeAgent creates a new spoke agent.
func NewSpokeAgent(hubAddr string, info RegistrationInfo, collector StatusCollector, handler DirectiveHandler, opts ...AgentOption) *SpokeAgent {
	a := &SpokeAgent{
		hubAddr:               hubAddr,
		info:                  info,
		collect:               collector,
		handler:               handler,
		heartbeatInterval:     30 * time.Second,
		directivePollInterval: 10 * time.Second,
		state:                 StateDisconnected,
		nowFn:                 time.Now,
		log:                   logr.Discard(),
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

// Start connects to the hub, registers, and runs heartbeat + directive loops
// until ctx is cancelled. It implements manager.Runnable.
func (a *SpokeAgent) Start(ctx context.Context) error {
	client, err := hubgrpc.NewSpokeClient(a.hubAddr, a.tlsCreds)
	if err != nil {
		a.setError(err)
		return fmt.Errorf("spoke: connect to hub %s: %w", a.hubAddr, err)
	}
	a.mu.Lock()
	a.client = client
	a.mu.Unlock()

	defer func() {
		client.Close()
		a.setState(StateStopped)
	}()

	// Register with hub.
	if err := a.register(ctx); err != nil {
		return fmt.Errorf("spoke: register: %w", err)
	}
	a.setState(StateRegistered)
	a.log.Info("registered with hub", "cluster", a.info.ClusterName, "hub", a.hubAddr)

	a.setState(StateRunning)

	// Run heartbeat and directive poll loops concurrently.
	errCh := make(chan error, 2)
	go func() { errCh <- a.heartbeatLoop(ctx) }()
	go func() { errCh <- a.directiveLoop(ctx) }()

	// Wait for context cancellation or a fatal error.
	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

// register sends a RegisterCluster RPC to the hub.
func (a *SpokeAgent) register(ctx context.Context) error {
	req := &hubgrpc.RegisterClusterRequest{
		ClusterName:         a.info.ClusterName,
		Provider:            a.info.Provider,
		Region:              a.info.Region,
		Endpoint:            a.info.Endpoint,
		CarbonIntensityGCO2: a.info.CarbonIntensityGCO2,
		Labels:              a.info.Labels,
		Capabilities:        a.info.Capabilities,
		CostProfile:         a.info.CostProfile,
	}
	resp, err := a.client.RegisterCluster(ctx, req)
	if err != nil {
		a.setError(err)
		return err
	}
	if !resp.Accepted {
		err := fmt.Errorf("hub rejected registration: %s", resp.Message)
		a.setError(err)
		return err
	}
	// If hub specifies a heartbeat interval, honour it.
	if resp.HeartbeatIntervalS > 0 {
		a.mu.Lock()
		a.heartbeatInterval = time.Duration(resp.HeartbeatIntervalS) * time.Second
		a.mu.Unlock()
	}
	return nil
}

// heartbeatLoop sends periodic status reports to the hub.
func (a *SpokeAgent) heartbeatLoop(ctx context.Context) error {
	// Send an initial heartbeat immediately after registration.
	if err := a.sendHeartbeat(ctx); err != nil {
		a.log.Error(err, "initial heartbeat failed")
		// Don't return — keep trying on the ticker.
	}

	a.mu.RLock()
	interval := a.heartbeatInterval
	a.mu.RUnlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := a.sendHeartbeat(ctx); err != nil {
				a.log.Error(err, "heartbeat failed")
				a.setError(err)
				// Continue — transient network errors should not kill the agent.
			}
		}
	}
}

// sendHeartbeat collects status and sends it to the hub.
func (a *SpokeAgent) sendHeartbeat(ctx context.Context) error {
	report, err := a.collect.Collect(ctx)
	if err != nil {
		return fmt.Errorf("collect status: %w", err)
	}
	report.ClusterName = a.info.ClusterName
	if report.Timestamp.IsZero() {
		report.Timestamp = a.nowFn()
	}

	ack, err := a.client.ReportStatus(ctx, report)
	if err != nil {
		return fmt.Errorf("report status: %w", err)
	}
	if !ack.Received {
		return fmt.Errorf("hub did not acknowledge heartbeat: %s", ack.Message)
	}

	a.mu.Lock()
	a.lastHeartbeat = a.nowFn()
	a.heartbeatCount++
	a.lastErr = nil
	a.mu.Unlock()

	return nil
}

// directiveLoop periodically polls the hub for pending directives.
func (a *SpokeAgent) directiveLoop(ctx context.Context) error {
	ticker := time.NewTicker(a.directivePollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := a.pollDirectives(ctx); err != nil {
				a.log.Error(err, "directive poll failed")
				a.setError(err)
			}
		}
	}
}

// pollDirectives fetches and processes directives from the hub.
func (a *SpokeAgent) pollDirectives(ctx context.Context) error {
	resp, err := a.client.GetDirective(ctx, a.info.ClusterName)
	if err != nil {
		return fmt.Errorf("get directives: %w", err)
	}
	for _, d := range resp.Directives {
		a.log.Info("received directive", "id", d.ID, "type", d.Type, "cluster", d.ClusterName)
		if err := a.handler.Handle(ctx, d); err != nil {
			a.log.Error(err, "directive handler failed", "id", d.ID)
			// Continue processing remaining directives.
		}
		a.mu.Lock()
		a.directiveCount++
		a.mu.Unlock()
	}
	return nil
}

// ---------------------------------------------------------------------------
// State accessors (thread-safe)
// ---------------------------------------------------------------------------

func (a *SpokeAgent) setState(s AgentState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = s
}

func (a *SpokeAgent) setError(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastErr = err
}

// State returns the current agent state.
func (a *SpokeAgent) State() AgentState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state
}

// LastHeartbeat returns the time of the last successful heartbeat.
func (a *SpokeAgent) LastHeartbeat() time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastHeartbeat
}

// HeartbeatCount returns the total number of successful heartbeats sent.
func (a *SpokeAgent) HeartbeatCount() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.heartbeatCount
}

// DirectiveCount returns the total number of directives processed.
func (a *SpokeAgent) DirectiveCount() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.directiveCount
}

// LastError returns the most recent error, or nil if the last operation succeeded.
func (a *SpokeAgent) LastError() error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastErr
}

// HeartbeatInterval returns the current heartbeat interval
// (may be updated by the hub during registration).
func (a *SpokeAgent) HeartbeatInterval() time.Duration {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.heartbeatInterval
}
