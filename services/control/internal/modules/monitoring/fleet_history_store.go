package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
)

const (
	maxFleetRangeSeconds = 366 * 24 * 60 * 60
	maxFleetPoints       = 1440
	maxFleetReplyBytes   = 4 << 20
	rawWindowSeconds     = 45
)

// FleetHistoryPolicy configures private query origins and bounded history.
// RawHorizonSeconds must fit within the installed raw retention.
type FleetHistoryPolicy struct {
	RawOrigin         string `json:"raw_origin"`
	LongOrigin        string `json:"long_origin"`
	RawHorizonSeconds int64  `json:"raw_horizon_seconds"`
	MaxRangeSeconds   int64  `json:"max_range_seconds"`
	SettlingSeconds   int64  `json:"settling_seconds"`
}

func (p FleetHistoryPolicy) valid() bool {
	if p == (FleetHistoryPolicy{}) {
		return true
	}
	raw, long := historyOrigin(p.RawOrigin), historyOrigin(p.LongOrigin)
	return raw != nil && (p.LongOrigin == "" || long != nil && historyAuthority(raw) != historyAuthority(long)) &&
		p.RawHorizonSeconds > rawWindowSeconds+p.SettlingSeconds && p.RawHorizonSeconds <= maxFleetRangeSeconds &&
		p.MaxRangeSeconds >= 60 && p.MaxRangeSeconds <= maxFleetRangeSeconds &&
		p.SettlingSeconds >= 30 && p.SettlingSeconds <= 300
}

func historyOrigin(raw string) *url.URL {
	u, e := url.Parse(raw)
	if e != nil || u.Hostname() == "" || u.Opaque != "" || u.User != nil || u.Path != "" || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" || u.String() != raw {
		return nil
	}
	if strings.HasSuffix(u.Host, ":") {
		return nil
	}
	if port := u.Port(); port != "" {
		value, e := strconv.ParseUint(port, 10, 16)
		if e != nil || value == 0 || strconv.FormatUint(value, 10) != port {
			return nil
		}
	}
	if u.Scheme == "https" || u.Scheme == "http" && net.ParseIP(u.Hostname()).IsLoopback() {
		return u
	}
	return nil
}

func historyAuthority(u *url.URL) string {
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(host, port)
}

type fleetHistoryReader struct {
	policy    FleetHistoryPolicy
	raw, long *url.URL
	client    *http.Client
}

func newFleetHistoryReader(p FleetHistoryPolicy) *fleetHistoryReader {
	if p.RawOrigin == "" {
		return nil
	}
	// Use the system trust store and direct private routes. An environment proxy
	// or redirect must never receive the scoped query.
	transport := &http.Transport{IdleConnTimeout: 30 * time.Second, ForceAttemptHTTP2: true}
	return &fleetHistoryReader{p, historyOrigin(p.RawOrigin), historyOrigin(p.LongOrigin), &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type historyTier struct {
	name                string
	origin              *url.URL
	cadence             int64
	observed, reachable string
}

func (r *fleetHistoryReader) read(ctx context.Context, scope workspaces.Workspace, now, from, to time.Time) (FleetHistory, error) {
	if to.After(now) || to.Sub(from) > time.Duration(r.policy.MaxRangeSeconds)*time.Second {
		return FleetHistory{}, errors.New("MONITORING_HISTORY_RANGE_INVALID")
	}
	const source = `last_over_time(up{job="ownsolid-node",site_id!="",cell_id!="",node_id!=""}[45s])`
	const grouping = " by (tenant_id, workspace_id, site_id, cell_id) "
	tier := historyTier{"raw", r.raw, 15, "count" + grouping + "(" + source + ")", "sum" + grouping + "(" + source + " == bool 1)"}
	if from.Add(-rawWindowSeconds * time.Second).Before(now.Add(-time.Duration(r.policy.RawHorizonSeconds) * time.Second)) {
		tier = historyTier{"rollup", r.long, 60,
			"last_over_time(ownsolid:workspace_cell_nodes_observed:count[1m])",
			"last_over_time(ownsolid:workspace_cell_nodes_reachable:count[1m])",
		}
		if tier.origin == nil {
			return FleetHistory{}, errors.New("MONITORING_LONG_TIER_NOT_CONFIGURED")
		}
	}
	// Widen the evaluation grid for longer requests while looking only for a
	// fresh source sample at each grid point.
	start, end := from.Unix(), min(to.Unix(), now.Unix()-r.policy.SettlingSeconds)
	if start > end {
		return FleetHistory{}, errors.New("MONITORING_HISTORY_RANGE_INVALID")
	}
	minimumStep := (end-start)/(maxFleetPoints-1) + 1
	step := max(tier.cadence, (minimumStep+tier.cadence-1)/tier.cadence*tier.cadence)
	start, end = (start+step-1)/step*step, end/step*step
	if start > end {
		return FleetHistory{}, errors.New("MONITORING_HISTORY_RANGE_INVALID")
	}
	cells, e := r.query(ctx, scope, tier, start, end, step)
	if e != nil {
		return FleetHistory{}, e
	}
	return FleetHistory{Tier: tier.name, StepSeconds: step, From: time.Unix(start, 0).UTC(), To: time.Unix(end, 0).UTC(), Cells: cells}, nil
}

type historyReply struct {
	Status   string   `json:"status"`
	Partial  bool     `json:"isPartial"`
	Warnings []string `json:"warnings"`
	Data     struct {
		Type   string `json:"resultType"`
		Result []struct {
			Labels map[string]string   `json:"metric"`
			Points [][]json.RawMessage `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

func (r *fleetHistoryReader) query(ctx context.Context, scope workspaces.Workspace, tier historyTier, start, end, step int64) ([]FleetHistoryCell, error) {
	unavailable := errors.New("MONITORING_RAW_TIER_UNAVAILABLE")
	if tier.name == "rollup" {
		unavailable = errors.New("MONITORING_LONG_TIER_UNAVAILABLE")
	}
	expr := `label_set(label_del(` + tier.observed + `, "__name__"), "measure", "observed") or label_set(label_del(` + tier.reachable + `, "__name__"), "measure", "reachable")`
	query := url.Values{
		"query": {expr}, "start": {strconv.FormatInt(start, 10)}, "end": {strconv.FormatInt(end, 10)},
		"step": {strconv.FormatInt(step, 10)}, "deny_partial_response": {"1"},
		// The service already cuts unsettled points. Disable the store's fill-forward adjustment.
		"latency_offset": {"1ms"},
		"extra_label":    {"tenant_id=" + scope.TenantID, "workspace_id=" + scope.ID},
	}
	endpoint := *tier.origin
	endpoint.Path, endpoint.RawQuery = "/api/v1/query_range", query.Encode()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if e != nil {
		return nil, unavailable
	}
	res, e := r.client.Do(req)
	if e != nil {
		return nil, unavailable
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, unavailable
	}
	body, e := io.ReadAll(io.LimitReader(res.Body, maxFleetReplyBytes+1))
	if e != nil || len(body) > maxFleetReplyBytes {
		return nil, unavailable
	}
	var reply historyReply
	if json.Unmarshal(body, &reply) != nil || reply.Status != "success" || reply.Partial ||
		len(reply.Warnings) > 0 || reply.Data.Type != "matrix" || reply.Data.Result == nil {
		return nil, unavailable
	}
	cells, e := reply.cells(scope, start, end, step)
	if e != nil {
		return nil, unavailable
	}
	return cells, nil
}

func (r historyReply) cells(scope workspaces.Workspace, start, end, step int64) ([]FleetHistoryCell, error) {
	invalid := errors.New("invalid history series")
	type cellKey struct{ site, cell string }
	type sample struct {
		stamp int64
		count uint64
	}
	type pair struct {
		observed, reachable []sample
	}
	pairs := make(map[cellKey]*pair)
	for _, row := range r.Data.Result {
		site, cell := row.Labels["site_id"], row.Labels["cell_id"]
		if len(row.Labels) != 5 || site == "" || cell == "" || len(site) > 128 || len(cell) > 128 ||
			row.Labels["tenant_id"] != scope.TenantID || row.Labels["workspace_id"] != scope.ID ||
			len(row.Points) == 0 || len(row.Points) > maxFleetPoints {
			return nil, invalid
		}
		key := cellKey{site, cell}
		counts := pairs[key]
		if counts == nil {
			counts = &pair{}
			pairs[key] = counts
		}
		var series *[]sample
		switch row.Labels["measure"] {
		case "observed":
			series = &counts.observed
		case "reachable":
			series = &counts.reachable
		default:
			return nil, invalid
		}
		if *series != nil {
			return nil, invalid
		}
		*series = make([]sample, 0, len(row.Points))
		previous := start - 1
		for _, item := range row.Points {
			if len(item) != 2 {
				return nil, invalid
			}
			stamp, e := strconv.ParseInt(string(item[0]), 10, 64)
			var value string
			if e != nil || json.Unmarshal(item[1], &value) != nil || stamp < start || stamp > end ||
				(stamp-start)%step != 0 || stamp <= previous {
				return nil, invalid
			}
			count, e := strconv.ParseUint(value, 10, 64)
			if e != nil {
				return nil, invalid
			}
			previous = stamp
			*series = append(*series, sample{stamp, count})
		}
	}
	out := make([]FleetHistoryCell, 0, len(pairs))
	for key, counts := range pairs {
		if len(counts.observed) != len(counts.reachable) {
			return nil, invalid
		}
		points := make([]FleetHistoryPoint, len(counts.observed))
		for i, observed := range counts.observed {
			reachable := counts.reachable[i]
			if observed.stamp != reachable.stamp || observed.count == 0 || reachable.count > observed.count {
				return nil, invalid
			}
			points[i] = FleetHistoryPoint{At: time.Unix(observed.stamp, 0).UTC(), Observed: observed.count, Reachable: reachable.count}
		}
		out = append(out, FleetHistoryCell{SiteID: key.site, CellID: key.cell, Points: points})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SiteID != out[j].SiteID {
			return out[i].SiteID < out[j].SiteID
		}
		return out[i].CellID < out[j].CellID
	})
	return out, nil
}
