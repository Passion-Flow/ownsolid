package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
)

const (
	cardinalityDateFormat       = "2006-01-02"
	maximumCardinalityRanks     = 100
	maximumCardinalityNameBytes = 1024
	maximumCardinalityJSONBytes = 1 << 20
	maximumCardinalityCount     = 1<<53 - 1
)

// SeriesCardinalityPolicy selects a private raw index and bounds its UTC-day view.
type SeriesCardinalityPolicy struct {
	// SourceID references the trusted installed source; a URL alone grants no authority.
	SourceID  string `json:"source_id"`
	RawOrigin string `json:"raw_origin"`
	// MaximumScanSeries must equal the observed finite native scan limit.
	MaximumScanSeries uint64 `json:"maximum_scan_series"`
	// LookbackDays includes today and must fit within the raw retention.
	LookbackDays int `json:"lookback_days"`
	MaximumRanks int `json:"maximum_ranks"`
}

func (p SeriesCardinalityPolicy) valid() bool {
	if p == (SeriesCardinalityPolicy{}) {
		return true
	}
	return validID(p.SourceID) && p.MaximumScanSeries > 0 && p.MaximumScanSeries <= maximumCardinalityCount &&
		metricsQueryOrigin(p.RawOrigin) != nil && p.LookbackDays >= 1 &&
		p.LookbackDays <= 366 && p.MaximumRanks >= 1 && p.MaximumRanks <= maximumCardinalityRanks
}

// SeriesCardinalityQuery selects a UTC day and an optional exact metric.
// A zero Limit uses the configured rank maximum. It accepts no raw selector.
type SeriesCardinalityQuery struct {
	Date   time.Time
	Metric string
	Limit  int
}

// Validate checks intrinsic UTC-day and fixed byte/rank bounds. Empty Metric
// and zero Limit retain their service defaults; current scope and configured
// range/rank policy are checked by the owning service.
func (q SeriesCardinalityQuery) Validate() error {
	if !q.valid() {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}

func (q SeriesCardinalityQuery) valid() bool {
	return q.Date.Location() == time.UTC && q.Date.Unix() >= 0 &&
		q.Date.Equal(q.Date.Truncate(24*time.Hour)) && len(q.Date.Format(cardinalityDateFormat)) == 10 &&
		len(q.Metric) <= maximumCardinalityNameBytes && utf8.ValidString(q.Metric) &&
		q.Limit >= 0 && q.Limit <= maximumCardinalityRanks
}

// SeriesCardinality counts unique series seen on a UTC day, not active agents
// or enforced ingestion budgets. Ranks are bounded views, not exhaustive lists.
type SeriesCardinality struct {
	WorkspaceID string              `json:"workspace_id"`
	Date        string              `json:"date"`
	TotalSeries uint64              `json:"total_series"`
	RankLimit   int                 `json:"rank_limit"`
	Metrics     []MetricCardinality `json:"metrics"`
	Nodes       []NodeCardinality   `json:"nodes"`
}

// MetricCardinality is the scoped daily count of one metric name.
type MetricCardinality struct {
	MetricName  string `json:"metric_name"`
	SeriesCount uint64 `json:"series_count"`
}

// NodeCardinality is a scoped daily count for a trusted canonical node UUID.
// Source rows without node_id never create an unattributed rank.
type NodeCardinality struct {
	NodeID      string `json:"node_id"`
	SeriesCount uint64 `json:"series_count"`
}

// GetSeriesCardinality applies current Admin diagnostics authority and a
// committed request audit to a bounded raw-index read.
func (s *Service) GetSeriesCardinality(ctx context.Context, a identity.Auth, workspace string, query SeriesCardinalityQuery, requestID string) (SeriesCardinality, error) {
	return s.getSeriesCardinality(ctx, a, workspace, query, requestID, nil)
}

// GetSeriesCardinalityJSON prepares the complete bounded response before the
// success audit commits. Failures and lost commit outcomes release no body.
func (s *Service) GetSeriesCardinalityJSON(ctx context.Context, a identity.Auth, workspace string, query SeriesCardinalityQuery, requestID string) (json.RawMessage, error) {
	var body json.RawMessage
	_, err := s.getSeriesCardinality(ctx, a, workspace, query, requestID, func(out SeriesCardinality) error {
		var err error
		body, err = encodeMetricsJSON(out, maximumCardinalityJSONBytes, "MONITORING_CARDINALITY_RESPONSE_LIMIT")
		return err
	})
	if err != nil {
		return nil, err
	}
	return body, nil
}

func (s *Service) getSeriesCardinality(ctx context.Context, a identity.Auth, workspace string, query SeriesCardinalityQuery, requestID string, prepare func(SeriesCardinality) error) (SeriesCardinality, error) {
	var out SeriesCardinality
	err := s.observeMetrics(ctx, a, workspace, requestID, metricsObservation{
		validate: query.Validate,
		read: func(ctx context.Context, scope workspaces.Workspace, now time.Time) error {
			if s.cardinality == nil {
				return errors.New("MONITORING_CARDINALITY_NOT_CONFIGURED")
			}
			var err error
			out, err = s.cardinality.read(ctx, scope, now, query)
			return err
		},
		prepare: func() error {
			if prepare != nil {
				return prepare(out)
			}
			return nil
		},
	})
	if err != nil {
		return SeriesCardinality{}, err
	}
	return out, nil
}
