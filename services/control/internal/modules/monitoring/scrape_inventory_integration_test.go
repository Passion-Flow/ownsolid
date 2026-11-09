//go:build repository_integration && monitoring_integration && linux

package monitoring_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

func TestNodeScrapeInventoryThroughVmagent(t *testing.T) {
	g := setup(t, true)
	node, handshake := g.enroll(agent.SystemdComponentsProfile, "scrape target")
	otherWorkspace := uuid.NewString()
	g.exec("INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Second scrape Workspace','active')", "", otherWorkspace, g.f.Tenant)
	join, e := g.n.MintForProfile(g.ctx, g.admin, otherWorkspace, "other scrape target", agent.SystemdComponentsProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	foreign, e := g.n.Enroll(g.ctx, agent.Enrollment{JoinToken: join.Token, Handshake: handshake}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}

	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "node_memory_MemAvailable_bytes{tenant_id=\"spoofed\",workspace_id=\"spoofed\",site_id=\"spoofed\",cell_id=\"spoofed\",node_id=\"spoofed\"} 42\n")
	}))
	t.Cleanup(target.Close)
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	policy := filepath.Join(dir, "policy.json")
	writeFixture := func(path string, data []byte) {
		t.Helper()
		if e := os.WriteFile(path, data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	raw := []byte(fmt.Sprintf(`{"format_version":1,"workspace_id":%q,"allowed_networks":["127.0.0.0/8"],"endpoints":{%q:%q,%q:"127.0.0.2:1"}}`,
		g.f.Workspace, node.NodeID, target.Listener.Addr().String(), foreign.NodeID))
	writeFixture(policy, raw)
	name := "nodes-" + g.f.Workspace + ".json"
	path := filepath.Join(dir, name)
	refresh := func(a identity.Auth) (int, error) {
		return g.s.RefreshNodeScrapeInventory(g.ctx, a, g.f.Workspace, policy, dir, uuid.NewString())
	}
	expectRefresh := func(count int) {
		t.Helper()
		if actual, e := refresh(g.admin); e != nil || actual != count {
			t.Fatalf("wanted %d targets, got %d, %v", count, actual, e)
		}
	}
	expectRefresh(1)
	before, e := os.ReadFile(path)
	if e != nil || bytes.Contains(before, []byte(foreign.NodeID)) {
		t.Fatalf("foreign target reached the output: %v", e)
	}
	if count, e := refresh(g.portal); e == nil || e.Error() != "ACCESS_DENIED" || count != 0 {
		t.Fatalf("Portal was allowed to publish: %d %v", count, e)
	}
	g.exec("UPDATE users SET is_platform_admin=false WHERE id=$1", "", g.f.User)
	if count, e := refresh(g.admin); e == nil || count != 0 {
		t.Fatalf("stale Admin authority was allowed to publish: %d %v", count, e)
	}
	g.exec("UPDATE users SET is_platform_admin=true WHERE id=$1", "", g.f.User)
	writeFixture(policy, []byte(strings.Replace(string(raw), target.Listener.Addr().String(), "203.0.113.15:443", 1)))
	if count, e := refresh(g.admin); e == nil || e.Error() != "MONITORING_SCRAPE_POLICY_INVALID" || count != 0 {
		t.Fatalf("unsafe replacement was accepted: %d %v", count, e)
	}
	after, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatalf("denied refresh changed the file: %v", e)
	}
	writeFixture(policy, raw)
	writeFixture(filepath.Join(dir, "node-ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: target.Certificate().Raw}))
	example, e := os.ReadFile("alerting/node-scrape.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	config := filepath.Join(dir, "vmagent.yaml")
	writeFixture(config, []byte(strings.NewReplacer("nodes.json", name, "scrape_interval: 15s", "scrape_interval: 1s", "scrape_timeout: 5s", "scrape_timeout: 800ms").Replace(string(example))))
	enrollment, e := g.n.Get(g.ctx, g.admin, g.f.Workspace, node.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	store, _, _ := startHistoryVM(t, g.ctx, "2d", "-search.latencyOffset=0s")
	collector, stopCollector := startMonitoringFixtureProcess(t, g.ctx, "vmagent", "OWNSOLID_TEST_VMAGENT",
		"-promscrape.config="+config, "-promscrape.fileSDCheckInterval=1s", "-memory.allowedBytes=128MB",
		"-remoteWrite.url="+store+"/api/v1/write", "-remoteWrite.flushInterval=1s",
		"-remoteWrite.tmpDataPath="+filepath.Join(t.TempDir(), "queue"))

	waitForScrapeInventory(t, g.ctx, func() bool {
		var reply struct {
			Data struct {
				Result []struct {
					Metric map[string]string
				}
			}
		}
		readScrapeFixture(t, g.ctx, store+"/api/v1/query?query="+url.QueryEscape("node_memory_MemAvailable_bytes"), &reply)
		if len(reply.Data.Result) == 0 {
			return false
		}
		labels := reply.Data.Result[0].Metric
		if len(reply.Data.Result) != 1 || labels["tenant_id"] != g.f.Tenant || labels["workspace_id"] != g.f.Workspace ||
			labels["node_id"] != node.NodeID || labels["site_id"] != enrollment.SiteID || labels["cell_id"] != enrollment.CellID ||
			labels["site_id"] == "" || labels["cell_id"] == "" {
			t.Fatalf("stored samples lack trusted scope: %+v", reply.Data.Result)
		}
		return true
	})
	g.exec("UPDATE managed_nodes SET status='disabled' WHERE id=$1", "", node.NodeID)
	expectRefresh(0)
	waitForScrapeInventory(t, g.ctx, func() bool {
		var reply struct {
			Data struct {
				ActiveTargets []json.RawMessage
			}
		}
		readScrapeFixture(t, g.ctx, collector+"/api/v1/targets", &reply)
		return reply.Data.ActiveTargets != nil && len(reply.Data.ActiveTargets) == 0
	})
	g.exec("UPDATE managed_nodes SET status='active' WHERE id=$1", "", node.NodeID)
	expectRefresh(1)
	g.exec("UPDATE workspaces SET status='disabled' WHERE id=$1", "", g.f.Workspace)
	expectRefresh(0)
	if raw, e := os.ReadFile(path); e != nil || string(raw) != "[]\n" {
		t.Fatalf("disabled Workspace did not withdraw the snapshot: %q %v", raw, e)
	}
	t.Log("vmagent collected the scoped TLS target and removed it after the authoritative node was disabled")

	// Exercise the complete database and publication path at the workspace cap.
	// Stop the collector first so the synthetic addresses are never contacted.
	stopCollector()
	g.exec("UPDATE workspaces SET status='active' WHERE id=$1", "", g.f.Workspace)
	const targetCount = 10_000
	g.exec(`INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at,execution_profile,capabilities,site_id,cell_id)
		SELECT ('a1790234-5080-451f-997f-'||lpad(to_hex(n),12,'0'))::uuid,tenant_id,workspace_id,
		       'scale scrape '||n,'active',decode(lpad(to_hex(n+1),64,'0'),'hex'),
		       protocol_version,facts,last_heartbeat_at,execution_profile,capabilities,site_id,cell_id
		FROM managed_nodes CROSS JOIN generate_series(0,$2-1) AS generated(n) WHERE id=$1`, "", node.NodeID, targetCount)
	endpoints := make(map[string]string, targetCount)
	for i := range targetCount {
		id := fmt.Sprintf("a1790234-5080-451f-997f-%012x", i)
		endpoints[id] = fmt.Sprintf("[fd12:3456:789a:bcde:f012:3456:789a:%04x]:65535", 4096+i)
	}
	assignment, e := json.Marshal(map[string]any{
		"format_version": 1, "workspace_id": g.f.Workspace, "allowed_networks": []string{"fd00::/8"}, "endpoints": endpoints,
	})
	if e != nil {
		t.Fatal(e)
	}
	writeFixture(policy, assignment)
	began := time.Now()
	expectRefresh(targetCount)
	elapsed := time.Since(began)
	info, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("10,000 IPv6 targets: %d input bytes, %d discovery bytes, %s for policy, PostgreSQL, audit, and publication", len(assignment), info.Size(), elapsed)

}

func waitForScrapeInventory(t *testing.T, ctx context.Context, ready func() bool) {
	t.Helper()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func readScrapeFixture(t *testing.T, ctx context.Context, path string, target any) {
	t.Helper()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if e != nil {
		t.Fatal(e)
	}
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatal("fixture query returned", resp.Status)
	}
	if e := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(target); e != nil {
		t.Fatal(e)
	}
}
