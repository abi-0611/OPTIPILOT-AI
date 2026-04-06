package simulator

import (
	"context"
	"errors"
	"time"

	"github.com/optipilot-ai/optipilot/internal/engine"
	"github.com/optipilot-ai/optipilot/internal/explainability"
	"github.com/optipilot-ai/optipilot/internal/metrics"
)

// PrometheusHistoryProvider adapts metrics.PrometheusClient to HistoryProvider.
type PrometheusHistoryProvider struct {
	Client metrics.PrometheusClient
}

// FetchHistory executes a range query and maps points into simulator DataPoint.
// No-data responses are normalized to an empty series so simulations can proceed.
func (p *PrometheusHistoryProvider) FetchHistory(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]DataPoint, error) {
	points, err := p.Client.QueryRange(ctx, query, start, end, step)
	if err != nil {
		if errors.Is(err, metrics.ErrNoData) {
			return []DataPoint{}, nil
		}
		return nil, err
	}

	out := make([]DataPoint, 0, len(points))
	for _, pt := range points {
		out = append(out, DataPoint{
			Timestamp: pt.Timestamp,
			Value:     pt.Value,
		})
	}
	return out, nil
}

// JournalQuerier is the subset of explainability.Journal needed for simulations.
type JournalQuerier interface {
	Query(filter explainability.QueryFilter) ([]engine.DecisionRecord, error)
}

// JournalDecisionProvider adapts explainability.Journal to DecisionProvider.
type JournalDecisionProvider struct {
	Journal JournalQuerier
}

// FetchDecisions returns recorded decisions for requested services and time window.
func (p *JournalDecisionProvider) FetchDecisions(ctx context.Context, services []string, start, end time.Time) ([]HistoricalDecision, error) {
	_ = ctx // journal queries are local sqlite reads
	if len(services) == 0 {
		return []HistoricalDecision{}, nil
	}

	dedup := map[string]HistoricalDecision{}
	for _, svc := range services {
		records, err := p.Journal.Query(explainability.QueryFilter{
			Service: svc,
			Since:   &start,
		})
		if err != nil {
			return nil, err
		}

		for _, r := range records {
			if r.Timestamp.After(end) {
				continue
			}
			dedup[r.ID] = HistoricalDecision{
				ID:          r.ID,
				Timestamp:   r.Timestamp,
				Service:     r.Service,
				Namespace:   r.Namespace,
				Action:      string(r.ActionType),
				Replicas:    r.SelectedAction.TargetReplica,
				CPUCores:    r.SelectedAction.CPURequest,
				HourlyCost:  r.CurrentState.HourlyCost,
				SLOBreached: !r.SLOStatus.Compliant,
			}
		}
	}

	out := make([]HistoricalDecision, 0, len(dedup))
	for _, v := range dedup {
		out = append(out, v)
	}
	return out, nil
}
