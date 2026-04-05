// Package forecaster provides a Go HTTP client for the OptiPilot ML service.
//
// The client integrates with the solver by returning *cel.ForecastResult and
// *SpotRiskResult.  When the ML service is unavailable the client returns nil,
// and the solver falls back to reactive-only mode (no pre-warming candidates).
//
// Circuit breaker: 5 consecutive errors within 1 minute → 30-second pause.
// Retry: 1 retry with 200 ms backoff on HTTP 5xx or network error.
package forecaster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/optipilot-ai/optipilot/internal/cel"
)

// ── request / response types matching the Python ML service schema ────────────

// MetricPoint is a single (timestamp, value) observation.
type MetricPoint struct {
	DS string  `json:"ds"` // ISO-8601 UTC
	Y  float64 `json:"y"`
}

// forecastRequest mirrors DemandForecastRequest in the Python service.
type forecastRequest struct {
	Service         string        `json:"service"`
	Metric          string        `json:"metric"`
	History         []MetricPoint `json:"history"`
	HorizonsMinutes []int         `json:"horizons_minutes"`
}

// ForecastPoint is a single horizon step from the ML service.
type ForecastPoint struct {
	DS   string  `json:"ds"`
	YHat float64 `json:"yhat"`
}

// forecastResponse mirrors DemandForecastResponse.
type forecastResponse struct {
	Service       string          `json:"service"`
	Metric        string          `json:"metric"`
	Forecasts     []ForecastPoint `json:"forecasts"`
	ModelUsed     string          `json:"model_used"`
	ChangePercent float64         `json:"change_percent"`
	Confidence    float64         `json:"confidence"`
}

// spotRiskRequest mirrors SpotRiskRequest.
type spotRiskRequest struct {
	InstanceType              string  `json:"instance_type"`
	AZ                        string  `json:"az"`
	HourOfDay                 int     `json:"hour_of_day"`
	DayOfWeek                 int     `json:"day_of_week"`
	RecentInterruptionCount7d int     `json:"recent_interruption_count_7d"`
	SpotPriceRatio            float64 `json:"spot_price_ratio"`
}

// SpotRiskResult is the structured result of a spot risk prediction.
type SpotRiskResult struct {
	InstanceType            string  `json:"instance_type"`
	AZ                      string  `json:"az"`
	InterruptionProbability float64 `json:"interruption_probability"`
	RecommendedAction       string  `json:"recommended_action"` // keep | migrate | switch_to_od
	Confidence              float64 `json:"confidence"`
}

// ── circuit breaker ───────────────────────────────────────────────────────────

const (
	cbMaxErrors      = 5
	cbWindowDuration = 1 * time.Minute
	cbPauseDuration  = 30 * time.Second
)

type circuitBreaker struct {
	mu        sync.Mutex
	errors    []time.Time // timestamps of recent errors
	openUntil time.Time   // zero = closed
	nowFn     func() time.Time
}

func newCircuitBreaker() *circuitBreaker {
	return &circuitBreaker{nowFn: time.Now}
}

func (cb *circuitBreaker) isOpen() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	now := cb.nowFn()
	if !cb.openUntil.IsZero() && now.Before(cb.openUntil) {
		return true
	}
	cb.openUntil = time.Time{} // reset once pause expires
	return false
}

func (cb *circuitBreaker) recordError() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	now := cb.nowFn()

	// Evict errors older than the window
	cutoff := now.Add(-cbWindowDuration)
	kept := cb.errors[:0]
	for _, t := range cb.errors {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	cb.errors = append(kept, now)

	if len(cb.errors) >= cbMaxErrors {
		cb.openUntil = now.Add(cbPauseDuration)
		cb.errors = nil // reset counter after tripping
	}
}

func (cb *circuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.errors = nil
	cb.openUntil = time.Time{}
}

// ErrorCount returns the number of recent errors (for tests).
func (cb *circuitBreaker) ErrorCount() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return len(cb.errors)
}

// ── client ────────────────────────────────────────────────────────────────────

const (
	defaultTimeout    = 10 * time.Second
	retryBackoff      = 200 * time.Millisecond
	defaultHorizon15m = 15
)

// Client calls the OptiPilot ML service.
type Client struct {
	baseURL    string
	httpClient *http.Client
	cb         *circuitBreaker
	sleepFn    func(time.Duration) // injectable for tests
}

// ClientOption configures the Client.
type ClientOption func(*Client)

// WithTimeout overrides the default 10-second HTTP timeout.
func WithTimeout(d time.Duration) ClientOption {
	return func(c *Client) { c.httpClient.Timeout = d }
}

// NewClient creates a Client pointing at baseURL (e.g. "http://optipilot-ml:8080").
func NewClient(baseURL string, opts ...ClientOption) *Client {
	c := &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: defaultTimeout},
		cb:         newCircuitBreaker(),
		sleepFn:    time.Sleep,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// SetNowFn injects a clock function into the circuit breaker (for tests).
func (c *Client) SetNowFn(fn func() time.Time) {
	c.cb.mu.Lock()
	defer c.cb.mu.Unlock()
	c.cb.nowFn = fn
}

// SetSleepFn replaces the sleep function (for tests — instant backoff).
func (c *Client) SetSleepFn(fn func(time.Duration)) {
	c.sleepFn = fn
}

// CircuitOpen reports whether the circuit breaker is currently open.
func (c *Client) CircuitOpen() bool {
	return c.cb.isOpen()
}

// ── ForecastDemand ────────────────────────────────────────────────────────────

// ForecastDemand calls POST /v1/forecast and converts the response to a
// *cel.ForecastResult usable by the solver.
//
// Returns nil (no error) when the circuit breaker is open — the solver should
// fall back to reactive-only mode in that case.
func (c *Client) ForecastDemand(
	ctx context.Context,
	service, metric string,
	history []MetricPoint,
) (*cel.ForecastResult, error) {
	if c.cb.isOpen() {
		return nil, nil // circuit open → reactive fallback
	}

	req := forecastRequest{
		Service:         service,
		Metric:          metric,
		History:         history,
		HorizonsMinutes: []int{defaultHorizon15m, 60, 360},
	}

	var resp forecastResponse
	err := c.doWithRetry(ctx, http.MethodPost, "/v1/forecast", req, &resp)
	if err != nil {
		c.cb.recordError()
		return nil, err
	}
	c.cb.recordSuccess()

	// Map the 15-min yhat to PredictedRPS (metric-agnostic best effort)
	var yhat15m float64
	if len(resp.Forecasts) > 0 {
		yhat15m = resp.Forecasts[0].YHat
	}

	return &cel.ForecastResult{
		PredictedRPS:  yhat15m,
		ChangePercent: resp.ChangePercent,
		Confidence:    resp.Confidence,
	}, nil
}

// ── PredictSpotRisk ───────────────────────────────────────────────────────────

// PredictSpotRisk calls POST /v1/spot-risk and returns a *SpotRiskResult.
//
// Returns nil (no error) when the circuit breaker is open.
func (c *Client) PredictSpotRisk(
	ctx context.Context,
	instanceType, az string,
) (*SpotRiskResult, error) {
	if c.cb.isOpen() {
		return nil, nil
	}

	now := time.Now().UTC()
	req := spotRiskRequest{
		InstanceType:   instanceType,
		AZ:             az,
		HourOfDay:      now.Hour(),
		DayOfWeek:      int(now.Weekday()+6) % 7, // Mon=0 … Sun=6 matching Python schema
		SpotPriceRatio: 1.0,
	}

	var resp SpotRiskResult
	err := c.doWithRetry(ctx, http.MethodPost, "/v1/spot-risk", req, &resp)
	if err != nil {
		c.cb.recordError()
		return nil, err
	}
	c.cb.recordSuccess()

	return &resp, nil
}

// ── HTTP plumbing ─────────────────────────────────────────────────────────────

// doWithRetry performs the HTTP request with one retry on 5xx / network error.
func (c *Client) doWithRetry(
	ctx context.Context,
	method, path string,
	body, out interface{},
) error {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			c.sleepFn(retryBackoff)
		}
		lastErr = c.do(ctx, method, path, body, out)
		if lastErr == nil {
			return nil
		}
		// Only retry on retriable errors
		if !isRetriable(lastErr) {
			return lastErr
		}
	}
	return lastErr
}

func (c *Client) do(
	ctx context.Context,
	method, path string,
	body, out interface{},
) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &retriableError{err}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 500 {
		return &retriableError{fmt.Errorf("ML service HTTP %d: %s", resp.StatusCode, respBody)}
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("ML service HTTP %d: %s", resp.StatusCode, respBody)
	}

	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	return nil
}

// ── retriable error ───────────────────────────────────────────────────────────

type retriableError struct{ cause error }

func (e *retriableError) Error() string { return e.cause.Error() }
func (e *retriableError) Unwrap() error { return e.cause }

func isRetriable(err error) bool {
	if err == nil {
		return false
	}
	var re *retriableError
	return asRetriable(err, &re)
}

// asRetriable walks the error chain for *retriableError.
func asRetriable(err error, target **retriableError) bool {
	for err != nil {
		if r, ok := err.(*retriableError); ok {
			*target = r
			return true
		}
		err = unwrap(err)
	}
	return false
}

func unwrap(err error) error {
	type unwrapper interface{ Unwrap() error }
	if u, ok := err.(unwrapper); ok {
		return u.Unwrap()
	}
	return nil
}
