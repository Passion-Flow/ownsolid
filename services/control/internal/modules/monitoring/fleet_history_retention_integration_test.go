//go:build repository_integration && monitoring_integration && linux

package monitoring_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring"
	monitoringhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

const (
	retentionCellCount        = 5
	retentionDays             = 90
	retentionWarmReads        = 30
	retentionCellNodes uint64 = 2000
)

// One cell loses reachability for an hour every four hours. Other points
// vary by up to eight nodes to avoid measuring only constant compression.
func retentionReachable(minute int64, cell int) uint64 {
	if cell == 0 && minute%240 < 60 {
		return 0
	}
	return retentionCellNodes - uint64((minute+int64(cell))%9)
}

// TestFleetHistoryNinetyDayAggregates measures synthetic recording output, not
// raw node or agent traffic. The original authenticated read remains unchanged.
func TestFleetHistoryNinetyDayAggregates(t *testing.T) {
	if os.Getenv("PROJECT_ONE_TEST_DSN_FILE") == "" {
		t.Fatal("the retention fixture requires isolated PostgreSQL")
	}
	accounts := identityfixture.New(t)
	password := identityfixture.Bootstrap(t, accounts)
	var tenant, workspace string
	if err := accounts.DB.QueryRow(t.Context(), "SELECT tenant_id::text,id::text FROM workspaces ORDER BY created_at,id LIMIT 1").Scan(&tenant, &workspace); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "long-data")
	start := func() (string, func()) {
		return startMonitoringFixtureProcess(t, t.Context(), "VictoriaMetrics retained aggregates", "OWNSOLID_TEST_VICTORIA_METRICS",
			"-storageDataPath="+dir, "-retentionPeriod=100d", "-memory.allowedBytes=192MB", "-search.disableCache")
	}
	initial, stopInitial := start()
	to := time.Now().UTC().Truncate(time.Minute).Add(-4 * time.Minute)
	from := to.Add(-retentionDays * 24 * time.Hour)
	const samples = int64(retentionCellCount * 2 * retentionDays * 24 * 60)
	if rows := importRetentionRollups(t, initial, tenant, workspace, from, to); rows != samples {
		t.Fatalf("stream produced %d samples, want %d", rows, samples)
	}
	fixtureRequest(t, t.Context(), http.MethodGet, initial+"/internal/force_flush", nil)
	verifyRetentionRows(t, initial, tenant, workspace, to.Add(-time.Second), samples)
	stopInitial()
	// Restart clears native process caches. The host page cache is left alone.
	long, stopLong := start()
	var rawReads atomic.Int32
	rawGuard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rawReads.Add(1)
		http.Error(w, "raw tier is excluded from this historical profile", http.StatusServiceUnavailable)
	}))
	t.Cleanup(rawGuard.Close)
	policy := monitoring.Defaults()
	policy.FleetHistory = monitoring.FleetHistoryPolicy{
		RawOrigin: rawGuard.URL, LongOrigin: long, RawHorizonSeconds: 3600,
		MaxRangeSeconds: retentionDays * 86400, SettlingSeconds: 30,
	}
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/monitoring/openapi.json")
	browser := newHistoryBrowser(t, mountHistoryHTTP(t, accounts, policy), httpfixture.AdminOrigin, schemas)
	browser.login(password)
	path := cellroute.Public(monitoringhttp.FleetHistoryPath, wire.LocalScope()) + "?" +
		url.Values{"workspace_id": {workspace}, "from": {from.Format(time.RFC3339)}, "to": {to.Format(time.RFC3339)}}.Encode()
	var first time.Duration
	var warm []time.Duration
	var returnedPoints int
	for request := range retentionWarmReads + 1 {
		started := time.Now()
		reply, headers := browser.request(http.MethodGet, path, nil, http.StatusOK, "")
		body, err := json.Marshal(reply["data"])
		var out monitoring.FleetHistory
		if err != nil || json.Unmarshal(body, &out) != nil || out.Tier != "rollup" ||
			out.StepSeconds <= 60 || out.From.Before(from) || out.To.After(to) || len(out.Cells) != retentionCellCount {
			t.Fatal("ninety-day read did not return the bounded native rollup grid")
		}
		points := 0
		for at := out.From; !at.After(out.To) && at.Before(to); at = at.Add(time.Duration(out.StepSeconds) * time.Second) {
			points++
		}
		if points < 1 || points > 1440 {
			t.Fatal("ninety-day grid is outside the public point budget")
		}
		sawZero := false
		for cell, result := range out.Cells {
			if result.SiteID != "site-1" || result.CellID != "cell-"+strconv.Itoa(cell) || len(result.Points) != points {
				t.Fatal("retained series disappeared or changed scope")
			}
			for i, point := range result.Points {
				at := out.From.Add(time.Duration(int64(i)*out.StepSeconds) * time.Second)
				if !point.At.Equal(at) || !at.Before(to) || point.Observed != retentionCellNodes ||
					point.Reachable != retentionReachable(int64(at.Sub(from)/time.Minute), cell) {
					t.Fatal("retained count, timestamp or measured zero changed")
				}
				sawZero = sawZero || point.Reachable == 0
			}
		}
		if !sawZero {
			t.Fatal("historical measured zero was lost")
		}
		var audits int
		if err := accounts.DB.QueryRow(t.Context(), "SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot' AND request_id=$1", headers.Get("X-Request-ID")).Scan(&audits); err != nil || audits != 1 {
			t.Fatal("historical HTTP response lacked its committed request audit", err)
		}
		elapsed := time.Since(started)
		if request == 0 {
			first = elapsed
		} else {
			warm = append(warm, elapsed)
		}
		returnedPoints = points
	}
	if rawReads.Load() != 0 {
		t.Fatal("historical profile reached the excluded raw tier")
	}
	verifyRetentionRows(t, long, tenant, workspace, to.Add(-time.Second), samples)
	stopLong()
	slices.Sort(warm)
	t.Logf("90-day synthetic aggregates: %d series, %d persisted samples, %d points per cell, %d allocated bytes; first read after native restart=%s; 30 subsequent authenticated reads with schema/count/audit verification: median=%s p95=%s maximum=%s",
		retentionCellCount*2, samples, returnedPoints, historyAllocatedBytes(t, dir), first,
		warm[(len(warm)+1)/2-1], warm[(len(warm)*95+99)/100-1], warm[len(warm)-1])
}

func importRetentionRollups(t *testing.T, origin, tenant, workspace string, from, to time.Time) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	reader, writer := io.Pipe()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/api/v1/import/prometheus", reader)
	if err != nil {
		reader.Close()
		writer.Close()
		t.Fatal(err)
	}
	type outcome struct {
		rows int64
		err  error
	}
	complete := make(chan outcome, 1)
	go func() {
		buffer := bufio.NewWriterSize(writer, 64<<10)
		rows, err := writeRetentionRollups(buffer, tenant, workspace, from, to)
		if err == nil {
			err = buffer.Flush()
		}
		writer.CloseWithError(err)
		complete <- outcome{rows, err}
	}()
	response, requestError := http.DefaultClient.Do(request)
	reader.CloseWithError(requestError)
	produced := <-complete
	if response != nil {
		response.Body.Close()
	}
	if requestError != nil || produced.err != nil || response == nil || response.StatusCode != http.StatusNoContent {
		// VictoriaMetrics uses 204 for a completed Prometheus text import.
		t.Fatalf("bounded native import failed: request=%v producer=%v", requestError, produced.err)
	}
	return produced.rows
}

func writeRetentionRollups(buffer *bufio.Writer, tenant, workspace string, from, to time.Time) (int64, error) {
	names := []string{"ownsolid:workspace_cell_nodes_observed:count", "ownsolid:workspace_cell_nodes_reachable:count"}
	var prefixes [retentionCellCount][2]string
	for cell := range prefixes {
		for measure, name := range names {
			prefixes[cell][measure] = fmt.Sprintf("%s{tenant_id=%q,workspace_id=%q,site_id=%q,cell_id=%q} ",
				name, tenant, workspace, "site-1", "cell-"+strconv.Itoa(cell))
		}
	}
	var rows int64
	for at, minute := from, int64(0); at.Before(to); at, minute = at.Add(time.Minute), minute+1 {
		stamp := strconv.FormatInt(at.UnixMilli(), 10)
		for cell := range prefixes {
			for measure, prefix := range prefixes[cell] {
				value := retentionCellNodes
				if measure == 1 {
					value = retentionReachable(minute, cell)
				}
				if _, err := buffer.WriteString(prefix + strconv.FormatUint(value, 10) + " " + stamp + "\n"); err != nil {
					return rows, err
				}
				rows++
			}
		}
	}
	return rows, nil
}

func verifyRetentionRows(t *testing.T, origin, tenant, workspace string, at time.Time, expected int64) {
	t.Helper()
	selector := fmt.Sprintf("{__name__=~%q,tenant_id=%q,workspace_id=%q}",
		"ownsolid:workspace_cell_nodes_(observed|reachable):count", tenant, workspace)
	query := url.Values{"query": {"sum(count_over_time(" + selector + "[90d]))"}, "time": {strconv.FormatInt(at.Unix(), 10)}, "deny_partial_response": {"1"}}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/api/v1/query?"+query.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8193))
	var out struct {
		Status   string   `json:"status"`
		Partial  bool     `json:"isPartial"`
		Warnings []string `json:"warnings"`
		Data     struct {
			Type   string `json:"resultType"`
			Result []struct {
				Value []json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err != nil || response.StatusCode != http.StatusOK || len(body) > 8192 || json.Unmarshal(body, &out) != nil ||
		out.Status != "success" || out.Partial || len(out.Warnings) != 0 || out.Data.Type != "vector" ||
		len(out.Data.Result) != 1 || len(out.Data.Result[0].Value) != 2 {
		t.Fatal("native retention count response is invalid")
	}
	var text string
	if json.Unmarshal(out.Data.Result[0].Value[1], &text) != nil {
		t.Fatal("native persisted sample count is not a string")
	}
	count, err := strconv.ParseFloat(text, 64)
	if err != nil || count != float64(expected) {
		t.Fatalf("native store has %s retained samples, want %d", text, expected)
	}
}
