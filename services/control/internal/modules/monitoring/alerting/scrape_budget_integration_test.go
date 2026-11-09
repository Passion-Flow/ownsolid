//go:build alerting_integration && linux

package alerting

import (
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
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestScopedNodeScrapeBudgets(t *testing.T) {
	const (
		tenantA    = "a84ee223-412c-457e-9ec8-8b50649d0d0a"
		tenantB    = "b84ee223-412c-457e-9ec8-8b50649d0d0a"
		workspaceA = "a943b5a7-c4cf-4e3b-a12a-4b933fa51ef1"
		workspaceB = "b943b5a7-c4cf-4e3b-a12a-4b933fa51ef1"
		noisyID    = "a1790234-5080-451f-997f-f4f4af42bb80"
		quietID    = "b1790234-5080-451f-997f-f4f4af42bb80"
	)
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{}, Timeout: 4 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	work := t.TempDir()
	metrics := runAlertService(t, ctx, client, "OWNSOLID_TEST_VICTORIA_METRICS", "/health",
		"-storageDataPath="+filepath.Join(work, "data"), "-memory.allowedBytes=128MB",
		"-search.latencyOffset=0s", "-search.disableCache")
	var phase atomic.Int32
	noisy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := phase.Load()
		io.WriteString(w, "node_memory_MemAvailable_bytes{tenant_id=\"spoofed\",workspace_id=\"spoofed\",node_id=\"spoofed\",job=\"spoofed\"} "+strconv.Itoa(101+int(p))+"\n")
		io.WriteString(w, "unapproved_metric{tenant_id=\"spoofed\"} 1\n")
		if p == 4 {
			io.WriteString(w, "#"+strings.Repeat("x", 1<<20)+"\n")
		}
		if p == 1 || p == 2 {
			n := 12
			if p == 2 {
				n = 32
			}
			for i := range n {
				fmt.Fprintf(w, "node_cpu_seconds_total{cpu=%q,mode=\"idle\",exported_tenant_id=\"spoofed\"} 1\n", strconv.Itoa(i))
			}
		}
	}))
	t.Cleanup(noisy.Close)
	quiet := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "node_memory_MemAvailable_bytes %d\n", 201+phase.Load())
	}))
	t.Cleanup(quiet.Close)
	type target struct {
		Targets []string          `json:"targets"`
		Labels  map[string]string `json:"labels"`
	}
	targets := []target{
		{[]string{noisy.Listener.Addr().String()}, map[string]string{"tenant_id": tenantA, "workspace_id": workspaceA, "node_id": noisyID, "site_id": "site-a", "cell_id": "cell-a"}},
		{[]string{quiet.Listener.Addr().String()}, map[string]string{"tenant_id": tenantB, "workspace_id": workspaceB, "node_id": quietID, "site_id": "site-b", "cell_id": "cell-b"}},
	}
	write := func(name string, contents []byte) {
		t.Helper()
		if e := os.WriteFile(filepath.Join(work, name), contents, 0600); e != nil {
			t.Fatal(e)
		}
	}
	write("node-ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: noisy.Certificate().Raw}))
	raw, e := json.Marshal(targets)
	if e != nil {
		t.Fatal(e)
	}
	write("nodes.json", raw)
	profile, e := os.ReadFile("node-scrape.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	replacements := strings.NewReplacer("scrape_interval: 15s", "scrape_interval: 1s", "scrape_timeout: 5s", "scrape_timeout: 800ms",
		"sample_limit: 2500", "sample_limit: 20", "series_limit: 2000", "series_limit: 4")
	write("scrape.yaml", []byte(replacements.Replace(string(profile))))
	collector := runAlertService(t, ctx, client, "OWNSOLID_TEST_VMAGENT", "/health",
		"-promscrape.config="+filepath.Join(work, "scrape.yaml"), "-promscrape.fileSDCheckInterval=1s",
		"-remoteWrite.url="+metrics+"/api/v1/write", "-remoteWrite.tmpDataPath="+filepath.Join(work, "queue"),
		"-remoteWrite.flushInterval=1s", "-memory.allowedBytes=128MB")
	t.Logf("private scrape fixture uses VictoriaMetrics at %s and vmagent at %s", metrics, collector)

	type sample struct {
		Metric map[string]string
		Value  []json.RawMessage
	}
	query := func(expr string) []sample {
		t.Helper()
		var reply struct {
			Status string
			Data   struct {
				Result []sample
			}
		}
		if e := alertRequest(ctx, client, http.MethodGet, metrics+"/api/v1/query?query="+url.QueryEscape(expr), "", nil, &reply); e != nil || reply.Status != "success" {
			t.Fatalf("query %s returned %+v, %v", expr, reply, e)
		}
		return reply.Data.Result
	}
	values := func(expr string) map[string]string {
		t.Helper()
		out := make(map[string]string)
		for _, point := range query(expr) {
			var value string
			if len(point.Value) != 2 || json.Unmarshal(point.Value[1], &value) != nil {
				t.Fatalf("wrong point for %s: %+v", expr, point)
			}
			id := point.Metric["node_id"]
			if _, duplicate := out[id]; duplicate {
				t.Fatalf("duplicate result for %s: %+v", expr, point)
			}
			out[id] = value
		}
		return out
	}
	wait := func(description string, ready func() bool) {
		t.Helper()
		for !ready() {
			select {
			case <-ctx.Done():
				t.Fatalf("%s: %v", description, ctx.Err())
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	wait("both initial exporters", func() bool {
		v := values("node_memory_MemAvailable_bytes")
		return v[noisyID] == "101" && v[quietID] == "201"
	})
	phase.Store(1)
	wait("discard only the noisy target's new series", func() bool {
		drop := values("scrape_series_limit_samples_dropped")
		memory := values("node_memory_MemAvailable_bytes")
		n, e := strconv.Atoi(drop[noisyID])
		return e == nil && n > 0 && drop[quietID] == "0" && memory[noisyID] == "102" && memory[quietID] == "202"
	})
	for _, sample := range query("{__name__=~\"node_(cpu_seconds_total|memory_MemAvailable_bytes)\"}") {
		var tenant, workspace, cell string
		switch sample.Metric["node_id"] {
		case noisyID:
			tenant, workspace, cell = tenantA, workspaceA, "cell-a"
		case quietID:
			tenant, workspace, cell = tenantB, workspaceB, "cell-b"
		default:
			t.Fatalf("untrusted node label survived: %+v", sample.Metric)
		}
		if sample.Metric["tenant_id"] != tenant || sample.Metric["workspace_id"] != workspace ||
			sample.Metric["cell_id"] != cell || sample.Metric["job"] != "ownsolid-node" {
			t.Fatalf("untrusted labels survived: %+v", sample.Metric)
		}
		for name := range sample.Metric {
			if strings.HasPrefix(name, "exported_") {
				t.Fatalf("exporter-controlled scope alias survived: %+v", sample.Metric)
			}
		}
	}
	if found := query("unapproved_metric"); len(found) != 0 {
		t.Fatalf("unapproved family reached the store: %+v", found)
	}
	phase.Store(2)
	wait("reject the oversized sample batch without affecting the other target", func() bool {
		up := values("up{job=\"ownsolid-node\"}")
		return up[noisyID] == "0" && up[quietID] == "1" && values("node_memory_MemAvailable_bytes")[quietID] == "203"
	})
	phase.Store(3)
	wait("recover the target after the sample limit clears", func() bool {
		v := values("node_memory_MemAvailable_bytes")
		return v[noisyID] == "104" && v[quietID] == "204"
	})
	phase.Store(4)
	wait("reject the oversized response body without affecting the other target", func() bool {
		up := values("up{job=\"ownsolid-node\"}")
		return up[noisyID] == "0" && up[quietID] == "1" && values("node_memory_MemAvailable_bytes")[quietID] == "205"
	})
	t.Log("observed per-target discard, stable samples through churn, and independent sample and response-size limits")
}
