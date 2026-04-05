package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// ErrNoData is returned when a Prometheus query returns an empty result set.
var ErrNoData = errors.New("prometheus query returned no data")

// ErrMultipleResults is returned when an instant query returns more than one series.
var ErrMultipleResults = errors.New("prometheus query returned multiple results, expected scalar")

// PrometheusClient provides read access to a Prometheus instance.
type PrometheusClient interface {
	// Query executes an instant query and returns the scalar result.
	Query(ctx context.Context, query string) (float64, error)

	// QueryRange executes a range query and returns ordered time-series data.
	QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]DataPoint, error)

	// Healthy checks if Prometheus is reachable.
	Healthy(ctx context.Context) error
}

// DataPoint represents a single (timestamp, value) sample.
type DataPoint struct {
	Timestamp time.Time
	Value     float64
}

// cacheEntry holds a cached query result.
type cacheEntry struct {
	value     float64
	expiresAt time.Time
}

// HTTPPrometheusClient implements PrometheusClient against the Prometheus HTTP API.
type HTTPPrometheusClient struct {
	baseURL    string
	httpClient *http.Client
	ttl        time.Duration

	mu    sync.Mutex
	cache map[string]cacheEntry
}

// NewHTTPPrometheusClient creates a new client.
// baseURL should be e.g. "http://prometheus:9090".
// timeout is the per-request HTTP timeout (0 uses 10s default).
// cacheTTL is how long instant-query results are cached (0 uses 5s default).
func NewHTTPPrometheusClient(baseURL string, timeout, cacheTTL time.Duration) *HTTPPrometheusClient {
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	if cacheTTL == 0 {
		cacheTTL = 5 * time.Second
	}
	return &HTTPPrometheusClient{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: timeout},
		ttl:        cacheTTL,
		cache:      make(map[string]cacheEntry),
	}
}

// Query executes an instant PromQL query. Results are cached for the configured TTL.
func (c *HTTPPrometheusClient) Query(ctx context.Context, query string) (float64, error) {
	c.mu.Lock()
	if e, ok := c.cache[query]; ok && time.Now().Before(e.expiresAt) {
		val := e.value
		c.mu.Unlock()
		return val, nil
	}
	c.mu.Unlock()

	params := url.Values{}
	params.Set("query", query)
	reqURL := fmt.Sprintf("%s/api/v1/query?%s", c.baseURL, params.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, fmt.Errorf("building prometheus request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("querying prometheus: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, fmt.Errorf("prometheus returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	val, err := c.parseInstantResponse(resp.Body)
	if err != nil {
		return 0, err
	}

	c.mu.Lock()
	c.cache[query] = cacheEntry{value: val, expiresAt: time.Now().Add(c.ttl)}
	c.mu.Unlock()

	return val, nil
}

// QueryRange executes a range PromQL query and returns all data points.
func (c *HTTPPrometheusClient) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]DataPoint, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", formatTimestamp(start))
	params.Set("end", formatTimestamp(end))
	params.Set("step", fmt.Sprintf("%.0fs", step.Seconds()))
	reqURL := fmt.Sprintf("%s/api/v1/query_range?%s", c.baseURL, params.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building prometheus range request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying prometheus range: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("prometheus returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	return c.parseRangeResponse(resp.Body)
}

// Healthy checks if Prometheus' /-/healthy endpoint responds with 200.
func (c *HTTPPrometheusClient) Healthy(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/-/healthy", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("prometheus unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("prometheus health check returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// --- internal parsing ---

// prometheusResponse is the envelope for both instant and range queries.
type prometheusResponse struct {
	Status string              `json:"status"`
	Data   prometheusQueryData `json:"data"`
	Error  string              `json:"error"`
}

type prometheusQueryData struct {
	ResultType string            `json:"resultType"`
	Result     []json.RawMessage `json:"result"`
}

type vectorSample struct {
	Metric map[string]string  `json:"metric"`
	Value  [2]json.RawMessage `json:"value"` // [timestamp, value_string]
}

type matrixSample struct {
	Metric map[string]string    `json:"metric"`
	Values [][2]json.RawMessage `json:"values"` // [[timestamp, value_string], ...]
}

func (c *HTTPPrometheusClient) parseInstantResponse(body io.Reader) (float64, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return 0, fmt.Errorf("reading prometheus response: %w", err)
	}

	var pr prometheusResponse
	if err := json.Unmarshal(data, &pr); err != nil {
		return 0, fmt.Errorf("parsing prometheus response JSON: %w", err)
	}
	if pr.Status != "success" {
		return 0, fmt.Errorf("prometheus query failed: %s", pr.Error)
	}

	switch pr.Data.ResultType {
	case "scalar":
		// scalar: [timestamp, value]
		if len(pr.Data.Result) == 0 {
			return 0, ErrNoData
		}
		return 0, fmt.Errorf("unexpected scalar format")

	case "vector":
		switch len(pr.Data.Result) {
		case 0:
			return 0, ErrNoData
		case 1:
			var s vectorSample
			if err := json.Unmarshal(pr.Data.Result[0], &s); err != nil {
				return 0, fmt.Errorf("parsing vector sample: %w", err)
			}
			return parseValueString(s.Value[1])
		default:
			return 0, ErrMultipleResults
		}

	default:
		return 0, fmt.Errorf("unexpected resultType %q for instant query", pr.Data.ResultType)
	}
}

func (c *HTTPPrometheusClient) parseRangeResponse(body io.Reader) ([]DataPoint, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("reading prometheus range response: %w", err)
	}

	var pr prometheusResponse
	if err := json.Unmarshal(data, &pr); err != nil {
		return nil, fmt.Errorf("parsing prometheus range response JSON: %w", err)
	}
	if pr.Status != "success" {
		return nil, fmt.Errorf("prometheus range query failed: %s", pr.Error)
	}

	if len(pr.Data.Result) == 0 {
		return nil, ErrNoData
	}

	// Use the first series only.
	var ms matrixSample
	if err := json.Unmarshal(pr.Data.Result[0], &ms); err != nil {
		return nil, fmt.Errorf("parsing matrix sample: %w", err)
	}

	points := make([]DataPoint, 0, len(ms.Values))
	for _, pair := range ms.Values {
		var tsFloat float64
		if err := json.Unmarshal(pair[0], &tsFloat); err != nil {
			return nil, fmt.Errorf("parsing range timestamp: %w", err)
		}
		val, err := parseValueString(pair[1])
		if err != nil {
			return nil, err
		}
		points = append(points, DataPoint{
			Timestamp: time.Unix(int64(tsFloat), 0),
			Value:     val,
		})
	}
	return points, nil
}

// parseValueString unmarshals a JSON-encoded Prometheus value string (e.g. "0.9995").
func parseValueString(raw json.RawMessage) (float64, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, fmt.Errorf("parsing value string: %w", err)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("converting %q to float: %w", s, err)
	}
	return f, nil
}

func formatTimestamp(t time.Time) string {
	return strconv.FormatFloat(float64(t.UnixNano())/1e9, 'f', 3, 64)
}
