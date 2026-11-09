//go:build repository_integration && monitoring_integration && linux

package monitoring_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
	"github.com/google/uuid"
)

const historyTLSChild = "OWNSOLID_TEST_HISTORY_TLS_CHILD"

func TestFleetHistoryPrivateTLS(t *testing.T) {
	// System trust is initialized once per process. Exercise the real client in
	// a fresh process without changing the host or other fixtures' trust.
	if os.Getenv(historyTLSChild) != "1" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 80*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestFleetHistoryPrivateTLS$", "-test.v")
		cmd.Env = append(os.Environ(), historyTLSChild+"=1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("private TLS fixture: %v\n%s", err, output)
		}
		t.Log(string(output))
		return
	}

	_, rawCA, rawCert, rawKey := smtpsink.Certificate(t, t.TempDir())
	_, longCA, longCert, longKey := smtpsink.Certificate(t, t.TempDir())
	root := t.TempDir()
	caDirectory := t.TempDir()
	systemCA := filepath.Join(root, "trusted-sites.pem")
	var roots []byte
	for _, path := range []string{rawCA, longCA} {
		pem, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		roots = append(roots, pem...)
	}
	if err := os.WriteFile(systemCA, roots, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", systemCA)
	t.Setenv("SSL_CERT_DIR", caDirectory)
	t.Cleanup(http.DefaultClient.CloseIdleConnections)

	g := setup(t, false)
	raw, _, _ := startHistoryVMAt(t, g.ctx, "2d", "https", "-tls=true", "-tlsCertFile="+rawCert, "-tlsKeyFile="+rawKey)
	long, _, _ := startHistoryVMAt(t, g.ctx, "90d", "https", "-tls=true", "-tlsCertFile="+longCert, "-tlsKeyFile="+longKey)
	policyPath := filepath.Join(root, "monitoring.json")
	load := func(longOrigin string) {
		t.Helper()
		policy := monitoring.Defaults()
		policy.FleetHistory = monitoring.FleetHistoryPolicy{
			RawOrigin: raw, LongOrigin: longOrigin, RawHorizonSeconds: 3600,
			MaxRangeSeconds: 90 * 86400, SettlingSeconds: 30,
		}
		data, err := json.Marshal(policy)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(policyPath, data, 0600); err != nil {
			t.Fatal(err)
		}
		g.config, err = monitoring.LoadEnvironment(map[string]string{monitoring.ConfigEnvironment: policyPath})
		if err != nil {
			t.Fatal(err)
		}
		g.rebuild()
	}
	load(long)

	recent := time.Now().UTC().Truncate(time.Minute).Add(-4 * time.Minute)
	old := recent.Add(-2 * time.Hour)
	line := `up{job="ownsolid-node",site_id="test-site",cell_id="test-cell",node_id="fixture-node",tenant_id=%q,workspace_id=%q} 1 %d` + "\n"
	data := fmt.Sprintf(line, g.f.Tenant, g.f.Workspace, old.UnixMilli()) +
		fmt.Sprintf(line, g.f.Tenant, g.f.Workspace, recent.UnixMilli())
	fixtureRequest(t, g.ctx, http.MethodPost, raw+"/api/v1/import/prometheus", strings.NewReader(data))
	fixtureRequest(t, g.ctx, http.MethodGet, raw+"/internal/force_flush", nil)
	replay := exec.CommandContext(g.ctx, os.Getenv("OWNSOLID_TEST_VMALERT"),
		"-rule=alerting/fleet-rollup-rules.yaml", "-datasource.url="+raw, "-datasource.tlsCAFile="+rawCA,
		"-remoteWrite.url="+long, "-remoteWrite.tlsCAFile="+longCA,
		"-replay.timeFrom="+old.Format(time.RFC3339), "-replay.timeTo="+old.Add(3*time.Minute).Format(time.RFC3339),
		"-replay.rulesDelay=0", "-replay.disableProgressBar")
	replay.Env = append(os.Environ(), "SSL_CERT_FILE="+filepath.Join(caDirectory, "missing.pem"))
	if log, err := replay.CombinedOutput(); err != nil {
		t.Fatalf("recording rule over native TLS: %v\n%s", err, log)
	}
	fixtureRequest(t, g.ctx, http.MethodGet, long+"/internal/force_flush", nil)

	for _, tc := range []struct {
		tier string
		from time.Time
	}{{"raw", recent}, {"rollup", old}} {
		out, err := g.s.GetFleetHistory(g.ctx, g.admin, g.f.Workspace, tc.from, tc.from.Add(2*time.Minute), uuid.NewString())
		if err != nil || out.Tier != tc.tier || len(out.Cells) != 1 || out.Cells[0].CellID != "test-cell" ||
			len(out.Cells[0].Points) == 0 || out.Cells[0].Points[0].Reachable != 1 || out.Cells[0].Points[0].Observed != 1 {
			t.Fatalf("%s read using the installed policy and private CAs: %+v, %v", tc.tier, out, err)
		}
	}

	var untrustedRequests atomic.Int32
	untrusted := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		untrustedRequests.Add(1)
	}))
	t.Cleanup(untrusted.Close)
	load(untrusted.URL)
	audits := g.count("SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot'")
	out, err := g.s.GetFleetHistory(g.ctx, g.admin, g.f.Workspace, old, old.Add(2*time.Minute), uuid.NewString())
	if err == nil || err.Error() != "MONITORING_LONG_TIER_UNAVAILABLE" || out.Tier != "" || out.Cells != nil ||
		untrustedRequests.Load() != 0 || g.count("SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot'") != audits {
		t.Fatalf("untrusted long tier returned data or emitted a successful audit: %+v, %v", out, err)
	}
	t.Log("Control and vmalert used verified native TLS on both tiers, and an untrusted long tier received no query")
}
