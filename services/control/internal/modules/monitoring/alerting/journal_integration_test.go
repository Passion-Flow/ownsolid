//go:build alerting_integration && linux

package alerting

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/telemetrystore"
	"go.yaml.in/yaml/v3"
)

const journalRule = "node-journal-rules.yaml"
const journalAlert = "OwnsolidNodeExporterJournalErrors"
const journalFixtureMessage = "sensitive fixture text"

func TestNodeJournalRuleDeliversAndResolvesByWorkspace(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 55*time.Second)
	defer cancel()
	work := t.TempDir()
	logs, client := runJournalStore(t, ctx, work)
	store, err := telemetrystore.NewClient(logs, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-10 * time.Second)
	write := func(tenant, workspace, node string, at time.Time, priority int, age time.Duration, removed bool) {
		t.Helper()
		r := telemetrystore.Record{
			Scope: telemetrystore.Scope{TenantID: tenant, WorkspaceID: workspace, NodeID: node, Component: wire.NodeExporter},
			Time:  at, TaskID: "fixture-task", Removed: removed,
			Body: journalFixtureBody(t, at, priority, age, journalFixtureMessage),
		}
		if err := store.Insert(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	const tenantA, tenantB, shared, other = "tenant-a", "tenant-b", "shared", "other"
	write(tenantA, shared, "node-1", start, 3, time.Second, false)
	write(tenantA, shared, "node-1", start.Add(time.Second), 3, time.Second, false)
	write(tenantA, shared, "node-2", start, 3, time.Second, false)
	write(tenantA, shared, "node-3", start, 3, time.Second, false)
	write(tenantA, shared, "node-4", start, 4, time.Second, false)
	write(tenantA, shared, "node-5", start, 3, 6*time.Minute, false)
	write(tenantA, shared, "node-6", start, 3, -time.Minute, false)
	write(tenantA, shared, "node-7", start.Add(-6*time.Minute), 3, time.Second, false)
	write(tenantA, shared, "node-8", start, 3, time.Second, false)
	write(tenantA, shared, "node-8", start.Add(time.Second), 3, time.Second, true)
	write(tenantA, shared, "node-9", start, 3, time.Second, false)
	write(tenantA, shared, "node-9", start.Add(time.Second), 4, time.Second, false)
	for _, node := range []string{"node-1", "node-2"} {
		write(tenantA, other, node, start, 3, time.Second, false)
		write(tenantB, shared, node, start, 3, time.Second, false)
	}
	// Bypass the Control writer solely to put invalid records in the test store.
	for _, field := range []string{"tenant_id", "workspace_id", "node_id", "_msg"} {
		var invalid bytes.Buffer
		for _, node := range []string{"node-1", "node-2", "node-3"} {
			row := journalWireRow(tenantA, shared, "invalid-"+node, start, journalFixtureBody(t, start, 3, time.Second, journalFixtureMessage))
			delete(row, field)
			if err := json.NewEncoder(&invalid).Encode(row); err != nil {
				t.Fatal(err)
			}
		}
		importJournalRows(t, ctx, client, logs, invalid.Bytes())
	}
	flushJournalStore(t, ctx, client, logs)
	stats := queryJournalStats(t, ctx, client, logs, readJournalExpr(t))
	if len(stats) != 1 || stats[0].Metric["tenant_id"] != tenantA ||
		stats[0].Metric["workspace_id"] != shared || stats[0].Value[1] != "3" {
		t.Fatalf("unexpected baseline result: %+v", stats)
	}

	deliveries := make(chan relayWebhook, 8)
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch relayWebhook
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBytes))
		if err != nil || json.Unmarshal(raw, &batch) != nil || bytes.Contains(raw, []byte(journalFixtureMessage)) {
			http.Error(w, "invalid or sensitive alert payload", http.StatusBadRequest)
			return
		}
		select {
		case deliveries <- batch:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "too many notifications", http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(sink.Close)
	amPath := filepath.Join(work, "alertmanager.yaml")
	writeFixture(t, amPath, []byte(fmt.Sprintf("route:\n  receiver: fixture\n  group_by: [tenant_id, workspace_id, component, alertname]\n  group_wait: 1s\n  group_interval: 2s\n  repeat_interval: 1h\nreceivers:\n  - name: fixture\n    webhook_configs:\n      - url: %q\n        send_resolved: true\n", sink.URL)))
	manager := runAlertService(t, ctx, client, "OWNSOLID_TEST_ALERTMANAGER", "/-/ready",
		"--config.file="+amPath, "--storage.path="+filepath.Join(work, "alertmanager"), "--cluster.listen-address=")
	// Keep the production expression and labels intact. Accelerate the test's
	// scheduling only, so the test need not wait multiple minutes.
	raw, err := os.ReadFile(journalRule)
	if err != nil || bytes.Count(raw, []byte("interval: 1m")) != 1 {
		t.Fatalf("cannot find the production journal rule interval: %v", err)
	}
	rulePath := filepath.Join(work, journalRule)
	writeFixture(t, rulePath, bytes.Replace(raw, []byte("interval: 1m"), []byte("interval: 2s"), 1))
	evaluator := runAlertService(t, ctx, client, "OWNSOLID_TEST_VMALERT", "/health",
		"-rule="+rulePath, "-rule.evalDelay=0s", "-datasource.url="+logs, "-notifier.url="+manager)

	await := func(tenant, status string) {
		t.Helper()
		select {
		case batch := <-deliveries:
			if batch.Status != status || len(batch.Alerts) != 1 {
				t.Fatalf("expected one %s alert for %s, got %+v", status, tenant, batch)
			}
			labels := batch.Alerts[0].Labels
			if labels["alertname"] != journalAlert || labels["severity"] != "warning" ||
				labels["tenant_id"] != tenant || labels["workspace_id"] != shared ||
				labels["component"] != wire.NodeExporter || labels["node_id"] != "" || labels["task_id"] != "" {
				t.Fatalf("unexpected %s alert labels: %+v", status, labels)
			}
		case <-ctx.Done():
			t.Fatalf("waiting for %s alert for %s: %v", status, tenant, ctx.Err())
		}
	}
	await(tenantA, "firing")
	write(tenantB, shared, "node-3", time.Now(), 3, time.Second, false)
	flushJournalStore(t, ctx, client, logs)
	await(tenantB, "firing")
	write(tenantA, shared, "node-3", time.Now(), 3, time.Second, true)
	flushJournalStore(t, ctx, client, logs)
	await(tenantA, "resolved")
	stats = queryJournalStats(t, ctx, client, logs, readJournalExpr(t))
	if len(stats) != 1 || stats[0].Metric["tenant_id"] != tenantB || stats[0].Value[1] != "3" {
		t.Fatalf("another tenant changed after removal: %+v", stats)
	}
	t.Logf("VictoriaLogs %s, vmalert %s and Alertmanager %s delivered isolated workspace alerts and one resolution", logs, evaluator, manager)
}

func TestNodeJournalRuleTenThousandNodes(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
	defer cancel()
	work := t.TempDir()
	logs, client := runJournalStore(t, ctx, work)
	at := time.Now().Add(-10 * time.Second)
	const tenants, workspaces, nodes = 10, 10, 100
	var inputBytes int
	for tenant := range tenants {
		for workspace := range workspaces {
			var input bytes.Buffer
			enc := json.NewEncoder(&input)
			for node := range nodes {
				digest := sha256.Sum256(fmt.Appendf(nil, "%d/%d/%d", tenant, workspace, node))
				message := fmt.Sprintf("Failed to open node exporter collector: %x", digest)
				body := journalFixtureBody(t, at, 3, time.Second, message)
				row := journalWireRow(fmt.Sprintf("tenant-%d", tenant), fmt.Sprintf("workspace-%d", workspace), fmt.Sprintf("node-%d", node), at, body)
				if err := enc.Encode(row); err != nil {
					t.Fatal(err)
				}
			}
			importJournalRows(t, ctx, client, logs, input.Bytes())
			inputBytes += input.Len()
		}
	}
	flushJournalStore(t, ctx, client, logs)
	expr := readJournalExpr(t)
	const iterations = 20
	latencies := make([]time.Duration, iterations)
	for i := range latencies {
		started := time.Now()
		stats := queryJournalStats(t, ctx, client, logs, expr)
		latencies[i] = time.Since(started)
		if len(stats) != tenants*workspaces {
			t.Fatalf("expected %d affected workspaces, got %d", tenants*workspaces, len(stats))
		}
		for _, point := range stats {
			if point.Value[1] != "100" {
				t.Fatalf("unexpected number of nodes for %+v", point)
			}
		}
	}
	slices.Sort(latencies)
	var storedBytes int64
	err := filepath.WalkDir(filepath.Join(work, "logs"), func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		info, err := entry.Info()
		if err == nil {
			storedBytes += info.Size()
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: %d nodes, %d workspaces, %d JSON-line bytes, %d VictoriaLogs data file bytes after flush, %d query samples (p50 %s, p95 %s, max %s)",
		logs, tenants*workspaces*nodes, tenants*workspaces, inputBytes, storedBytes, iterations, latencies[9], latencies[18], latencies[19])
}

func runJournalStore(t *testing.T, ctx context.Context, work string) (string, *http.Client) {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{}, Timeout: 10 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	logs := runAlertService(t, ctx, client, "OWNSOLID_TEST_VICTORIA_LOGS", "/health",
		"-storageDataPath="+filepath.Join(work, "logs"), "-memory.allowedBytes=256MB")
	return logs, client
}

func journalFixtureBody(t *testing.T, at time.Time, priority int, age time.Duration, message string) json.RawMessage {
	t.Helper()
	logs := wire.ComponentLogs{
		Source: "systemd_journal", Available: true, ObservedAt: at, Since: at.Add(-time.Hour),
	}
	for range 8 {
		logs.Entries = append(logs.Entries, wire.ComponentLogEntry{
			Timestamp: at.Add(-age), Priority: priority, Message: message, Sanitized: true,
		})
	}
	if err := logs.Validate(); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(logs)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func journalWireRow(tenant, workspace, node string, at time.Time, body json.RawMessage) map[string]string {
	return map[string]string{
		"_time": at.Format(time.RFC3339Nano), "_msg": string(body), "tenant_id": tenant,
		"workspace_id": workspace, "node_id": node, "component": wire.NodeExporter,
	}
}

func importJournalRows(t *testing.T, ctx context.Context, client *http.Client, logs string, body []byte) {
	t.Helper()
	path := "/insert/jsonline?_stream_fields=tenant_id,workspace_id,node_id,component"
	if err := alertRequest(ctx, client, http.MethodPost, logs+path, "application/stream+json", body, nil); err != nil {
		t.Fatal(err)
	}
}

func flushJournalStore(t *testing.T, ctx context.Context, client *http.Client, logs string) {
	t.Helper()
	if err := alertRequest(ctx, client, http.MethodPost, logs+"/internal/force_flush", "", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func readJournalExpr(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(journalRule)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Groups []struct {
			Rules []struct{ Expr string }
		}
	}
	if err := yaml.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Groups) != 1 || len(config.Groups[0].Rules) != 1 {
		t.Fatal("expected one journal rule")
	}
	return strings.TrimSpace(config.Groups[0].Rules[0].Expr)
}

type journalStatsPoint struct {
	Metric map[string]string
	Value  [2]any
}

func queryJournalStats(t *testing.T, ctx context.Context, client *http.Client, logs, expr string) []journalStatsPoint {
	t.Helper()
	var result struct {
		Status string
		Data   struct{ Result []journalStatsPoint }
	}
	query := url.Values{"query": {expr}}.Encode()
	if err := alertRequest(ctx, client, http.MethodGet, logs+"/select/logsql/stats_query?"+query, "", nil, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Fatalf("unexpected query status %q", result.Status)
	}
	return result.Data.Result
}
