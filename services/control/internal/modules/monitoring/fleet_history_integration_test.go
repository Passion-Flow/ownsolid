//go:build repository_integration && monitoring_integration && linux

package monitoring_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring"
)

func TestScopedFleetHistoryOffline(t *testing.T) {
	g := setup(t, false)
	if os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE") != "postgresql" {
		t.Fatal("the offline fleet fixture requires isolated PostgreSQL")
	}
	raw, rawDir, _ := startHistoryVM(t, g.ctx, "2d")
	long, longDir, stopLong := startHistoryVM(t, g.ctx, "90d")
	var rawReads atomic.Int32
	var onRawRead atomic.Pointer[func()]
	rawURL, _ := url.Parse(raw)
	proxy := httputil.NewSingleHostReverseProxy(rawURL)
	rawCounted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawReads.Add(1)
		if hook := onRawRead.Load(); hook != nil {
			(*hook)()
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(rawCounted.Close)
	g.config.FleetHistory = monitoring.FleetHistoryPolicy{
		RawOrigin: rawCounted.URL, LongOrigin: long, RawHorizonSeconds: 3600,
		MaxRangeSeconds: 90 * 86400, SettlingSeconds: 30,
	}
	g.rebuild()
	otherWorkspace, foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString(), uuid.NewString()
	g.exec("INSERT INTO tenants(id,name,status) VALUES($1,'Foreign tenant','active')", "", foreignTenant)
	g.exec("INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign workspace','active')", "", foreignWorkspace, foreignTenant)
	g.exec("INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Second workspace','active')", "", otherWorkspace, g.f.Tenant)

	// Keep raw data for an old range deliberately. An unavailable rollup tier
	// must fail even though a raw fallback could return successful data.
	recent := time.Now().UTC().Truncate(time.Minute).Add(-4 * time.Minute)
	old := recent.Add(-2 * time.Hour)
	var samples bytes.Buffer
	insert := func(stamp time.Time, tenant, workspace, cell, node string, up int) {
		fmt.Fprintf(&samples, "up{job=%q,site_id=%q,cell_id=%q,node_id=%q,tenant_id=%q,workspace_id=%q} %d %d\n",
			"ownsolid-node", "site-1", cell, node, tenant, workspace, up, stamp.UnixMilli())
	}
	for _, stamp := range []time.Time{old, recent} {
		insert(stamp, g.f.Tenant, g.f.Workspace, "down", "n-1", 0)
		insert(stamp, g.f.Tenant, g.f.Workspace, "healthy", "n-2", 1)
		insert(stamp, g.f.Tenant, otherWorkspace, "down", "n-1", 1)
		insert(stamp, foreignTenant, foreignWorkspace, "down", "n-1", 1)
		insert(stamp, foreignTenant, g.f.Workspace, "down", "n-1", 1)
		insert(stamp, "", g.f.Workspace, "down", "n-1", 1)
		for i := range 10000 {
			insert(stamp, g.f.Tenant, otherWorkspace, fmt.Sprintf("scale-%d", i%5), fmt.Sprintf("scale-node-%d", i), 1)
		}
	}
	fixtureRequest(t, g.ctx, http.MethodPost, raw+"/api/v1/import/prometheus", &samples)
	fixtureRequest(t, g.ctx, http.MethodGet, raw+"/internal/force_flush", nil)
	replay := exec.CommandContext(g.ctx, os.Getenv("OWNSOLID_TEST_VMALERT"),
		"-rule=alerting/fleet-rollup-rules.yaml", "-datasource.url="+raw, "-remoteWrite.url="+long,
		"-replay.timeFrom="+old.Format(time.RFC3339), "-replay.timeTo="+old.Add(3*time.Minute).Format(time.RFC3339),
		"-replay.rulesDelay=0", "-replay.disableProgressBar")
	if log, e := replay.CombinedOutput(); e != nil {
		t.Fatalf("real rule replay: %v\n%s", e, log)
	}
	fixtureRequest(t, g.ctx, http.MethodGet, long+"/internal/force_flush", nil)
	get := func(a identity.Auth, workspace string, from, to time.Time) (monitoring.FleetHistory, error) {
		return g.s.GetFleetHistory(g.ctx, a, workspace, from, to, uuid.NewString())
	}
	reject := func(a identity.Auth, workspace string, from, to time.Time, codes ...string) {
		t.Helper()
		out, e := get(a, workspace, from, to)
		if out.Tier == "" && out.Cells == nil && e != nil {
			for _, code := range codes {
				if e.Error() == code {
					return
				}
			}
		}
		t.Fatalf("unexpected diagnostic refusal for %v: %+v, %v", codes, out, e)
	}
	reject(g.portal, g.f.Workspace, recent, recent.Add(2*time.Minute), "ACCESS_DENIED")
	reject(g.admin, foreignWorkspace, recent, recent.Add(2*time.Minute), "ACCESS_DENIED", "RESOURCE_NOT_FOUND")
	if rawReads.Load() != 0 {
		t.Fatal("denied callers reached raw store")
	}

	for _, tc := range []struct {
		tier   string
		from   time.Time
		step   int64
		points int
	}{{"raw", recent, 15, 3}, {"rollup", old, 60, 1}} {
		out, e := get(g.admin, g.f.Workspace, tc.from, tc.from.Add(2*time.Minute))
		if e != nil || out.Tier != tc.tier || out.StepSeconds != tc.step || len(out.Cells) != 2 {
			t.Fatalf("wanted the scoped %s result: %+v, %v", tc.tier, out, e)
		}
		for i, cell := range out.Cells {
			if cell.SiteID != "site-1" || cell.CellID != []string{"down", "healthy"}[i] || len(cell.Points) != tc.points {
				t.Fatalf("%s included an unexpected cell or filled a missing interval: %+v", tc.tier, cell)
			}
			for j, point := range cell.Points {
				if !point.At.Equal(tc.from.Add(time.Duration(int64(j)*tc.step)*time.Second)) ||
					point.Observed != 1 || point.Reachable != uint64(i) {
					t.Fatalf("%s scoped count or zero is wrong: %+v", tc.tier, point)
				}
			}
		}
	}
	mixed, e := get(g.admin, g.f.Workspace, old, recent.Add(2*time.Minute))
	if e != nil || mixed.Tier != "rollup" || len(mixed.Cells) != 2 || len(mixed.Cells[0].Points) != 1 {
		t.Fatalf("a range crossing the raw horizon must stay in rollup: %+v, %v", mixed, e)
	}
	empty, e := get(g.admin, g.f.Workspace, old.Add(time.Minute), old.Add(2*time.Minute))
	if e != nil || empty.Tier != "rollup" || empty.Cells == nil || len(empty.Cells) != 0 {
		t.Fatalf("missing samples must return an empty success: %+v, %v", empty, e)
	}

	for _, start := range []time.Time{recent, old} {
		started := time.Now()
		out, e := get(g.admin, otherWorkspace, start, start.Add(2*time.Minute))
		if e != nil || len(out.Cells) != 6 {
			t.Fatalf("wrong scoped 10,000-node result: %d cells, %v", len(out.Cells), e)
		}
		for _, cell := range out.Cells[1:] {
			if len(cell.Points) == 0 || cell.Points[0].Observed != 2000 || cell.Points[0].Reachable != 2000 {
				t.Fatalf("wrong 2,000-node cell result: %+v", cell)
			}
		}
		t.Logf("10,000-node %s Service read including SQL and audit: %s", out.Tier, time.Since(started))
	}
	reads := rawReads.Load()
	stopLong()
	reject(g.admin, g.f.Workspace, old, old.Add(2*time.Minute), "MONITORING_LONG_TIER_UNAVAILABLE")
	g.config.FleetHistory.LongOrigin = ""
	g.rebuild()
	reject(g.admin, g.f.Workspace, old, old.Add(2*time.Minute), "MONITORING_LONG_TIER_NOT_CONFIGURED")
	if rawReads.Load() != reads {
		t.Fatal("unavailable or unconfigured long tier attempted raw fallback")
	}

	audits := g.count("SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot'")
	// This update would block if the service held its first SQL fence during
	// store I/O. The second fence must catch it before emitting data or audit.
	var revocation sync.Once
	revoke := func() {
		revocation.Do(func() {
			_, e := g.f.PostgreSQL.Exec(g.ctx, "UPDATE workspaces SET status='disabled' WHERE id=$1", g.f.Workspace)
			if e != nil {
				t.Error("workspace revocation during query", e)
			}
		})
	}
	onRawRead.Store(&revoke)
	reject(g.admin, g.f.Workspace, recent, recent.Add(2*time.Minute), "ACCESS_DENIED")
	if g.count("SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot'") != audits {
		t.Fatal("denied diagnostics emitted a successful audit")
	}
	t.Logf("fixture disks after graceful stop: raw=%d long=%d allocated bytes", rawDir(), longDir())
}

func startHistoryVM(t *testing.T, ctx context.Context, retention string) (string, func() int64, func()) {
	t.Helper()
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := listener.Addr().String()
	listener.Close()
	base := t.TempDir()
	dir, log := filepath.Join(base, "data"), filepath.Join(base, "store.log")
	file, e := os.Create(log)
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(os.Getenv("OWNSOLID_TEST_VICTORIA_METRICS"),
		"-httpListenAddr="+address, "-storageDataPath="+dir, "-retentionPeriod="+retention,
		"-memory.allowedBytes=192MB", "-search.disableCache")
	cmd.Stdout, cmd.Stderr = file, file
	e = cmd.Start()
	file.Close()
	if e != nil {
		t.Fatal(e)
	}
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	stop := sync.OnceFunc(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case e := <-finished:
			if e != nil {
				logText, _ := os.ReadFile(log)
				t.Errorf("VictoriaMetrics %s exit: %v\n%s", retention, e, logText)
			}
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-finished
			t.Errorf("VictoriaMetrics %s missed the graceful stop deadline", retention)
		}
		t.Logf("stopped VictoriaMetrics %s on %s", retention, address)
	})
	t.Cleanup(stop)
	origin := "http://" + address
	deadline, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for deadline.Err() == nil {
		req, _ := http.NewRequestWithContext(deadline, http.MethodGet, origin+"/health", nil)
		resp, e := http.DefaultClient.Do(req)
		if e == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				t.Logf("started VictoriaMetrics %s on %s", retention, address)
				return origin, func() int64 {
					stop()
					var size int64
					if e := filepath.Walk(dir, func(_ string, info os.FileInfo, e error) error {
						if e == nil {
							size += info.Sys().(*syscall.Stat_t).Blocks * 512
						}
						return e
					}); e != nil {
						t.Fatal(e)
					}
					return size
				}, stop
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	logText, _ := os.ReadFile(log)
	t.Fatalf("VictoriaMetrics %s startup:\n%s", retention, logText)
	return "", nil, nil
}

func fixtureRequest(t *testing.T, ctx context.Context, method, path string, body io.Reader) {
	t.Helper()
	req, e := http.NewRequestWithContext(ctx, method, path, body)
	if e != nil {
		t.Fatal(e)
	}
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		t.Fatalf("fixture request %s returned %d: %s", path, resp.StatusCode, body)
	}
}
