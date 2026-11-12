package monitoring

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
)

func TestSeriesCardinalityConfiguration(t *testing.T) {
	const valid = `{"series_cardinality":{"raw_origin":"https://metrics.example.invalid","lookback_days":7,"maximum_ranks":20}}`
	for _, tc := range []struct {
		input string
		valid bool
	}{
		{`{}`, true}, {valid, true},
		{`{"series_cardinality":{}}`, true},
		{`{"series_cardinality":null}`, false},
		{strings.Replace(valid, `"lookback_days":7`, `"lookback_days":null`, 1), false},
		{strings.Replace(valid, `"lookback_days":7`, `"lookback_days":0`, 1), false},
		{strings.Replace(valid, `"lookback_days":7`, `"lookback_days":367`, 1), false},
		{strings.Replace(valid, `"maximum_ranks":20`, `"maximum_ranks":101`, 1), false},
		{strings.Replace(valid, `"maximum_ranks":20`, `"maximum_ranks":0`, 1), false},
		{strings.Replace(valid, "https://metrics.example.invalid", "http://metrics.example.invalid", 1), false},
		{strings.Replace(valid, "https://metrics.example.invalid", "https://user@metrics.example.invalid", 1), false},
		{strings.Replace(valid, "https://metrics.example.invalid", "https://metrics.example.invalid/path", 1), false},
		{strings.Replace(valid, "maximum_ranks", "arbitrary_setting", 1), false},
	} {
		file := filepath.Join(t.TempDir(), "monitoring.json")
		if err := os.WriteFile(file, []byte(tc.input), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(file)
		if (err == nil) != tc.valid {
			t.Fatalf("configuration valid=%t, got %v: %s", tc.valid, err, tc.input)
		}
	}
}

func TestSeriesCardinalityQueryValidation(t *testing.T) {
	date := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		q     SeriesCardinalityQuery
		valid bool
	}{
		{"explicit day", SeriesCardinalityQuery{Date: date}, true},
		{"exact escaped metric", SeriesCardinalityQuery{Date: date, Metric: "m\u00e9tric\"\\", Limit: 100}, true},
		{"future valid syntax", SeriesCardinalityQuery{Date: date.AddDate(0, 0, 1)}, true},
		{"missing day", SeriesCardinalityQuery{}, false},
		{"non UTC", SeriesCardinalityQuery{Date: date.In(time.FixedZone("local", 0))}, false},
		{"partial day", SeriesCardinalityQuery{Date: date.Add(time.Second)}, false},
		{"subsecond", SeriesCardinalityQuery{Date: date.Add(time.Nanosecond)}, false},
		{"before epoch", SeriesCardinalityQuery{Date: time.Unix(-86400, 0).UTC()}, false},
		{"negative rank", SeriesCardinalityQuery{Date: date, Limit: -1}, false},
		{"excess ranks", SeriesCardinalityQuery{Date: date, Limit: 101}, false},
		{"invalid UTF8", SeriesCardinalityQuery{Date: date, Metric: string([]byte{255})}, false},
		{"oversized metric", SeriesCardinalityQuery{Date: date, Metric: strings.Repeat("x", maximumCardinalityNameBytes+1)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.q.valid() != tc.valid {
				t.Fatalf("query validation=%t, want %t", tc.q.valid(), tc.valid)
			}
		})
	}
}

func TestSeriesCardinalityScopedNativeProjection(t *testing.T) {
	date := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	scope := workspaces.Workspace{ID: "11111111-1111-4111-8111-111111111111", TenantID: "22222222-2222-4222-8222-222222222222"}
	const node = "33333333-3333-4333-8333-333333333333"
	metric := "m\u00e9tric\"\\"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/status/tsdb" || q.Get("date") != "2026-11-12" || q.Get("topN") != "3" ||
			q.Get("focusLabel") != "node_id" || q.Get("deny_partial_response") != "1" ||
			q.Get("match[]") != "{__name__="+strconv.Quote(metric)+"}" ||
			!reflect.DeepEqual(q["extra_label"], []string{"tenant_id=" + scope.TenantID, "workspace_id=" + scope.ID}) ||
			len(q) != 7 || !strings.HasSuffix(q.Get("timeout"), "ms") {
			t.Error("request did not retain exact server-owned scope and bounded options")
		}
		timeout, err := time.ParseDuration(q.Get("timeout"))
		if err != nil || timeout <= 0 || timeout > 5*time.Second {
			t.Error("source timeout is not bounded by the operation")
		}
		name, _ := json.Marshal(metric)
		fmt.Fprintf(w, `{"status":"success","data":{"totalSeries":2,"seriesCountByMetricName":[{"name":%s,"value":2}],"seriesCountByFocusLabelValue":[{"name":%q,"value":1}],"seriesCountByLabelValuePair":[{"name":"foreign-private-label","value":999}],"metricNameStats":{"records":[{"metricName":"foreign-private-metric","requestsCount":900}]}}}`, name, node)
	}))
	defer server.Close()
	r := newSeriesCardinalityReader(SeriesCardinalityPolicy{RawOrigin: server.URL, LookbackDays: 7, MaximumRanks: 3}, 5*time.Second, 2)
	t.Cleanup(r.client.CloseIdleConnections)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	out, err := r.read(ctx, scope, date.Add(12*time.Hour), SeriesCardinalityQuery{Date: date, Metric: metric})
	if err != nil || out.WorkspaceID != scope.ID || out.Date != "2026-11-12" || out.TotalSeries != 2 || out.RankLimit != 3 || len(out.Metrics) != 1 || len(out.Nodes) != 1 || out.Nodes[0].NodeID != node {
		t.Fatalf("invalid scoped projection: %+v %v", out, err)
	}
	body, err := json.Marshal(out)
	if err != nil || bytes.Contains(body, []byte("foreign-private")) || bytes.Contains(body, []byte("requestsCount")) || bytes.Contains(body, []byte("metricNameStats")) {
		t.Fatal("source-only metadata escaped projection")
	}
	// The unranked remainder is not evidence for a fabricated unattributed row.
	if len(out.Nodes) != 1 || out.Nodes[0].SeriesCount != 1 {
		t.Fatal("missing focus rows were invented")
	}
}

func TestSeriesCardinalityRejectsMalformedAndIncompleteSource(t *testing.T) {
	const valid = `{"status":"success","data":{"totalSeries":2,"seriesCountByMetricName":[{"name":"cpu","value":2}],"seriesCountByFocusLabelValue":[{"name":"33333333-3333-4333-8333-333333333333","value":2}]}}`
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"partial", strings.Replace(valid, `"status":"success"`, `"status":"success","isPartial":true`, 1), 200},
		{"null partial flag", strings.Replace(valid, `"status":"success"`, `"status":"success","isPartial":null`, 1), 200},
		{"warnings", strings.Replace(valid, `"status":"success"`, `"status":"success","warnings":["partial scan"]`, 1), 200},
		{"missing total", strings.Replace(valid, `"totalSeries":2,`, "", 1), 200},
		{"null total", strings.Replace(valid, `"totalSeries":2`, `"totalSeries":null`, 1), 200},
		{"fractional", strings.Replace(valid, `"totalSeries":2`, `"totalSeries":1.5`, 1), 200},
		{"negative", strings.Replace(valid, `"totalSeries":2`, `"totalSeries":-1`, 1), 200},
		{"unsafe integer", strings.Replace(valid, `"totalSeries":2`, `"totalSeries":9007199254740992`, 1), 200},
		{"rank exceeds total", strings.Replace(valid, `"name":"cpu","value":2`, `"name":"cpu","value":3`, 1), 200},
		{"zero rank", strings.Replace(valid, `"name":"cpu","value":2`, `"name":"cpu","value":0`, 1), 200},
		{"absent metric rank", strings.Replace(valid, `[{"name":"cpu","value":2}]`, `[]`, 1), 200},
		{"null name", strings.Replace(valid, `"name":"cpu"`, `"name":null`, 1), 200},
		{"missing value", strings.Replace(valid, `"name":"cpu","value":2`, `"name":"cpu"`, 1), 200},
		{"duplicate metric", strings.Replace(valid, `[{"name":"cpu","value":2}]`, `[{"name":"cpu","value":1},{"name":"cpu","value":1}]`, 1), 200},
		{"sum exceeds total", strings.Replace(valid, `[{"name":"cpu","value":2}]`, `[{"name":"cpu","value":2},{"name":"gpu","value":1}]`, 1), 200},
		{"excess rows", strings.Replace(valid, `[{"name":"cpu","value":2}]`, `[{"name":"a","value":1},{"name":"b","value":1},{"name":"c","value":1},{"name":"d","value":1}]`, 1), 200},
		{"invalid node", strings.Replace(valid, "33333333-3333-4333-8333-333333333333", "arbitrary-agent", 1), 200},
		{"empty node", strings.Replace(valid, "33333333-3333-4333-8333-333333333333", "", 1), 200},
		{"duplicate node", strings.Replace(valid, `[{"name":"33333333-3333-4333-8333-333333333333","value":2}]`, `[{"name":"33333333-3333-4333-8333-333333333333","value":1},{"name":"33333333-3333-4333-8333-333333333333","value":1}]`, 1), 200},
		{"foreign metric drilldown", strings.Replace(valid, `"name":"cpu"`, `"name":"other"`, 1), 200},
		{"oversized", strings.Repeat(" ", maximumCardinalityReplyBytes+1), 200},
		{"invalid UTF8", valid + string([]byte{255}), 200},
		{"trailing JSON", valid + `{}`, 200},
		{"upstream failure", valid, 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer server.Close()
			reader := newSeriesCardinalityReader(SeriesCardinalityPolicy{RawOrigin: server.URL, LookbackDays: 1, MaximumRanks: 3}, time.Second, 1)
			defer reader.client.CloseIdleConnections()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			date := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
			out, err := reader.read(ctx, workspaces.Workspace{}, date, SeriesCardinalityQuery{Date: date, Metric: "cpu"})
			if err == nil || err.Error() != "MONITORING_CARDINALITY_UNAVAILABLE" || out.Metrics != nil || out.Nodes != nil {
				t.Fatalf("invalid source released data: %+v %v", out, err)
			}
		})
	}
}

func TestSeriesCardinalityDateAndRankAdmission(t *testing.T) {
	reader := newSeriesCardinalityReader(SeriesCardinalityPolicy{RawOrigin: "http://127.0.0.1:1", LookbackDays: 7, MaximumRanks: 2}, time.Second, 1)
	defer reader.client.CloseIdleConnections()
	today := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		q    SeriesCardinalityQuery
		code string
	}{
		{SeriesCardinalityQuery{Date: today.AddDate(0, 0, -7)}, "MONITORING_CARDINALITY_RANGE_INVALID"},
		{SeriesCardinalityQuery{Date: today.AddDate(0, 0, 1)}, "MONITORING_CARDINALITY_RANGE_INVALID"},
		{SeriesCardinalityQuery{Date: today, Limit: 3}, "VALIDATION_FAILED"},
	} {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		out, err := reader.read(ctx, workspaces.Workspace{}, today, tc.q)
		cancel()
		if err == nil || err.Error() != tc.code || out.Metrics != nil {
			t.Fatalf("admission failed: %+v %v", out, err)
		}
	}
	if _, err := reader.read(context.Background(), workspaces.Workspace{}, today, SeriesCardinalityQuery{Date: today}); err == nil || err.Error() != "MONITORING_OWNER_REQUIRED" {
		t.Fatalf("unbounded source read: %v", err)
	}
}

func TestSeriesCardinalityRedirectAndCancellation(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("scoped source query followed a redirect") }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	date := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	reader := newSeriesCardinalityReader(SeriesCardinalityPolicy{RawOrigin: redirect.URL, LookbackDays: 1, MaximumRanks: 1}, time.Second, 1)
	defer reader.client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := reader.read(ctx, workspaces.Workspace{}, date, SeriesCardinalityQuery{Date: date}); err == nil || err.Error() != "MONITORING_CARDINALITY_UNAVAILABLE" {
		t.Fatal("redirect accepted", err)
	}
	canceled, stop := context.WithTimeout(t.Context(), time.Second)
	stop()
	if _, err := reader.read(canceled, workspaces.Workspace{}, date, SeriesCardinalityQuery{Date: date}); err != context.Canceled {
		t.Fatal("cancellation was hidden", err)
	}
}

func TestSeriesCardinalityEmptyAndRankOrdering(t *testing.T) {
	const nodeA = "33333333-3333-4333-8333-333333333333"
	const nodeB = "44444444-4444-4444-8444-444444444444"
	const nodeC = "55555555-5555-4555-8555-555555555555"
	scope := workspaces.Workspace{ID: "11111111-1111-4111-8111-111111111111"}
	selection := SeriesCardinalityQuery{Date: time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)}
	for _, tc := range []struct {
		name, body string
		empty      bool
	}{
		{"empty index", `{"status":"success","data":{"totalSeries":0,"seriesCountByMetricName":[],"seriesCountByFocusLabelValue":[]}}`, true},
		{"stable ranks", `{"status":"success","data":{"totalSeries":4,"seriesCountByMetricName":[{"name":"z","value":1},{"name":"a","value":1},{"name":"largest","value":2}],"seriesCountByFocusLabelValue":[{"name":"` + nodeB + `","value":1},{"name":"` + nodeA + `","value":1},{"name":"` + nodeC + `","value":2}]}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reply cardinalityReply
			if err := json.Unmarshal([]byte(tc.body), &reply); err != nil {
				t.Fatal(err)
			}
			out, err := reply.project(scope, selection, 3)
			if err != nil || out.Metrics == nil || out.Nodes == nil {
				t.Fatalf("projection: %+v %v", out, err)
			}
			if tc.empty {
				if out.TotalSeries != 0 || len(out.Metrics) != 0 || len(out.Nodes) != 0 {
					t.Fatalf("measured empty: %+v", out)
				}
				return
			}
			if out.Metrics[0].MetricName != "largest" || out.Metrics[1].MetricName != "a" || out.Metrics[2].MetricName != "z" ||
				out.Nodes[0].NodeID != nodeC || out.Nodes[1].NodeID != nodeA || out.Nodes[2].NodeID != nodeB {
				t.Fatalf("unstable canonical node rank order: %+v", out)
			}
		})
	}
}
