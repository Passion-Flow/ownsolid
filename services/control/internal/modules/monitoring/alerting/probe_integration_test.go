//go:build alerting_integration && linux

package alerting

import (
	"context"
	"crypto/tls"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const platformProbeJob = "ownsolid-platform-readiness"

func TestPlatformProbesWriteScopedReadinessMetrics(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 55*time.Second)
	defer cancel()
	work := t.TempDir()
	logs, client := runJournalStore(t, ctx, work)
	logsOrigin, err := url.Parse(logs)
	if err != nil {
		t.Fatal(err)
	}
	reverseProxy := httputil.NewSingleHostReverseProxy(logsOrigin)
	reverseProxy.Transport = client.Transport

	var state atomic.Int32
	state.Store(http.StatusOK)
	var redirectCalls atomic.Int32
	cert, key, _ := relayFixtureCertificate(t, work)
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	platform := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			redirectCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/health/ready" || r.Header.Get("Authorization") != "" {
			http.Error(w, "unexpected probe request", http.StatusBadRequest)
			return
		}
		switch code := int(state.Load()); code {
		case http.StatusOK:
			r.URL.Path = "/health"
			reverseProxy.ServeHTTP(w, r)
		case http.StatusTemporaryRedirect:
			http.Redirect(w, r, "/redirected", code)
		default:
			http.Error(w, "not ready", code)
		}
	}))
	platform.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	platform.StartTLS()
	t.Cleanup(platform.Close)

	var untrustedCalls atomic.Int32
	untrusted := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		untrustedCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(untrusted.Close)

	moduleFile := filepath.Join(work, "probe-modules.yaml")
	module := readProbeFixture(t, "platform-probe-modules.example.yaml",
		"/etc/ownsolid/monitoring/site-ca.pem", strconv.Quote(cert))
	writeFixture(t, moduleFile, module)
	exporter := runAlertService(t, ctx, client, "OWNSOLID_TEST_BLACKBOX_EXPORTER", "/-/healthy",
		"--config.file="+moduleFile)

	scrapeFile := filepath.Join(work, "probe-scrape.yaml")
	scrape := readProbeFixture(t, "platform-probe-scrape.example.yaml",
		"https://admin.site.invalid/health/ready", platform.URL+"/health/ready",
		"https://portal.site.invalid/health/ready", untrusted.URL+"/health/ready",
		"127.0.0.1:9115", strings.TrimPrefix(exporter, "http://"),
		"scrape_interval: 30s", "scrape_interval: 2s",
		"scrape_timeout: 5s", "scrape_timeout: 2s")
	writeFixture(t, scrapeFile, scrape)
	metrics := runAlertService(t, ctx, client, "OWNSOLID_TEST_VICTORIA_METRICS", "/health",
		"-storageDataPath="+filepath.Join(work, "metrics"), "-search.latencyOffset=0s", "-memory.allowedBytes=256MB")
	agent := runAlertService(t, ctx, client, "OWNSOLID_TEST_VMAGENT", "/health",
		"-promscrape.config="+scrapeFile, "-remoteWrite.url="+metrics+"/api/v1/write",
		"-remoteWrite.tmpDataPath="+filepath.Join(work, "remote-write"), "-remoteWrite.flushInterval=1s")

	const good, bad = "admin-readiness", "portal-readiness"
	awaitProbeValue(t, ctx, client, metrics, "probe_success", good, "1")
	awaitProbeValue(t, ctx, client, metrics, "probe_http_status_code", good, "200")
	awaitProbeValue(t, ctx, client, metrics, "probe_success", bad, "0")
	awaitProbeValue(t, ctx, client, metrics, "probe_http_status_code", bad, "0")

	state.Store(http.StatusServiceUnavailable)
	awaitProbeValue(t, ctx, client, metrics, "probe_http_status_code", good, "503")
	awaitProbeValue(t, ctx, client, metrics, "probe_success", good, "0")
	awaitProbeValue(t, ctx, client, metrics, "up", good, "1")

	state.Store(http.StatusTemporaryRedirect)
	awaitProbeValue(t, ctx, client, metrics, "probe_http_status_code", good, "307")
	awaitProbeValue(t, ctx, client, metrics, "probe_success", good, "0")

	state.Store(http.StatusOK)
	awaitProbeValue(t, ctx, client, metrics, "probe_success", good, "1")
	awaitProbeValue(t, ctx, client, metrics, "probe_http_status_code", good, "200")
	if redirectCalls.Load() != 0 || untrustedCalls.Load() != 0 {
		t.Fatalf("unexpected HTTP requests: redirect target %d, untrusted server %d", redirectCalls.Load(), untrustedCalls.Load())
	}

	var series struct {
		Data []map[string]string
	}
	selector := fmt.Sprintf("{job=%q}", platformProbeJob)
	query := url.Values{"match[]": {selector}}.Encode()
	if err := alertRequest(ctx, client, http.MethodGet, metrics+"/api/v1/series?"+query, "", nil, &series); err != nil {
		t.Fatal(err)
	}
	names := map[string][]string{}
	for _, labels := range series.Data {
		endpoint, name := labels["endpoint"], labels["__name__"]
		expected := map[string]string{
			"site_id": "site-a", "endpoint": endpoint, "instance": endpoint, "job": platformProbeJob, "__name__": name,
		}
		if (endpoint != good && endpoint != bad) || !maps.Equal(labels, expected) {
			t.Fatalf("unexpected platform probe labels: %v", labels)
		}
		names[endpoint] = append(names[endpoint], name)
	}
	for _, endpoint := range []string{good, bad} {
		slices.Sort(names[endpoint])
		for _, name := range names[endpoint] {
			if strings.HasPrefix(name, "probe_") && name != "probe_success" && name != "probe_duration_seconds" && name != "probe_http_status_code" {
				t.Fatalf("unexpected probe series %q", name)
			}
		}
		for _, name := range []string{"probe_success", "probe_duration_seconds", "probe_http_status_code", "up"} {
			if !slices.Contains(names[endpoint], name) {
				t.Fatalf("missing %q for %s: %v", name, endpoint, names[endpoint])
			}
		}
	}
	t.Logf("VictoriaLogs %s via %s, exporter %s, vmagent %s, VictoriaMetrics %s: %d series per endpoint (%v)",
		logs, platform.URL, exporter, agent, metrics, len(names[good]), names[good])
}

func readProbeFixture(t *testing.T, path string, replacements ...string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(replacements); i += 2 {
		if strings.Count(string(raw), replacements[i]) != 1 {
			t.Fatalf("expected one fixture placeholder %q in %s", replacements[i], path)
		}
	}
	return []byte(strings.NewReplacer(replacements...).Replace(string(raw)))
}

func awaitProbeValue(t *testing.T, ctx context.Context, client *http.Client, metrics, name, endpoint, want string) {
	t.Helper()
	query := url.Values{"query": {fmt.Sprintf("%s{job=%q,endpoint=%q}", name, platformProbeJob, endpoint)}}.Encode()
	timer := time.NewTicker(200 * time.Millisecond)
	defer timer.Stop()
	var observed any
	for {
		var vector struct {
			Data struct {
				Result []struct{ Value [2]any }
			}
		}
		err := alertRequest(ctx, client, http.MethodGet, metrics+"/api/v1/query?"+query, "", nil, &vector)
		if err == nil && len(vector.Data.Result) == 1 {
			observed = vector.Data.Result[0].Value[1]
			if observed == want {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for %s=%s on %s, last value %v: %v", name, want, endpoint, observed, ctx.Err())
		case <-timer.C:
		}
	}
}
