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
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
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
	source, diskBytes, stop := startHistoryVM(t, g.ctx, "7d", "-search.maxTSDBStatusSeries=10000", "-search.maxStatusRequestDuration=5s", "-search.maxTSDBStatusTopNSeries=3")
	target, _ := url.Parse(source)
	proxy := httputil.NewSingleHostReverseProxy(target)
	var reads atomic.Int32
	var onRead atomic.Pointer[func()]
	counted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if callback := onRead.Load(); callback != nil {
			(*callback)()
		}
		proxy.ServeHTTP(w, r)
	}))
	defer counted.Close()
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
	g.config.SeriesCardinality = monitoring.SeriesCardinalityPolicy{RawOrigin: counted.URL, LookbackDays: 7, MaximumRanks: 3}
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
		labels := map[string]string{"__name__": metric, "workspace_id": workspace}
		if tenant != "" {
			labels["tenant_id"] = tenant
		}
		if node != "" {
			labels["node_id"] = node
		}
		value := map[string]any{"metric": labels, "values": []int{1}, "timestamps": []int64{stamp.UnixMilli()}}
		if err := json.NewEncoder(&samples).Encode(value); err != nil {
			t.Fatal(err)
		}
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
