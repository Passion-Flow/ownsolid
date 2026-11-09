//go:build alerting_integration && linux

package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type alertLabels map[string]string

type webhookBatch struct {
	Status string
	Alerts []struct{ Labels alertLabels }
}

func TestRackOutageDeliversOneOfflineNotification(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	work := t.TempDir()
	var mu sync.Mutex
	var batches []webhookBatch
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch webhookBatch
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&batch); err != nil {
			http.Error(w, "invalid alert batch", http.StatusBadRequest)
			return
		}
		mu.Lock()
		batches = append(batches, batch)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(sink.Close)

	receiver := filepath.Join(work, "webhook-url")
	writeFixture(t, receiver, []byte(sink.URL+"\n"))
	cfg, err := os.ReadFile("alertmanager.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg = bytes.ReplaceAll(cfg, []byte("/etc/ownsolid/alertmanager/webhook-url"), []byte(strconv.Quote(receiver)))
	cfgPath := filepath.Join(work, "alertmanager.yaml")
	writeFixture(t, cfgPath, cfg)

	client := &http.Client{Transport: &http.Transport{}, Timeout: 15 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	metrics := runAlertService(t, ctx, client, "OWNSOLID_TEST_VICTORIA_METRICS", "/health",
		"-storageDataPath="+filepath.Join(work, "metrics"), "-search.latencyOffset=0s", "-memory.allowedBytes=256MB")
	manager := runAlertService(t, ctx, client, "OWNSOLID_TEST_ALERTMANAGER", "/-/ready",
		"--config.file="+cfgPath, "--storage.path="+filepath.Join(work, "alertmanager"), "--cluster.listen-address=")

	now := time.Now()
	matchers := []map[string]any{
		{"name": "site_id", "value": "site-a", "isRegex": false, "isEqual": true},
		{"name": "cell_id", "value": "cell-c", "isRegex": false, "isEqual": true},
	}
	silence, err := json.Marshal(map[string]any{
		"matchers": matchers, "startsAt": now.Add(-time.Minute), "endsAt": now.Add(5 * time.Minute),
		"createdBy": "integration-test", "comment": "Planned cell maintenance",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := alertRequest(ctx, client, http.MethodPost, manager+"/api/v2/silences", "application/json", silence, nil); err != nil {
		t.Fatal(err)
	}

	var samples strings.Builder
	for i := range 40 {
		fmt.Fprintf(&samples, "up{job=\"ownsolid-node\",site_id=\"site-a\",cell_id=\"cell-a\",rack_id=\"rack-1\",node_id=\"node-%d\"} 0\n", i)
	}
	for _, cell := range []string{"cell-b", "cell-c"} {
		for node, value := range []int{0, 1} {
			fmt.Fprintf(&samples, "up{job=\"ownsolid-node\",site_id=\"site-a\",cell_id=\"%s\",rack_id=\"rack-1\",node_id=\"node-%d\"} %d\n", cell, node, value)
		}
	}
	// A metric without a trusted site never produces an alert.
	samples.WriteString("up{job=\"ownsolid-node\",cell_id=\"cell-a\",rack_id=\"rack-1\",node_id=\"unknown-site\"} 0\n")
	payload := []byte(samples.String())
	publish := func() {
		t.Helper()
		if err := alertRequest(ctx, client, http.MethodPost, metrics+"/api/v1/import/prometheus", "text/plain", payload, nil); err != nil {
			t.Fatal(err)
		}
	}
	publish()
	runAlertService(t, ctx, client, "OWNSOLID_TEST_VMALERT", "/health",
		"-rule=fleet-rules.yaml", "-rule.evalDelay=0s", "-datasource.queryStep=15s", "-datasource.url="+metrics,
		"-notifier.url="+manager, "-remoteWrite.url="+metrics, "-remoteRead.url="+metrics)

	for {
		publish()
		mu.Lock()
		received := append([]webhookBatch(nil), batches...)
		mu.Unlock()
		if len(received) >= 2 {
			verifyRackDeliveries(t, received)
			verifyRackInhibition(t, ctx, client, manager)
			t.Log("one critical rack notification, 40 inhibited node alerts, one separate node notification, one silenced node alert")
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("expected two notifications, received %v: %v", received, ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}

func verifyRackDeliveries(t *testing.T, batches []webhookBatch) {
	t.Helper()
	expected := map[string]string{"cell-a": "OwnsolidRackUnreachable", "cell-b": "OwnsolidNodeUnreachable"}
	if len(batches) != len(expected) {
		t.Fatalf("received %d notifications, expected %d", len(batches), len(expected))
	}
	for _, batch := range batches {
		if batch.Status != "firing" || len(batch.Alerts) != 1 {
			t.Fatalf("expected one firing alert in each notification: %+v", batch)
		}
		labels := batch.Alerts[0].Labels
		cell := labels["cell_id"]
		if labels["site_id"] != "site-a" || labels["rack_id"] != "rack-1" ||
			expected[cell] == "" || labels["alertname"] != expected[cell] {
			t.Fatalf("unexpected notification: %v", labels)
		}
		if cell == "cell-a" && labels["severity"] != "critical" {
			t.Fatalf("rack notification severity was %q", labels["severity"])
		}
		delete(expected, cell)
	}
}

func verifyRackInhibition(t *testing.T, ctx context.Context, client *http.Client, manager string) {
	t.Helper()
	var alerts []struct {
		Labels alertLabels
		Status struct {
			State       string
			InhibitedBy []string
			SilencedBy  []string
		}
	}
	if err := alertRequest(ctx, client, http.MethodGet, manager+"/api/v2/alerts", "", nil, &alerts); err != nil {
		t.Fatal(err)
	}
	inhibited, silenced := 0, 0
	for _, a := range alerts {
		switch {
		case a.Labels["cell_id"] == "cell-a" && a.Labels["alertname"] == "OwnsolidNodeUnreachable":
			if a.Status.State != "suppressed" || len(a.Status.InhibitedBy) != 1 {
				t.Fatalf("rack node was not inhibited: %+v", a)
			}
			inhibited++
		case a.Labels["cell_id"] == "cell-c":
			if a.Status.State != "suppressed" || len(a.Status.SilencedBy) != 1 {
				t.Fatalf("maintenance silence did not apply: %+v", a)
			}
			silenced++
		}
	}
	if len(alerts) != 43 || inhibited != 40 || silenced != 1 {
		t.Fatalf("alerts=%d inhibited=%d silenced=%d", len(alerts), inhibited, silenced)
	}
}

func runAlertService(t *testing.T, ctx context.Context, client *http.Client, env, health string, flags ...string) string {
	t.Helper()
	path := os.Getenv(env)
	if !filepath.IsAbs(path) {
		t.Fatalf("%s must name an absolute path to a pinned test binary", env)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if env == "OWNSOLID_TEST_ALERTMANAGER" || env == "OWNSOLID_TEST_BLACKBOX_EXPORTER" {
		flags = append(flags, "--web.listen-address="+addr)
	} else {
		flags = append(flags, "-httpListenAddr="+addr)
	}

	logFile, err := os.CreateTemp(t.TempDir(), "service-log-")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(path, flags...)
	cmd.Env = append(os.Environ(), "GOMAXPROCS=2")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	err = cmd.Start()
	_ = logFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-exited:
			if err != nil {
				t.Errorf("%s: %v", env, err)
			}
		case <-time.After(30 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
			t.Errorf("%s did not stop on SIGTERM", env)
		}
		if t.Failed() {
			log, _ := os.ReadFile(logFile.Name())
			t.Logf("%s log:\n%s", env, log)
		}
	})
	url := "http://" + addr
	for {
		err := alertRequest(ctx, client, http.MethodGet, url+health, "", nil, nil)
		if err == nil {
			return url
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s failed readiness: %v", env, err)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func alertRequest(ctx context.Context, client *http.Client, method, url, mediaType string, data []byte, result any) error {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	if mediaType != "" {
		req.Header.Set("Content-Type", mediaType)
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return fmt.Errorf("%s %s returned HTTP %d: %s", method, url, res.StatusCode, body)
	}
	if result != nil {
		return json.NewDecoder(res.Body).Decode(result)
	}
	_, err = io.Copy(io.Discard, res.Body)
	return err
}

func writeFixture(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFleetRollupsWriteOnlyAggregatesToTheLongTier(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	work := t.TempDir()
	client := &http.Client{Transport: &http.Transport{}, Timeout: 15 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	short := runAlertService(t, ctx, client, "OWNSOLID_TEST_VICTORIA_METRICS", "/health",
		"-storageDataPath="+filepath.Join(work, "short"), "-search.latencyOffset=0s", "-memory.allowedBytes=256MB", "-retentionPeriod=2d")
	long := runAlertService(t, ctx, client, "OWNSOLID_TEST_VICTORIA_METRICS", "/health",
		"-storageDataPath="+filepath.Join(work, "long"), "-search.latencyOffset=0s", "-memory.allowedBytes=256MB", "-retentionPeriod=90d")
	runAlertService(t, ctx, client, "OWNSOLID_TEST_VMALERT", "/health",
		"-rule=fleet-rollup-rules.yaml", "-rule.evalDelay=0s", "-datasource.queryStep=15s",
		"-datasource.url="+short, "-remoteWrite.url="+long)

	payload := []byte(strings.Join([]string{
		"up{job=\"ownsolid-node\",site_id=\"site-a\",cell_id=\"cell-a\",node_id=\"up\"} 1",
		"up{job=\"ownsolid-node\",site_id=\"site-a\",cell_id=\"cell-a\",node_id=\"down\"} 0",
		"up{job=\"ownsolid-node\",site_id=\"site-a\",cell_id=\"cell-b\",node_id=\"down\"} 0",
		"up{job=\"ownsolid-node\",site_id=\"site-a\",node_id=\"unknown-cell\"} 1",
	}, "\n") + "\n")
	for {
		if err := alertRequest(ctx, client, http.MethodPost, short+"/api/v1/import/prometheus", "text/plain", payload, nil); err != nil {
			t.Fatal(err)
		}
		observed := rollupQuery(t, ctx, client, long, "ownsolid:cell_nodes_observed:count")
		reachable := rollupQuery(t, ctx, client, long, "ownsolid:cell_nodes_reachable:count")
		if len(observed) != 0 && len(reachable) != 0 {
			if len(observed) != 2 || observed["cell-a"] != "2" || observed["cell-b"] != "1" ||
				len(reachable) != 2 || reachable["cell-a"] != "1" || reachable["cell-b"] != "0" {
				t.Fatalf("incorrect cell rollups: observed=%v reachable=%v", observed, reachable)
			}
			if raw := rollupQuery(t, ctx, client, long, "up"); len(raw) != 0 {
				t.Fatalf("raw data leaked to the long tier: %v", raw)
			}
			if misplaced := rollupQuery(t, ctx, client, short, "ownsolid:cell_nodes_observed:count"); len(misplaced) != 0 {
				t.Fatalf("rollup written to the raw tier: %v", misplaced)
			}
			t.Log("long tier stores two scoped counts per cell, including a measured zero, and no raw up series")
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("rollups did not arrive: %v", ctx.Err())
		case <-time.After(3 * time.Second):
		}
	}
}

func rollupQuery(t *testing.T, ctx context.Context, client *http.Client, origin, expr string) map[string]string {
	t.Helper()
	var reply struct {
		Status string
		Data   struct {
			Result []struct {
				Metric map[string]string
				Value  []json.RawMessage
			}
		}
	}
	path := origin + "/api/v1/query?nocache=1&query=" + url.QueryEscape(expr)
	if err := alertRequest(ctx, client, http.MethodGet, path, "", nil, &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Status != "success" {
		t.Fatalf("query %q failed", expr)
	}
	result := make(map[string]string, len(reply.Data.Result))
	for _, row := range reply.Data.Result {
		cell := row.Metric["cell_id"]
		if len(row.Value) != 2 || cell == "" || row.Metric["site_id"] != "site-a" {
			t.Fatalf("invalid cell rollup for %q: %+v", expr, row)
		}
		if _, duplicate := result[cell]; duplicate {
			t.Fatalf("duplicate series in cell %s for %q", cell, expr)
		}
		var value string
		if err := json.Unmarshal(row.Value[1], &value); err != nil {
			t.Fatal(err)
		}
		result[cell] = value
	}
	return result
}
