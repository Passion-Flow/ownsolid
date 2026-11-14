//go:build repository_integration && monitoring_integration && linux

package monitoring_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring"
	monitoringhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/monitoringmetrics"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"
)

func TestLaneDHistoryTLS(t *testing.T) {
	binary := os.Getenv("LANE_D_CLI_BINARY")
	if binary == "" || os.Getenv("PROJECT_ONE_TEST_DSN_FILE") == "" {
		t.Fatal("the history CLI fixture needs its compiled CLI and isolated PostgreSQL")
	}
	accounts := identityfixture.New(t)
	password := identityfixture.Bootstrap(t, accounts)
	ctx := t.Context()
	var tenant, workspace string
	if err := accounts.DB.QueryRow(ctx, "SELECT tenant_id::text,id::text FROM workspaces ORDER BY created_at,id LIMIT 1").Scan(&tenant, &workspace); err != nil {
		t.Fatal(err)
	}
	foreignTenant, foreign := uuid.NewString(), uuid.NewString()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO tenants(id,name,status) VALUES($1,'Foreign CLI tenant','active')", []any{foreignTenant}},
		{"INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign CLI workspace','active')", []any{foreign, foreignTenant}},
	} {
		if _, err := accounts.DB.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	raw, _, _ := startHistoryVM(t, ctx, "2d")
	long, _, _ := startHistoryVM(t, ctx, "90d")
	var rawReads atomic.Int32
	var longFailure atomic.Bool
	front := func(origin string, rawTier bool) *httptest.Server {
		target, _ := url.Parse(origin)
		proxy := httputil.NewSingleHostReverseProxy(target)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rawTier {
				rawReads.Add(1)
			} else if longFailure.Load() {
				http.Error(w, "synthetic history outage", 503)
				return
			}
			proxy.ServeHTTP(w, r)
		}))
		t.Cleanup(server.Close)
		return server
	}
	rawFront, longFront := front(raw, true), front(long, false)
	policy := monitoring.Defaults()
	policy.FleetHistory = monitoring.FleetHistoryPolicy{RawOrigin: rawFront.URL, LongOrigin: longFront.URL,
		RawHorizonSeconds: 3600, MaxRangeSeconds: 90 * 86400, SettlingSeconds: 30}
	recorder, err := monitoringmetrics.New(policy.HTTP, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	accounts.Config.Server.AdminOrigin = "https://" + server.Listener.Addr().String()
	app := identityhttp.New(accounts)
	if err := app.InitializationError(); err != nil {
		t.Fatal(err)
	}
	service, err := monitoring.New(accounts, app.Workspaces.Service, app.Licensing.Service,
		app.Components.Service, app.Distribution.Service, recorder, policy)
	if err != nil {
		t.Fatal(err)
	}
	app.Monitoring = &monitoringhttp.Handler{Service: service}
	var requestMu sync.Mutex
	var requestID string
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		app.ServeHTTP(w, r)
		if strings.HasSuffix(r.URL.Path, "/monitoring/fleet-history") {
			requestMu.Lock()
			requestID = w.Header().Get("X-Request-ID")
			requestMu.Unlock()
		}
	})
	server.StartTLS()
	t.Cleanup(server.Close)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OWNSOLID_CONFIG_DIR", dir)
	t.Setenv("OWNSOLID_SERVER", server.URL)
	ca, secret := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "password")
	config, err := json.Marshal(map[string]string{"ca_file": ca, "workspace_id": workspace})
	if err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string][]byte{
		ca:     pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}),
		secret: []byte(password), filepath.Join(dir, "config.json"): config,
	} {
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(success bool, want string, args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if bytes.Contains(out, []byte(password)) || strings.Contains(stderr.String(), password) {
			t.Fatal("history CLI exposed its login credential")
		}
		if success {
			if err != nil || stderr.Len() != 0 || !bytes.Contains(out, []byte(want)) {
				t.Fatalf("CLI success failed: %v, %s, %s", err, stderr.String(), out)
			}
		} else if err == nil || len(out) != 0 || !strings.Contains(stderr.String(), want) {
			t.Fatalf("CLI refusal failed: %v, %s, %s", err, stderr.String(), out)
		}
		return out
	}
	run(true, "", "login", "--email", "admin@example.invalid", "--password-file", secret)
	run(true, "Examples:", "monitoring", "history", "--help")
	recent := time.Now().UTC().Truncate(time.Minute).Add(-4 * time.Minute)
	old := recent.Add(-2 * time.Hour)
	var samples bytes.Buffer
	for _, stamp := range []time.Time{old, recent} {
		for index, cell := range []string{"down", "healthy"} {
			fmt.Fprintf(&samples, "up{job=%q,site_id=%q,cell_id=%q,node_id=%q,tenant_id=%q,workspace_id=%q} %d %d\n",
				"ownsolid-node", "site-1", cell, cell, tenant, workspace, index, stamp.UnixMilli())
		}
		fmt.Fprintf(&samples, "up{job=%q,site_id=%q,cell_id=%q,node_id=%q,tenant_id=%q,workspace_id=%q} 1 %d\n",
			"ownsolid-node", "site-1", "foreign", "foreign", foreignTenant, foreign, stamp.UnixMilli())
	}
	fixtureRequest(t, ctx, http.MethodPost, raw+"/api/v1/import/prometheus", &samples)
	fixtureRequest(t, ctx, http.MethodGet, raw+"/internal/force_flush", nil)
	replay := exec.CommandContext(ctx, os.Getenv("OWNSOLID_TEST_VMALERT"),
		"-rule=alerting/fleet-rollup-rules.yaml", "-datasource.url="+raw, "-remoteWrite.url="+long,
		"-replay.timeFrom="+old.Format(time.RFC3339), "-replay.timeTo="+old.Add(3*time.Minute).Format(time.RFC3339),
		"-replay.rulesDelay=0", "-replay.disableProgressBar")
	if log, err := replay.CombinedOutput(); err != nil {
		t.Fatalf("native rollup replay: %v\n%s", err, log)
	}
	fixtureRequest(t, ctx, http.MethodGet, long+"/internal/force_flush", nil)
	count := func() int {
		t.Helper()
		var n int
		if err := accounts.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot'").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	history := func(from time.Time, success bool, want string, extra ...string) []byte {
		t.Helper()
		before := count()
		args := append([]string{"monitoring", "history", "--from", from.Format(time.RFC3339),
			"--to", from.Add(2 * time.Minute).Format(time.RFC3339)}, extra...)
		out := run(success, want, args...)
		requestMu.Lock()
		id := requestID
		requestMu.Unlock()
		var audits int
		if err := accounts.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot' AND request_id=$1", id).Scan(&audits); err != nil {
			t.Fatal(err)
		}
		delta := 0
		if success {
			delta = 1
		}
		if count() != before+delta || audits != delta {
			t.Fatal("history success audit did not match the request outcome")
		}
		return out
	}
	for _, test := range []struct {
		from   time.Time
		tier   string
		points int
	}{{recent, "raw", 3}, {old, "rollup", 1}} {
		out := history(test.from, true, test.tier, "-o", "json")
		var value monitoring.FleetHistory
		if err := json.Unmarshal(out, &value); err != nil || value.Tier != test.tier || len(value.Cells) != 2 {
			t.Fatalf("history scope or tier changed: %v, %s", err, out)
		}
		for i, cell := range value.Cells {
			if cell.SiteID != "site-1" || cell.CellID != []string{"down", "healthy"}[i] || len(cell.Points) != test.points {
				t.Fatal("CLI filled a gap or included foreign data", cell)
			}
			for _, point := range cell.Points {
				if point.Observed != 1 || point.Reachable != uint64(i) {
					t.Fatal("CLI changed measured zero", point)
				}
			}
		}
	}
	history(recent, true, "REACHABLE")
	emptyOutput := history(old.Add(time.Minute), true, "", "-o", "yaml")
	var empty struct {
		Cells []any `yaml:"cells"`
	}
	if err := yaml.Unmarshal(emptyOutput, &empty); err != nil || empty.Cells == nil || len(empty.Cells) != 0 {
		t.Fatalf("empty YAML history changed: %v, %s", err, emptyOutput)
	}
	history(recent, false, "RESOURCE_NOT_FOUND", "--workspace", foreign)
	reads := rawReads.Load()
	longFailure.Store(true)
	history(old, false, "MONITORING_LONG_TIER_UNAVAILABLE", "-o", "json")
	if rawReads.Load() != reads {
		t.Fatal("CLI long-tier refusal attempted a raw fallback")
	}
	longFailure.Store(false)
	history(old, true, "rollup", "-o", "json")
	sessionPath := filepath.Join(dir, fmt.Sprintf("session-%x", sha256.Sum256([]byte(server.URL))))
	retained, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	run(true, "", "logout")
	if err := os.WriteFile(sessionPath, retained, 0600); err != nil {
		t.Fatal(err)
	}
	history(recent, false, "AUTH_SESSION_EXPIRED", "-o", "json")
	run(true, "", "login", "--email", "admin@example.invalid", "--password-file", secret)
	history(recent, true, "raw", "-o", "json")
}
