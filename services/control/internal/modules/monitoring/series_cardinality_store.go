package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
)

const maximumCardinalityReplyBytes = 4 << 20

type seriesCardinalityReader struct {
	policy SeriesCardinalityPolicy
	origin *url.URL
	source *SeriesCardinalityQuerySource
}

func newSeriesCardinalityReader(p SeriesCardinalityPolicy, source *SeriesCardinalityQuerySource) *seriesCardinalityReader {
	if p.RawOrigin == "" || source == nil {
		return nil
	}
	return &seriesCardinalityReader{p, metricsQueryOrigin(p.RawOrigin), source}
}

func cardinalitySourceError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("MONITORING_CARDINALITY_UNAVAILABLE")
}

func (r *seriesCardinalityReader) read(ctx context.Context, scope workspaces.Workspace, now time.Time, selection SeriesCardinalityQuery) (SeriesCardinality, error) {
	if ctx == nil || r == nil || r.origin == nil || r.source == nil {
		return SeriesCardinality{}, errors.New("MONITORING_OWNER_REQUIRED")
	}
	today := now.UTC().Truncate(24 * time.Hour)
	earliest := today.AddDate(0, 0, 1-r.policy.LookbackDays)
	if selection.Date.Before(earliest) || selection.Date.After(today) {
		return SeriesCardinality{}, errors.New("MONITORING_CARDINALITY_RANGE_INVALID")
	}
	limit := selection.Limit
	if limit == 0 {
		limit = r.policy.MaximumRanks
	}
	if limit < 1 || limit > r.policy.MaximumRanks {
		return SeriesCardinality{}, errors.New("VALIDATION_FAILED")
	}
	_, ok := ctx.Deadline()
	if !ok {
		return SeriesCardinality{}, errors.New("MONITORING_OWNER_REQUIRED")
	}
	if err := ctx.Err(); err != nil {
		return SeriesCardinality{}, err
	}
	var out SeriesCardinality
	err := r.source.withLease(ctx, func(operation context.Context, lease SeriesCardinalitySourceLease) error {
		deadline, _ := operation.Deadline()
		query := url.Values{
			"date":                  {selection.Date.Format(cardinalityDateFormat)},
			"topN":                  {strconv.Itoa(limit)},
			"focusLabel":            {"node_id"},
			"extra_label":           {"tenant_id=" + scope.TenantID, "workspace_id=" + scope.ID},
			"timeout":               {strconv.FormatInt(max(1, time.Until(deadline).Milliseconds()), 10) + "ms"},
			"deny_partial_response": {"1"},
		}
		if selection.Metric != "" {
			query.Set("match[]", "{__name__="+strconv.Quote(selection.Metric)+"}")
		}
		endpoint := *r.origin
		endpoint.Path, endpoint.RawQuery = "/api/v1/status/tsdb", query.Encode()
		request, err := http.NewRequestWithContext(operation, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return cardinalitySourceError(ctx)
		}
		response, err := lease.RoundTrip(request)
		if err != nil || response == nil || response.Body == nil {
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			return cardinalitySourceError(ctx)
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return cardinalitySourceError(ctx)
		}
		body, readError := io.ReadAll(io.LimitReader(response.Body, maximumCardinalityReplyBytes+1))
		closeError := response.Body.Close()
		if readError != nil || closeError != nil || len(body) > maximumCardinalityReplyBytes || !utf8.Valid(body) {
			return cardinalitySourceError(ctx)
		}
		var reply cardinalityReply
		if json.Unmarshal(body, &reply) != nil || reply.Status != "success" || !reply.complete() || len(reply.Warnings) != 0 || reply.Data == nil {
			return cardinalitySourceError(ctx)
		}
		out, err = reply.project(scope, selection, limit)
		if err != nil {
			return cardinalitySourceError(ctx)
		}
		return nil
	})
	if err != nil {
		return SeriesCardinality{}, err
	}
	return out, nil
}

type cardinalityNativeRank struct {
	Name  *string `json:"name"`
	Value *uint64 `json:"value"`
}

type cardinalityReply struct {
	Status   string          `json:"status"`
	Partial  json.RawMessage `json:"isPartial"`
	Warnings []string        `json:"warnings"`
	Data     *struct {
		Total   *uint64                 `json:"totalSeries"`
		Metrics []cardinalityNativeRank `json:"seriesCountByMetricName"`
		Nodes   []cardinalityNativeRank `json:"seriesCountByFocusLabelValue"`
	} `json:"data"`
}

func (r cardinalityReply) complete() bool {
	return r.Partial == nil || string(r.Partial) == "false"
}

func (r cardinalityReply) project(scope workspaces.Workspace, selection SeriesCardinalityQuery, limit int) (SeriesCardinality, error) {
	invalid := errors.New("invalid cardinality source projection")
	if r.Data == nil || r.Data.Total == nil || *r.Data.Total > maximumCardinalityCount ||
		len(r.Data.Metrics) > limit || len(r.Data.Nodes) > limit {
		return SeriesCardinality{}, invalid
	}
	total := *r.Data.Total
	if total > 0 && len(r.Data.Metrics) == 0 {
		return SeriesCardinality{}, invalid
	}
	out := SeriesCardinality{
		WorkspaceID: scope.ID, Date: selection.Date.Format(cardinalityDateFormat),
		TotalSeries: total, RankLimit: limit,
		Metrics: make([]MetricCardinality, 0, len(r.Data.Metrics)),
		Nodes:   make([]NodeCardinality, 0, len(r.Data.Nodes)),
	}
	seen := make(map[string]bool, len(r.Data.Metrics))
	var sum uint64
	for _, row := range r.Data.Metrics {
		if row.Name == nil || row.Value == nil || *row.Name == "" || len(*row.Name) > maximumCardinalityNameBytes ||
			!utf8.ValidString(*row.Name) || *row.Value == 0 || *row.Value > total-sum || seen[*row.Name] ||
			selection.Metric != "" && *row.Name != selection.Metric {
			return SeriesCardinality{}, invalid
		}
		sum += *row.Value
		seen[*row.Name] = true
		out.Metrics = append(out.Metrics, MetricCardinality{*row.Name, *row.Value})
	}
	if selection.Metric != "" && sum != total {
		return SeriesCardinality{}, invalid
	}
	seen = make(map[string]bool, len(r.Data.Nodes))
	sum = 0
	for _, row := range r.Data.Nodes {
		if row.Name == nil || row.Value == nil || *row.Value == 0 || *row.Value > total-sum || seen[*row.Name] {
			return SeriesCardinality{}, invalid
		}
		if !validID(*row.Name) {
			return SeriesCardinality{}, invalid
		}
		sum += *row.Value
		seen[*row.Name] = true
		out.Nodes = append(out.Nodes, NodeCardinality{*row.Name, *row.Value})
	}
	sort.Slice(out.Metrics, func(i, j int) bool {
		a, b := out.Metrics[i], out.Metrics[j]
		if a.SeriesCount != b.SeriesCount {
			return a.SeriesCount > b.SeriesCount
		}
		return a.MetricName < b.MetricName
	})
	sort.Slice(out.Nodes, func(i, j int) bool {
		a, b := out.Nodes[i], out.Nodes[j]
		if a.SeriesCount != b.SeriesCount {
			return a.SeriesCount > b.SeriesCount
		}
		return a.NodeID < b.NodeID
	})
	return out, nil
}
