//go:build repository_integration && monitoring_integration && linux

package monitoring_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
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
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring"
	monitoringhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
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
		return readFleetHistoryCandidate(t, g, a, workspace, from, to)
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
		t.Logf("10,000-node %s HTTP candidate read including SQL and audit: %s", out.Tier, time.Since(started))
	}
	reads := rawReads.Load()
	checkHistoryResponseLimit(t, g, get, recent)
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

func readFleetHistoryCandidate(t *testing.T, g *story, a identity.Auth, workspace string, from, to time.Time) (monitoring.FleetHistory, error) {
	t.Helper()
	query := url.Values{"workspace_id": {workspace}, "from": {from.Format(time.RFC3339)}, "to": {to.Format(time.RFC3339)}}
	request := httptest.NewRequestWithContext(g.ctx, http.MethodGet, monitoringhttp.FleetHistoryPath+"?"+query.Encode(), nil)
	handler := monitoringhttp.Handler{Service: g.s}
	requestID := uuid.NewString()
	body, status, err := handler.HandleFleetHistory(request, a, requestID)
	if err != nil {
		if body != nil || status != 0 {
			t.Fatal("failed history candidate returned a successful response")
		}
		return monitoring.FleetHistory{}, err
	}
	var out monitoring.FleetHistory
	if status != http.StatusOK || len(body) > 1<<20 || json.Unmarshal(body, &out) != nil {
		t.Fatalf("invalid candidate history response: status %d, bytes %d", status, len(body))
	}
	if g.count("SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot' AND request_id=$1", requestID) != 1 {
		t.Fatal("successful candidate response lacked its committed audit")
	}
	return out, nil
}

func checkHistoryResponseLimit(t *testing.T, g *story, get func(identity.Auth, string, time.Time, time.Time) (monitoring.FleetHistory, error), end time.Time) {
	t.Helper()
	oversized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oversizedHistoryReply(t, w, r, g.f.Tenant, g.f.Workspace)
	}))
	t.Cleanup(oversized.Close)
	before := g.config.FleetHistory.LongOrigin
	g.config.FleetHistory.LongOrigin = oversized.URL
	g.rebuild()
	defer func() {
		g.config.FleetHistory.LongOrigin = before
		g.rebuild()
	}()
	audits := g.count("SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot'")
	out, err := get(g.admin, g.f.Workspace, end.Add(-5*24*time.Hour), end)
	code, status := httpserver.PublicProblem(err)
	if out.Tier != "" || out.Cells != nil || code != "MONITORING_HISTORY_RESPONSE_LIMIT" || status != http.StatusUnprocessableEntity ||
		g.count("SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot'") != audits {
		t.Fatalf("oversized projection or audit escaped the bound: %s %d %+v", code, status, out)
	}
	t.Log("candidate refused an over-budget history body before successful audit")
}

// oversizedHistoryReply is an explicit protocol double for the response budget.
// Native VictoriaMetrics supplies every successful data read in the HTTP profile.
func oversizedHistoryReply(t *testing.T, w http.ResponseWriter, r *http.Request, tenant, workspace string) {
	t.Helper()
	const cells = 24
	q := r.URL.Query()
	start, errStart := strconv.ParseInt(q.Get("start"), 10, 64)
	end, errEnd := strconv.ParseInt(q.Get("end"), 10, 64)
	step, errStep := strconv.ParseInt(q.Get("step"), 10, 64)
	if errStart != nil || errEnd != nil || errStep != nil || step <= 0 || end < start || (end-start)/step > 1440 {
		t.Error("fixture received an invalid evaluation grid")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var values strings.Builder
	for at := start; at <= end; at += step {
		if at > start {
			values.WriteByte(',')
		}
		fmt.Fprintf(&values, "[%d,\"1\"]", at)
	}
	var response strings.Builder
	response.WriteString(`{"status":"success","data":{"resultType":"matrix","result":[`)
	for cell := range cells {
		for _, measure := range []string{"observed", "reachable"} {
			if cell > 0 || measure == "reachable" {
				response.WriteByte(',')
			}
			fmt.Fprintf(&response, `{"metric":{"tenant_id":%q,"workspace_id":%q,"site_id":"site-1","cell_id":"large-%d","measure":%q},"values":[%s]}`,
				tenant, workspace, cell, measure, values.String())
		}
	}
	response.WriteString(`]}}`)
	_, _ = io.WriteString(w, response.String())
}

func startHistoryVM(t *testing.T, ctx context.Context, retention string, flags ...string) (string, func() int64, func()) {
	t.Helper()
	return startHistoryVMAt(t, ctx, retention, "http", flags...)
}

func startHistoryVMAt(t *testing.T, ctx context.Context, retention, scheme string, flags ...string) (string, func() int64, func()) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	process, disk := startHistoryVMIn(t, ctx, dir, retention, scheme, flags...)
	return process.origin, disk, process.stop
}

func startHistoryVMIn(t *testing.T, ctx context.Context, dir, retention, scheme string, flags ...string) (monitoringFixtureProcess, func() int64) {
	t.Helper()
	flags = append(flags, "-storageDataPath="+dir, "-retentionPeriod="+retention, "-memory.allowedBytes=192MB", "-search.disableCache")
	process := launchMonitoringFixtureProcessAt(t, ctx, scheme, "VictoriaMetrics "+retention, "OWNSOLID_TEST_VICTORIA_METRICS", flags...)
	return process, func() int64 {
		process.stop()
		return historyAllocatedBytes(t, dir)
	}
}

func historyAllocatedBytes(t *testing.T, dir string) int64 {
	t.Helper()
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
}

func startMonitoringFixtureProcess(t *testing.T, ctx context.Context, name, environment string, flags ...string) (string, func()) {
	t.Helper()
	return startMonitoringFixtureProcessAt(t, ctx, "http", name, environment, flags...)
}

func startMonitoringFixtureProcessAt(t *testing.T, ctx context.Context, scheme, name, environment string, flags ...string) (string, func()) {
	t.Helper()
	process := launchMonitoringFixtureProcessAt(t, ctx, scheme, name, environment, flags...)
	return process.origin, process.stop
}

type monitoringFixtureProcess struct {
	origin, binarySHA256 string
	argv                 []string
	stop                 func()
}

func launchMonitoringFixtureProcessAt(t *testing.T, ctx context.Context, scheme, name, environment string, flags ...string) monitoringFixtureProcess {
	t.Helper()
	binary := os.Getenv(environment)
	if !filepath.IsAbs(binary) {
		t.Fatalf("%s must point to the verified binary", environment)
	}
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := listener.Addr().String()
	listener.Close()
	log := filepath.Join(t.TempDir(), "store.log")
	file, e := os.Create(log)
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(binary, append(flags, "-httpListenAddr="+address)...)
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
				t.Errorf("%s exit: %v\n%s", name, e, logText)
			}
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-finished
			t.Errorf("%s missed the graceful stop deadline", name)
		}
		t.Logf("stopped %s on %s", name, address)
	})
	t.Cleanup(stop)
	// Bind fixture qualification to the executed child, rather than a caller's
	// assertion about its intended flags or a flag's upstream default.
	commandLine, e := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", cmd.Process.Pid))
	if e != nil || len(commandLine) == 0 || commandLine[len(commandLine)-1] != 0 {
		t.Fatalf("%s actual command line unavailable: %v", name, e)
	}
	argv := strings.Split(string(commandLine[:len(commandLine)-1]), "\x00")
	if !slices.Equal(argv, cmd.Args) {
		t.Fatalf("%s actual command line differs from the executed arguments", name)
	}
	executable, e := os.Open(fmt.Sprintf("/proc/%d/exe", cmd.Process.Pid))
	if e != nil {
		t.Fatal(e)
	}
	digest := sha256.New()
	_, readErr := io.Copy(digest, executable)
	closeErr := executable.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("%s executable identity: %v / %v", name, readErr, closeErr)
	}
	origin := scheme + "://" + address
	deadline, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for deadline.Err() == nil {
		req, _ := http.NewRequestWithContext(deadline, http.MethodGet, origin+"/health", nil)
		resp, e := http.DefaultClient.Do(req)
		if e == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				t.Logf("started %s on %s", name, address)
				return monitoringFixtureProcess{origin: origin, binarySHA256: fmt.Sprintf("%x", digest.Sum(nil)), argv: argv, stop: stop}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	logText, _ := os.ReadFile(log)
	t.Fatalf("%s startup:\n%s", name, logText)
	return monitoringFixtureProcess{}
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
