//go:build repository_integration && monitoring_integration && linux

package monitoring_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring"
	monitoringhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
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

	rawTLS, longTLS := newHistoryCertificate(t), newHistoryCertificate(t)
	nextTLS, deniedTLS := newHistoryCertificate(t), newHistoryCertificate(t)
	root := t.TempDir()
	caDirectory := t.TempDir()
	systemCA := filepath.Join(root, "trusted-sites.pem")
	var roots []byte
	for _, path := range []string{rawTLS.ca, longTLS.ca, nextTLS.ca} {
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
	raw, _, _ := startHistoryVMAt(t, g.ctx, "2d", "https", "-tls=true", "-tlsCertFile="+rawTLS.cert, "-tlsKeyFile="+rawTLS.key)
	long, _, _ := startHistoryVMAt(t, g.ctx, "90d", "https", "-tls=true", "-tlsCertFile="+longTLS.cert, "-tlsKeyFile="+longTLS.key, "-http.disableKeepAlive")
	policyPath := filepath.Join(root, "monitoring.json")
	policy := monitoring.Defaults()
	policy.FleetHistory = monitoring.FleetHistoryPolicy{
		RawOrigin: raw, LongOrigin: long, RawHorizonSeconds: 3600,
		MaxRangeSeconds: 90 * 86400, SettlingSeconds: 30,
	}
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, policyJSON, 0600); err != nil {
		t.Fatal(err)
	}
	g.config, err = monitoring.LoadEnvironment(map[string]string{monitoring.ConfigEnvironment: policyPath})
	if err != nil {
		t.Fatal(err)
	}
	g.rebuild()

	recent := time.Now().UTC().Truncate(time.Minute).Add(-4 * time.Minute)
	old := recent.Add(-2 * time.Hour)
	line := `up{job="ownsolid-node",site_id="test-site",cell_id="test-cell",node_id="fixture-node",tenant_id=%q,workspace_id=%q} 1 %d` + "\n"
	data := fmt.Sprintf(line, g.f.Tenant, g.f.Workspace, old.UnixMilli()) +
		fmt.Sprintf(line, g.f.Tenant, g.f.Workspace, recent.UnixMilli())
	fixtureRequest(t, g.ctx, http.MethodPost, raw+"/api/v1/import/prometheus", strings.NewReader(data))
	fixtureRequest(t, g.ctx, http.MethodGet, raw+"/internal/force_flush", nil)
	replay := exec.CommandContext(g.ctx, os.Getenv("OWNSOLID_TEST_VMALERT"),
		"-rule=alerting/fleet-rollup-rules.yaml", "-datasource.url="+raw, "-datasource.tlsCAFile="+rawTLS.ca,
		"-remoteWrite.url="+long, "-remoteWrite.tlsCAFile="+longTLS.ca,
		"-replay.timeFrom="+old.Format(time.RFC3339), "-replay.timeTo="+old.Add(3*time.Minute).Format(time.RFC3339),
		"-replay.rulesDelay=0", "-replay.disableProgressBar")
	replay.Env = append(os.Environ(), "SSL_CERT_FILE="+filepath.Join(caDirectory, "missing.pem"))
	if log, err := replay.CombinedOutput(); err != nil {
		t.Fatalf("recording rule over native TLS: %v\n%s", err, log)
	}
	fixtureRequest(t, g.ctx, http.MethodGet, long+"/internal/force_flush", nil)

	read := func(from time.Time) (monitoring.FleetHistory, error) {
		return readFleetHistoryCandidate(t, g, g.admin, g.f.Workspace, from, from.Add(2*time.Minute))
	}
	check := func(tier string, from time.Time) {
		t.Helper()
		out, err := read(from)
		if err != nil || out.Tier != tier || len(out.Cells) != 1 || out.Cells[0].CellID != "test-cell" ||
			len(out.Cells[0].Points) == 0 || out.Cells[0].Points[0].Reachable != 1 || out.Cells[0].Points[0].Observed != 1 {
			t.Fatalf("%s read using the installed policy and private CAs: %+v, %v", tier, out, err)
		}
	}
	check("raw", recent)
	check("rollup", old)

	rawQueries := historyRangeRequests(t, g.ctx, raw)
	audits := g.count("SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot'")
	replaceHistoryCertificate(t, g.ctx, long, longTLS, deniedTLS)
	out, err := read(old)
	code, status := httpserver.PublicProblem(err)
	if code != "MONITORING_LONG_TIER_UNAVAILABLE" || status != http.StatusServiceUnavailable ||
		out.Tier != "" || out.Cells != nil || historyRangeRequests(t, g.ctx, raw) != rawQueries ||
		g.count("SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot'") != audits {
		t.Fatalf("untrusted long tier returned data, fell back, or recorded success: %s %d %+v", code, status, out)
	}
	check("raw", recent)
	replaceHistoryCertificate(t, g.ctx, long, longTLS, nextTLS)
	check("rollup", old)
	if historyRangeRequests(t, g.ctx, raw) != rawQueries+1 ||
		g.count("SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot'") != audits+2 {
		t.Fatal("certificate replacement changed tier routing or audit accounting")
	}
	t.Log("same-origin certificate reload refused the untrusted leaf, kept raw available, and recovered rollup on the trusted replacement")
}

type historyCertificate struct {
	leaf          tls.Certificate
	ca, cert, key string
}

func newHistoryCertificate(t *testing.T) historyCertificate {
	t.Helper()
	leaf, ca, cert, key := smtpsink.Certificate(t, t.TempDir())
	return historyCertificate{leaf, ca, cert, key}
}

func replaceHistoryCertificate(t *testing.T, parent context.Context, origin string, active, expected historyCertificate) {
	t.Helper()
	for _, paths := range [][2]string{{expected.cert, active.cert}, {expected.key, active.key}} {
		data, err := os.ReadFile(paths[0])
		if err != nil {
			t.Fatal(err)
		}
		next := paths[1] + ".next"
		if err := os.WriteFile(next, data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(next, paths[1]); err != nil {
			t.Fatal(err)
		}
	}
	ca, err := os.ReadFile(expected.ca)
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(ca) {
		t.Fatal("fixture issuer is unavailable", err)
	}
	u, err := url.Parse(origin)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	defer cancel()
	dial := tls.Dialer{Config: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		attempt, stop := context.WithTimeout(ctx, time.Second)
		conn, err := dial.DialContext(attempt, "tcp", u.Host)
		stop()
		if err == nil {
			peer := conn.(*tls.Conn).ConnectionState().PeerCertificates
			_ = conn.Close()
			if len(peer) == 1 && bytes.Equal(peer[0].Raw, expected.leaf.Certificate[0]) {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("native listener did not present the expected replacement leaf")
		case <-tick.C:
		}
	}
}

func historyRangeRequests(t *testing.T, ctx context.Context, raw string) uint64 {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw+"/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatal("native metrics request returned", resp.Status)
	}
	rows := bufio.NewScanner(io.LimitReader(resp.Body, 1<<20))
	for rows.Scan() {
		if value, ok := strings.CutPrefix(rows.Text(), `vm_http_requests_total{path="/api/v1/query_range"} `); ok {
			count, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			return count
		}
	}
	t.Fatal("native range counter is unavailable", rows.Err())
	return 0
}
