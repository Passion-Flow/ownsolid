//go:build repository_integration && monitoring_integration && linux

package monitoring_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

func TestScopedSeriesCardinalityOffline(t *testing.T) {
	if os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE") != "postgresql" {
		t.Fatal("cardinality qualification requires real isolated PostgreSQL")
	}
	g := setup(t, false)
	process, diskBytes := startHistoryVMIn(t, g.ctx, filepath.Join(t.TempDir(), "index"), "7d", "http", "-disablePerDayIndex=false", "-search.maxTSDBStatusSeries=10000", "-search.maxStatusRequestDuration=5s", "-search.maxTSDBStatusTopNSeries=3")
	source, stop := process.origin, process.stop
	var reads atomic.Int32
	var onRead atomic.Pointer[func()]
	stamp := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	day := stamp.Truncate(24 * time.Hour)
	exact := monitoring.SeriesCardinalityQuery{Date: day, Metric: "ownsolid_cardinality_cpu"}
	missing := monitoring.SeriesCardinalityQuery{Date: day, Metric: "ownsolid_cardinality_missing"}
	overview := monitoring.SeriesCardinalityQuery{Date: day}
	read := func(a identity.Auth, workspace string, q monitoring.SeriesCardinalityQuery) (monitoring.SeriesCardinality, error) {
		return g.s.GetSeriesCardinality(g.ctx, a, workspace, q, uuid.NewString())
	}
	refused := func(a identity.Auth, workspace string, q monitoring.SeriesCardinalityQuery, codes ...string) {
		t.Helper()
		out, err := read(a, workspace, q)
		if err != nil && out.Metrics == nil && out.Nodes == nil && out.WorkspaceID == "" {
			for _, code := range codes {
				if err.Error() == code {
					return
				}
			}
		}
		t.Fatalf("unexpected cardinality refusal: %+v %v, expected %v", out, err, codes)
	}
	refused(g.admin, g.f.Workspace, exact, "MONITORING_CARDINALITY_NOT_CONFIGURED")
	g.config.SeriesCardinality = monitoring.SeriesCardinalityPolicy{SourceID: uuid.NewString(), RawOrigin: source, MaximumScanSeries: 10000, LookbackDays: 7, MaximumRanks: 3}
	g.cardinalitySource = openCardinalityFixtureSource(t, g, process, func() {
		reads.Add(1)
		if callback := onRead.Load(); callback != nil {
			(*callback)()
		}
	})
	g.rebuild()
	other, foreignTenant, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString()
	g.exec("INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other cardinality scope','active')", "", other, g.f.Tenant)
	g.exec("INSERT INTO tenants(id,name,status) VALUES($1,'Foreign cardinality tenant','active')", "", foreignTenant)
	g.exec("INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign cardinality scope','active')", "", foreign, foreignTenant)
	refused(g.portal, g.f.Workspace, exact, "ACCESS_DENIED")
	refused(g.admin, foreign, exact, "RESOURCE_NOT_FOUND", "ACCESS_DENIED")
	refused(g.admin, g.f.Workspace, monitoring.SeriesCardinalityQuery{}, "VALIDATION_FAILED")
	refused(g.admin, g.f.Workspace, monitoring.SeriesCardinalityQuery{Date: day.AddDate(0, 0, -8)}, "MONITORING_CARDINALITY_RANGE_INVALID")
	if reads.Load() != 0 {
		t.Fatal("denied or invalid caller queried the native index")
	}

	var samples bytes.Buffer
	emit := func(metric, tenant, workspace, node string) {
		writeCardinalityFixtureSample(t, &samples, stamp, metric, tenant, workspace, node)
	}
	for index := range 10000 {
		emit(exact.Metric, g.f.Tenant, g.f.Workspace, uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("ownsolid-cardinality-%d", index))).String())
	}
	emit("ownsolid_cardinality_app", g.f.Tenant, g.f.Workspace, "")
	unicodeMetric := "card_\u6d4b\u8bd5_\"\\escaped"
	unicodeNode := uuid.NewString()
	emit(unicodeMetric, g.f.Tenant, g.f.Workspace, unicodeNode)
	emit(exact.Metric, g.f.Tenant, other, uuid.NewString())
	emit(exact.Metric, foreignTenant, foreign, uuid.NewString())
	emit(exact.Metric, foreignTenant, g.f.Workspace, uuid.NewString())
	emit(exact.Metric, "", g.f.Workspace, uuid.NewString())
	fixtureRequest(t, g.ctx, http.MethodPost, source+"/api/v1/import", &samples)
	fixtureRequest(t, g.ctx, http.MethodGet, source+"/internal/force_flush", nil)

	waitForCardinalityFixture(t, g.ctx, source, day, []cardinalityFixtureScope{
		{exact.Metric, g.f.Tenant, g.f.Workspace, 10000},
		{"ownsolid_cardinality_app", g.f.Tenant, g.f.Workspace, 1},
		{unicodeMetric, g.f.Tenant, g.f.Workspace, 1},
		{exact.Metric, g.f.Tenant, other, 1},
		{exact.Metric, foreignTenant, foreign, 1},
		{exact.Metric, foreignTenant, g.f.Workspace, 1},
		{exact.Metric, "", g.f.Workspace, 1},
	})

	// The overview exceeds the native scan cap even though only three ranks
	// were requested. Narrowing to the exact 10,000-series metric must recover.
	refused(g.admin, g.f.Workspace, overview, "MONITORING_CARDINALITY_UNAVAILABLE")
	requestID := uuid.NewString()
	started := time.Now()
	body, err := g.s.GetSeriesCardinalityJSON(g.ctx, g.admin, g.f.Workspace, exact, requestID)
	if err != nil || len(body) == 0 || len(body)+len(`{"data":`)+len("}\n") > 1<<20 {
		t.Fatalf("narrowed native JSON read: %d bytes %v", len(body), err)
	}
	var out monitoring.SeriesCardinality
	if json.Unmarshal(body, &out) != nil || out.TotalSeries != 10000 || out.WorkspaceID != g.f.Workspace || out.Date != day.Format("2006-01-02") || out.RankLimit != 3 || len(out.Metrics) != 1 || out.Metrics[0].MetricName != exact.Metric || out.Metrics[0].SeriesCount != 10000 || len(out.Nodes) != 3 {
		t.Fatalf("native scope or rank projection: %+v", out)
	}
	for _, node := range out.Nodes {
		if node.NodeID == "" || node.SeriesCount != 1 {
			t.Fatalf("invalid native node rank: %+v", node)
		}
	}
	if g.count("SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot' AND request_id=$1 AND actor_user_id=$2 AND target_type='workspace' AND target_id=$3", requestID, g.admin.Me.User.ID, g.f.Workspace) != 1 {
		t.Fatal("served projection lacks its committed scoped request audit")
	}
	t.Logf("10,000 synthetic node-series narrowed read including SQL/audit: %s; %d JSON bytes", time.Since(started), len(body))
	empty, err := read(g.admin, g.f.Workspace, missing)
	if err != nil || empty.TotalSeries != 0 || empty.Metrics == nil || empty.Nodes == nil || len(empty.Metrics) != 0 || len(empty.Nodes) != 0 {
		t.Fatalf("empty native index: %+v %v", empty, err)
	}

	app, err := read(g.admin, g.f.Workspace, monitoring.SeriesCardinalityQuery{Date: day, Metric: "ownsolid_cardinality_app"})
	if err != nil || app.TotalSeries != 1 || len(app.Metrics) != 1 || app.Nodes == nil || len(app.Nodes) != 0 {
		t.Fatalf("native missing-node omission: %+v %v", app, err)
	}
	escaped, err := read(g.admin, g.f.Workspace, monitoring.SeriesCardinalityQuery{Date: day, Metric: unicodeMetric})
	if err != nil || escaped.TotalSeries != 1 || len(escaped.Metrics) != 1 || escaped.Metrics[0].MetricName != unicodeMetric || len(escaped.Nodes) != 1 || escaped.Nodes[0].NodeID != unicodeNode {
		t.Fatalf("native exact Unicode selection: %+v %v", escaped, err)
	}
	isolated, err := read(g.admin, other, exact)
	if err != nil || isolated.TotalSeries != 1 || isolated.WorkspaceID != other {
		t.Fatalf("other Workspace isolation: %+v %v", isolated, err)
	}

	before := g.count("SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot'")
	var revocation atomic.Bool
	revoke := func() {
		if revocation.CompareAndSwap(false, true) {
			ctx, cancel := context.WithTimeout(g.ctx, 2*time.Second)
			defer cancel()
			if _, err := g.f.PostgreSQL.Exec(ctx, "UPDATE workspaces SET status='disabled' WHERE id=$1", g.f.Workspace); err != nil {
				t.Error("authority revocation blocked during store I/O", err)
			}
		}
	}
	onRead.Store(&revoke)
	rejected, err := g.s.GetSeriesCardinalityJSON(g.ctx, g.admin, g.f.Workspace, exact, uuid.NewString())
	if rejected != nil || err == nil || err.Error() != "ACCESS_DENIED" || g.count("SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot'") != before {
		t.Fatalf("mid-query revocation released data/audit: %d bytes %v", len(rejected), err)
	}
	onRead.Store(nil)
	g.exec("UPDATE workspaces SET status='active' WHERE id=$1", "", g.f.Workspace)

	original := g.i.Storage
	fault := &cardinalityCommitFault{Store: original}
	g.i.Storage = fault
	defer func() { g.i.Storage = original }()
	for _, mode := range []string{"rollback", "unknown", "cancel"} {
		requestID := uuid.NewString()
		fault.requestID = requestID
		fault.mode = mode
		fault.armed.Store(true)
		ctx, cancel := context.WithCancel(g.ctx)
		fault.cancel = cancel
		body, err := g.s.GetSeriesCardinalityJSON(ctx, g.admin, g.f.Workspace, exact, requestID)
		cancel()
		if err == nil || body != nil || fault.armed.Load() {
			t.Fatalf("%s commit outcome released data or missed injection: %d bytes %v", mode, len(body), err)
		}
		expected := 1
		if mode == "rollback" {
			expected = 0
		}
		if g.count("SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot' AND request_id=$1", requestID) != expected {
			t.Fatalf("%s real transaction audit outcome differs", mode)
		}
		if mode == "unknown" && err.Error() != string(transaction.CommitUnknown) {
			t.Fatal("lost commit outcome was misclassified", err)
		}
	}
	g.i.Storage = original
	if out, err := read(g.admin, g.f.Workspace, exact); err != nil || out.TotalSeries != 10000 {
		t.Fatalf("fresh explicit retry failed: %+v %v", out, err)
	}
	stop()
	t.Logf("native index allocated bytes after graceful shutdown: %d; fixture is one UTC day, not the seventeen-million-series load", diskBytes())
	t.Run("daily-index-profile", func(t *testing.T) {
		qualifyDailyCardinalityFixture(t, g, other, foreignTenant, foreign)
	})
}

func writeCardinalityFixtureSample(t *testing.T, samples *bytes.Buffer, stamp time.Time, metric, tenant, workspace, node string) {
	t.Helper()
	labels := map[string]string{"__name__": metric, "workspace_id": workspace}
	if tenant != "" {
		labels["tenant_id"] = tenant
	}
	if node != "" {
		labels["node_id"] = node
	}
	value := map[string]any{"metric": labels, "values": []int{1}, "timestamps": []int64{stamp.UnixMilli()}}
	if err := json.NewEncoder(samples).Encode(value); err != nil {
		t.Fatal(err)
	}
}

func qualifyDailyCardinalityFixture(t *testing.T, g *story, other, foreignTenant, foreign string) {
	t.Helper()
	first, second, empty := cardinalityFixtureDays(time.Now())
	cpu, app := "ownsolid_daily_cpu", "ownsolid_daily_app"
	nodeA, shared, nodeB := uuid.NewString(), uuid.NewString(), uuid.NewString()
	var samples bytes.Buffer
	emit := func(day time.Time, metric, tenant, workspace, node string) {
		writeCardinalityFixtureSample(t, &samples, day.Add(12*time.Hour), metric, tenant, workspace, node)
	}
	emit(first, cpu, g.f.Tenant, g.f.Workspace, nodeA)
	emit(first, cpu, g.f.Tenant, g.f.Workspace, shared)
	emit(second, cpu, g.f.Tenant, g.f.Workspace, nodeB)
	emit(second, app, g.f.Tenant, g.f.Workspace, shared)
	emit(second, app, g.f.Tenant, g.f.Workspace, "")
	for _, day := range []time.Time{first, second} {
		emit(day, cpu, g.f.Tenant, other, uuid.NewString())
		emit(day, cpu, foreignTenant, foreign, uuid.NewString())
		emit(day, cpu, foreignTenant, g.f.Workspace, uuid.NewString())
		emit(day, cpu, "", g.f.Workspace, uuid.NewString())
	}
	emit(second, cpu, g.f.Tenant, other, uuid.NewString())
	flags := []string{"-disablePerDayIndex=false", "-search.maxTSDBStatusSeries=10000", "-search.maxStatusRequestDuration=5s", "-search.maxTSDBStatusTopNSeries=3"}
	dir := filepath.Join(t.TempDir(), "daily")
	original, originalSource := g.config.SeriesCardinality, g.cardinalitySource
	defer func() { g.config.SeriesCardinality, g.cardinalitySource = original, originalSource; g.rebuild() }()
	for _, stage := range []string{"ingested", "restarted"} {
		process, disk := startHistoryVMIn(t, g.ctx, dir, "7d", "http", flags...)
		if err := admitDailyCardinalityFixture(process.argv); err != nil {
			t.Fatal("actual daily source profile refused before product binding", err)
		}
		t.Logf("%s daily fixture admitted from actual child argv: binary SHA-256 %s; disablePerDayIndex=false; UTC days %s / %s; empty %s", stage, process.binarySHA256, first.Format("2006-01-02"), second.Format("2006-01-02"), empty.Format("2006-01-02"))
		if stage == "ingested" {
			fixtureRequest(t, g.ctx, http.MethodPost, process.origin+"/api/v1/import", bytes.NewReader(samples.Bytes()))
			fixtureRequest(t, g.ctx, http.MethodGet, process.origin+"/internal/force_flush", nil)
		}
		for _, day := range []time.Time{first, second} {
			count, otherCount, appCount := uint64(2), uint64(1), uint64(0)
			if day.Equal(second) {
				count, otherCount, appCount = 1, 2, 2
			}
			waitForCardinalityFixture(t, g.ctx, process.origin, day, []cardinalityFixtureScope{
				{cpu, g.f.Tenant, g.f.Workspace, count}, {app, g.f.Tenant, g.f.Workspace, appCount}, {cpu, g.f.Tenant, other, otherCount},
				{cpu, foreignTenant, foreign, 1}, {cpu, foreignTenant, g.f.Workspace, 1}, {cpu, "", g.f.Workspace, 1},
			})
		}
		g.config.SeriesCardinality.RawOrigin = process.origin
		g.cardinalitySource = openCardinalityFixtureSource(t, g, process, nil)
		g.rebuild()
		checks := []struct {
			day               time.Time
			workspace, metric string
			total             uint64
			metrics, nodes    map[string]uint64
		}{
			{first, g.f.Workspace, "", 2, map[string]uint64{cpu: 2}, map[string]uint64{nodeA: 1, shared: 1}},
			{second, g.f.Workspace, "", 3, map[string]uint64{cpu: 1, app: 2}, map[string]uint64{nodeB: 1, shared: 1}},
			{first, g.f.Workspace, cpu, 2, map[string]uint64{cpu: 2}, map[string]uint64{nodeA: 1, shared: 1}},
			{second, g.f.Workspace, cpu, 1, map[string]uint64{cpu: 1}, map[string]uint64{nodeB: 1}},
			{first, g.f.Workspace, app, 0, map[string]uint64{}, map[string]uint64{}},
			{second, g.f.Workspace, app, 2, map[string]uint64{app: 2}, map[string]uint64{shared: 1}},
			{empty, g.f.Workspace, "", 0, map[string]uint64{}, map[string]uint64{}},
		}
		for _, check := range checks {
			requestID := uuid.NewString()
			out, err := g.s.GetSeriesCardinality(g.ctx, g.admin, check.workspace, monitoring.SeriesCardinalityQuery{Date: check.day, Metric: check.metric}, requestID)
			metrics, nodes := map[string]uint64{}, map[string]uint64{}
			for _, rank := range out.Metrics {
				metrics[rank.MetricName] = rank.SeriesCount
			}
			for _, rank := range out.Nodes {
				nodes[rank.NodeID] = rank.SeriesCount
			}
			if err != nil || out.TotalSeries != check.total || out.Date != check.day.Format("2006-01-02") || out.WorkspaceID != check.workspace || out.RankLimit != 3 || out.Metrics == nil || out.Nodes == nil || len(metrics) != len(out.Metrics) || len(nodes) != len(out.Nodes) || !reflect.DeepEqual(metrics, check.metrics) || !reflect.DeepEqual(nodes, check.nodes) {
				t.Fatalf("%s daily product projection for %s/%q: %+v %v", stage, check.day.Format("2006-01-02"), check.metric, out, err)
			}
			if g.count("SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot' AND request_id=$1 AND target_id=$2", requestID, check.workspace) != 1 {
				t.Fatal("daily product projection lacks its committed scope audit")
			}
		}
		process.stop()
		t.Logf("%s two-day fixture allocated bytes after graceful stop: %d; five selected series, not fleet sizing", stage, disk())
	}
	// Deliberately start the unsupported native mode in a separate data directory.
	// The fixture admission must refuse its observed argv before product binding.
	flags[0] = "-disablePerDayIndex=true"
	control, disk := startHistoryVMIn(t, g.ctx, filepath.Join(t.TempDir(), "global"), "7d", "http", flags...)
	if err := admitDailyCardinalityFixture(control.argv); err == nil {
		t.Fatal("disabled actual source profile was admitted")
	}
	controlPolicy := g.config
	controlPolicy.SeriesCardinality.RawOrigin = control.origin
	ctx, cancel := context.WithTimeout(g.ctx, 5*time.Second)
	defer cancel()
	if adapter, err := monitoring.OpenSeriesCardinalityQuerySource(ctx, controlPolicy, newCardinalityFixtureOwner(t, controlPolicy, control, nil)); err == nil || adapter != nil {
		t.Fatal("typed query adapter admitted actual true-index control")
	}
	t.Logf("disabled-index control refused by fixture admission: actual binary SHA-256 %s; disablePerDayIndex=true; not an installed-product admission result", control.binarySHA256)
	fixtureRequest(t, g.ctx, http.MethodPost, control.origin+"/api/v1/import", bytes.NewReader(samples.Bytes()))
	fixtureRequest(t, g.ctx, http.MethodGet, control.origin+"/internal/force_flush", nil)
	for _, day := range []time.Time{first, second, empty} {
		waitForCardinalityFixture(t, g.ctx, control.origin, day, []cardinalityFixtureScope{
			{cpu, g.f.Tenant, g.f.Workspace, 3}, {app, g.f.Tenant, g.f.Workspace, 2},
			{cpu, g.f.Tenant, other, 3}, {cpu, foreignTenant, foreign, 2},
			{cpu, foreignTenant, g.f.Workspace, 2}, {cpu, "", g.f.Workspace, 2},
		})
	}
	control.stop()
	t.Logf("disabled-index control allocated bytes after graceful stop: %d; native global counts are not daily counts", disk())
}

// The wrapper observes a real final audit insert. It provokes a real constraint
// rollback or drops delivery only after the real PostgreSQL commit completes.
type cardinalityCommitFault struct {
	transaction.Store
	requestID, mode string
	cancel          context.CancelFunc
	armed           atomic.Bool
}

func (s *cardinalityCommitFault) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	injected := false
	err := s.Store.Within(ctx, ro, func(ctx context.Context, q transaction.Executor) error {
		if err := fn(ctx, q); err != nil {
			return err
		}
		if !s.armed.Load() {
			return nil
		}
		var count int
		if err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot' AND request_id=$1", s.requestID)).Scan(&count); err != nil {
			return err
		}
		if count == 1 && s.armed.CompareAndSwap(true, false) {
			injected = true
			if s.mode == "rollback" {
				_, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, "INSERT INTO audit_events(id,audience,action,outcome,request_id) VALUES('invalid','admin','monitoring.constraint','invalid','invalid')"))
				return err
			}
		}
		return nil
	})
	if err == nil && injected {
		if s.mode == "unknown" {
			return errors.Join(context.Canceled, &transaction.Error{Kind: transaction.CommitRolledBack}, &transaction.Error{Kind: transaction.CommitUnknown})
		}
		if s.mode == "cancel" {
			s.cancel()
		}
	}
	return err
}

type cardinalityFixtureScope struct {
	metric, tenant, workspace string
	series                    uint64
}

// Read native fixture counts directly; product observations and their audit
// assertions must remain single attempts through the authorized service.
func waitForCardinalityFixture(t *testing.T, parent context.Context, origin string, day time.Time, scopes []cardinalityFixtureScope) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	for _, scope := range scopes {
		query := url.Values{
			"date": {day.Format("2006-01-02")}, "topN": {"1"}, "timeout": {"2s"},
			"match[]": {fmt.Sprintf("{__name__=%s,tenant_id=%s,workspace_id=%s}", strconv.Quote(scope.metric), strconv.Quote(scope.tenant), strconv.Quote(scope.workspace))},
		}
		for {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/api/v1/status/tsdb?"+query.Encode(), nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatalf("native cardinality fixture readiness: %v", err)
			}
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 8193))
			closeErr := response.Body.Close()
			var reply struct {
				Status   string   `json:"status"`
				Partial  bool     `json:"isPartial"`
				Warnings []string `json:"warnings"`
				Data     *struct {
					Total *uint64 `json:"totalSeries"`
				} `json:"data"`
			}
			if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK || len(body) > 8192 ||
				json.Unmarshal(body, &reply) != nil || reply.Status != "success" || reply.Partial || len(reply.Warnings) != 0 ||
				reply.Data == nil || reply.Data.Total == nil {
				t.Fatal("native cardinality fixture returned an invalid readiness response")
			}
			if *reply.Data.Total > scope.series {
				t.Fatalf("native fixture has excess series for %q: %d, expected %d", scope.metric, *reply.Data.Total, scope.series)
			}
			if *reply.Data.Total == scope.series {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatalf("native fixture visibility deadline for %q: %d, expected %d", scope.metric, *reply.Data.Total, scope.series)
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
}
