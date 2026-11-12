package install

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestFleetHistoryPrivateDeploymentDocker(t *testing.T) {
	if os.Getenv("OWNSOLID_HISTORY_DOCKER") != "1" {
		t.Skip("requires the isolated Linux deployment fixture")
	}
	path, c := historyFixture(t)
	c.MetricsImage = os.Getenv("OWNSOLID_HISTORY_METRICS_IMAGE")
	c.EvaluatorImage = os.Getenv("OWNSOLID_HISTORY_EVALUATOR_IMAGE")
	installer := os.Getenv("OWNSOLID_HISTORY_INSTALLER")
	rulesPath := os.Getenv("OWNSOLID_HISTORY_RULES_FILE")
	if installer == "" || rulesPath == "" {
		t.Fatal("explicit installer and recording rules are required")
	}
	for _, target := range []*string{&c.Raw.Listen, &c.Long.Listen, &c.EvaluatorListen} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		*target = listener.Addr().String()
		listener.Close()
	}
	t.Logf("Private listeners: raw=%s long=%s evaluator=%s", c.Raw.Listen, c.Long.Listen, c.EvaluatorListen)
	c.RequestSeconds = 120
	rules, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.RulesFile, rules, 0600); err != nil {
		t.Fatal(err)
	}
	c.RulesSHA256 = wire.Digest(rules)
	policy := map[string]any{"format_version": 1, "fleet_history": historyPolicy{
		RawOrigin: "https://" + c.Raw.Listen, LongOrigin: "https://" + c.Long.Listen,
		RawHorizonSeconds: 3600, MaxRangeSeconds: 86400, SettlingSeconds: 60,
	}}
	if err := os.WriteFile(c.MonitoringFile, historyJSON(policy), 0600); err != nil {
		t.Fatal(err)
	}
	writeHistoryFixture(t, path, c)
	site, err := loadHistorySite(path)
	if err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(c.Root, "compose.yaml")
	t.Cleanup(func() {
		if exists(active) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := (ExecRunner{}).Run(ctx, "docker", "compose", "-f", active, "down", "--timeout", "5"); err != nil {
				t.Error("cleanup owned telemetry project", err)
			}
		}
	})
	apply := func() {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), installer, "fleet-history", "apply", "--config", path)
		output, err := cmd.CombinedOutput()
		if err != nil {
			compose := active
			if !exists(compose) {
				compose = filepath.Join(c.Root, "compose-candidate.yaml")
			}
			logs, _ := (ExecRunner{}).Run(t.Context(), "docker", "compose", "-f", compose, "logs", "--no-color", "--tail", "30")
			t.Fatalf("installed CLI failed: %v\n%s\n%s", err, output, logs)
		}
		if !bytes.Contains(output, []byte(`"status":"FLEET_HISTORY_READY"`)) {
			t.Fatalf("missing ready output: %s", output)
		}
	}
	apply()
	// Prove that workload traffic on the publishing bridge reaches the recorder.
	// The bridge is IPv4-only, so its IPv6 probe is refused before transmission.
	probe := os.Getenv("OWNSOLID_HISTORY_PROBE")
	if probe == "" {
		t.Fatal("the isolated workload probe binary is required")
	}
	if output, err := (ExecRunner{}).Run(t.Context(), "docker", "run", "--rm", "--pull", "never", "--network", c.Project+"_publish",
		"--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true",
		"--user", "10001:10001", "--memory", "64m", "--cpus", "1", "--pids-limit", "32",
		"--mount", "type=bind,source="+probe+",target=/probe,readonly", "--entrypoint", "/probe", c.MetricsImage); err != nil {
		t.Fatalf("workload recorder probe failed: %v %s", err, output)
	}
	client := func(roots *tls.Config) *http.Client {
		transport := &http.Transport{TLSClientConfig: roots}
		t.Cleanup(transport.CloseIdleConnections)
		return &http.Client{Transport: transport, Timeout: 3 * time.Second}
	}
	rawClient := client(&tls.Config{RootCAs: site.rawRoots, MinVersion: tls.VersionTLS12})
	longClient := client(&tls.Config{RootCAs: site.longRoots, MinVersion: tls.VersionTLS12})
	do := func(client *http.Client, method, address, body string) ([]byte, *http.Response) {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), method, address, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		reply, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer reply.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(reply.Body, 1<<20))
		if err != nil || reply.StatusCode != http.StatusOK && reply.StatusCode != http.StatusNoContent {
			t.Fatalf("store response %d: %s %v", reply.StatusCode, raw, err)
		}
		return raw, reply
	}
	labels := `job="ownsolid-node",tenant_id="a1000000-0000-4000-8000-000000000001",workspace_id="a1000000-0000-4000-8000-000000000002",site_id="fixture-site",cell_id="fixture-cell"`
	sample := "up{" + labels + `,node_id="a1000000-0000-4000-8000-000000000003"} 1
` +
		"up{" + labels + `,node_id="a1000000-0000-4000-8000-000000000004"} 0
`
	// Allow two production evaluation intervals plus the datasource's latency offset.
	// Keep the tiny source fresh without changing the one-minute production rules.
	query := "https://" + c.Long.Listen + "/api/v1/query?query=" + url.QueryEscape(`ownsolid:workspace_cell_nodes_observed:count{workspace_id="a1000000-0000-4000-8000-000000000002"}`)
	hasAggregate := func(body []byte) bool {
		var result struct {
			Data struct {
				Result []struct {
					Value []json.RawMessage `json:"value"`
				} `json:"result"`
			} `json:"data"`
		}
		return json.Unmarshal(body, &result) == nil && len(result.Data.Result) == 1 && len(result.Data.Result[0].Value) == 2 && string(result.Data.Result[0].Value[1]) == `"2"`
	}
	retained := func(client *http.Client) *http.Response {
		t.Helper()
		body, reply := do(client, http.MethodGet, query, "")
		if !hasAggregate(body) {
			t.Fatalf("persisted aggregate changed or disappeared: %s", body)
		}
		return reply
	}
	found := false
	deadline := time.Now().Add(150 * time.Second)
	for time.Now().Before(deadline) {
		do(rawClient, http.MethodPost, "https://"+c.Raw.Listen+"/api/v1/import/prometheus", sample)
		body, _ := do(longClient, http.MethodGet, query, "")
		if hasAggregate(body) {
			found = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !found {
		t.Fatal("real vmalert did not persist the scoped aggregate")
	}
	t.Log("Real stores and vmalert persisted the scoped 2-node aggregate")
	// An acknowledged stop followed by interruption must be safely retryable.
	interrupted := &historyStopInterruption{runner: ExecRunner{}}
	if err := applyHistory(t.Context(), site, interrupted, &bytes.Buffer{}, probeHistory); err == nil {
		t.Fatal("interrupted stop reported success")
	}
	apply()
	retained(longClient)
	t.Log("Retry after an interrupted real stop retained the long-tier sample")

	oldCA, err := os.ReadFile(c.Long.TLS.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	rawCA, err := os.ReadFile(c.Raw.TLS.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	nextCA, nextKey, err := generateCA("next-deployment-issuer")
	if err != nil {
		t.Fatal(err)
	}
	historyWrite(t, c.Long.TLS.CAFile, append(oldCA, nextCA...))
	historyWrite(t, c.ControlCAFile, append(append(bytes.Clone(rawCA), oldCA...), nextCA...))
	apply()
	overlap, err := loadHistorySite(path)
	if err != nil {
		t.Fatal(err)
	}
	overlapClient := client(&tls.Config{RootCAs: overlap.longRoots, MinVersion: tls.VersionTLS12})
	retained(overlapClient)
	retained(overlapClient)
	host, _, err := net.SplitHostPort(c.Long.Listen)
	if err != nil {
		t.Fatal(err)
	}
	leaf, key, err := issueServerCertificate(nextCA, nextKey, []string{host, "172.29.248.3"})
	if err != nil {
		t.Fatal(err)
	}
	historyWrite(t, c.Long.TLS.CertificateFile, leaf)
	historyWrite(t, c.Long.TLS.PrivateKeyFile, key)
	apply()
	reply := retained(overlapClient)
	pair, err := tls.X509KeyPair(leaf, key)
	if err != nil || reply.TLS == nil || !bytes.Equal(reply.TLS.PeerCertificates[0].Raw, pair.Certificate[0]) {
		t.Fatal("an old authenticated connection survived the deployed issuer change", err)
	}
	historyWrite(t, c.Long.TLS.CAFile, nextCA)
	historyWrite(t, c.ControlCAFile, append(bytes.Clone(rawCA), nextCA...))
	apply()
	next, err := loadHistorySite(path)
	if err != nil {
		t.Fatal(err)
	}
	nextClient := client(&tls.Config{RootCAs: next.longRoots, MinVersion: tls.VersionTLS12})
	retained(nextClient)
	if response, err := longClient.Get("https://" + c.Long.Listen + "/health"); err == nil {
		response.Body.Close()
		t.Fatal("old issuer-only trust accepted the new issuer")
	}
	t.Log("Pretrusted issuer replacement drained old connections; new-only trust retained data and old-only trust refused the peer")
}

type historyStopInterruption struct{ runner Runner }

func (r *historyStopInterruption) Run(ctx context.Context, name string, args ...string) (string, error) {
	output, err := r.runner.Run(ctx, name, args...)
	if err == nil && strings.Contains(strings.Join(args, " "), " stop evaluator raw long") {
		return output, errors.New("fixture interrupted after real stop")
	}
	return output, err
}
