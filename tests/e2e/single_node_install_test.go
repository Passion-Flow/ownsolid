// Package e2e drives the real installer and control plane. run-single-node.sh runs it
// inside a container whose only way off the host is the egress recorder, so everything
// it proves works offline and any outbound attempt fails the run.
package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	license "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

const (
	portalOrigin = "https://portal.ownsolid.test:8443"
	adminOrigin  = "https://admin.ownsolid.test:8443"
	adminEmail   = "admin@ownsolid.test"
)

// webAsset matches the bundled files an application's page refers to.
var webAsset = regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`)

type environment struct {
	installer, composeFile, controlImage, postgresImage, archive, root, agent string
	metricsImage, logsImage                                                   string
	egress                                                                    egressWatch
	// licenseKey signs the test's license; its trust file is in every configuration.
	licenseKey license.PrivateKey
	trust      license.Trust
}

func loadEnvironment(t *testing.T) environment {
	t.Helper()
	e := environment{
		installer: os.Getenv("OWNSOLID_E2E_INSTALLER"), composeFile: os.Getenv("OWNSOLID_E2E_COMPOSE_FILE"),
		controlImage: os.Getenv("OWNSOLID_E2E_CONTROL_IMAGE"), postgresImage: os.Getenv("OWNSOLID_E2E_POSTGRES_IMAGE"),
		archive: os.Getenv("OWNSOLID_E2E_IMAGE_ARCHIVE"), root: os.Getenv("OWNSOLID_E2E_ROOT"),
		agent:        os.Getenv("OWNSOLID_E2E_AGENT"),
		metricsImage: os.Getenv("OWNSOLID_E2E_METRICS_IMAGE"), logsImage: os.Getenv("OWNSOLID_E2E_LOGS_IMAGE"),
		egress: egressWatch{log: os.Getenv("OWNSOLID_E2E_EGRESS_LOG"), iface: os.Getenv("OWNSOLID_E2E_EGRESS_INTERFACE")},
	}
	if e.installer == "" {
		t.Skip("needs tests/e2e/run-single-node.sh, which sets OWNSOLID_E2E_INSTALLER")
	}
	if e.egress.log == "" || e.egress.iface == "" {
		t.Fatal("OWNSOLID_E2E_EGRESS_LOG and OWNSOLID_E2E_EGRESS_INTERFACE must name the egress recorder")
	}
	var err error
	if e.licenseKey, e.trust, err = license.GenerateKey("e2e-issuer"); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e environment) configuration(t *testing.T, directory, size, controlImage string) string {
	t.Helper()
	return e.configurationWith(t, directory, size, controlImage, "")
}

// configurationWith writes an install configuration with extra appended, for example
// control_settings, under a name of its own.
func (e environment) configurationWith(t *testing.T, directory, size, controlImage, extra string) string {
	t.Helper()
	raw, err := json.Marshal(e.trust)
	if err != nil {
		t.Fatal(err)
	}
	trustFile := filepath.Join(directory, "license-trust.json")
	if err = os.WriteFile(trustFile, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	name := size + controlImage
	if extra != "" {
		name += "-" + fmt.Sprintf("%x", sha256.Sum256([]byte(extra)))[:12]
	}
	path := filepath.Join(directory, "install-"+strings.NewReplacer(":", "-", "/", "-").Replace(name)+".yaml")
	content := fmt.Sprintf(`format_version: 1
topology: {size: %s}
install_root: %s
compose_file: %s
images:
  archive_file: %s
  control: %s
  postgres: %s
  metrics: %s
  logs: %s
origins: {portal: %s, admin: %s}
license: {trust_file: %s}
smtp: {host: smtp.ownsolid.test, from_address: platform@ownsolid.test}
administrator:
  email: %s
  display_name: Installation Administrator
  installation_name: End To End
  workspace_name: Default Workspace
`, size, e.root, e.composeFile, e.archive, controlImage, e.postgresImage, e.metricsImage, e.logsImage, portalOrigin, adminOrigin, trustFile, adminEmail) + extra
	if err = os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func run(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()
	return runWithEnvironment(t, nil, name, args...)
}

// runWithEnvironment runs a command with variables added to the test's environment and
// returns its combined output.
func runWithEnvironment(t *testing.T, variables []string, name string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	command.Env = append(os.Environ(), variables...)
	output, err := command.CombinedOutput()
	return string(output), err
}

func (e environment) compose(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return run(t, "docker", append([]string{"compose", "--env-file", filepath.Join(e.root, "compose.env"), "-f", filepath.Join(e.root, "compose.yaml")}, args...)...)
}

func (e environment) apply(t *testing.T, path string) {
	t.Helper()
	output, err := run(t, e.installer, "apply", "--config", path)
	if err != nil || !strings.Contains(output, "INSTALL_COMPLETE") {
		t.Fatalf("apply: %v\n%s", err, output)
	}
}

// browser talks to the published port the way a browser does: origin host name, verified
// certificate, cookies and the CSRF token.
type browser struct {
	client *http.Client
	origin string
}

func (e environment) newBrowser(t *testing.T, origin string) *browser {
	t.Helper()
	authority, err := os.ReadFile(filepath.Join(e.root, "config", "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(authority) {
		t.Fatal("generated CA does not parse")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &browser{origin: origin, client: &http.Client{Jar: jar, Timeout: 30 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, "127.0.0.1:8443")
		},
	}}}
}

func (b *browser) request(t *testing.T, method, path string, body any, csrf string) (int, []byte) {
	t.Helper()
	return b.requestWith(t, method, path, body, csrf, nil)
}

func (b *browser) requestWith(t *testing.T, method, path string, body any, csrf string, headers map[string]string) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, b.origin+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", b.origin)
		request.Header.Set("X-CSRF-Token", csrf)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := b.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, raw
}

func (b *browser) login(t *testing.T, password string) int {
	t.Helper()
	status, raw := b.request(t, http.MethodGet, "/api/v1/auth/csrf", nil, "")
	if status != http.StatusOK {
		t.Fatalf("csrf: %d %s", status, raw)
	}
	var csrf struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &csrf); err != nil || csrf.Data.Token == "" {
		t.Fatalf("csrf body: %v", err)
	}
	status, _ = b.request(t, http.MethodPost, "/api/v1/auth/login", map[string]string{"email": adminEmail, "password": password}, csrf.Data.Token)
	return status
}

func (e environment) password(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.root, "operator", "admin-credential"))
	if err != nil {
		t.Fatal(err)
	}
	var credential struct {
		Password string `json:"password"`
	}
	if err = json.Unmarshal(raw, &credential); err != nil || credential.Password == "" {
		t.Fatal("administrator credential file does not hold a password")
	}
	return credential.Password
}

func (e environment) assertLogin(t *testing.T, password string) {
	t.Helper()
	b := e.newBrowser(t, adminOrigin)
	if status := b.login(t, password); status != http.StatusOK {
		t.Fatalf("administrator login: status %d", status)
	}
	if status, raw := b.request(t, http.MethodGet, "/api/v1/me", nil, ""); status != http.StatusOK || !strings.Contains(string(raw), adminEmail) {
		t.Fatalf("session read: status %d", status)
	}
	if status, _ := b.request(t, http.MethodGet, "/api/v1/sites/default/cells/default/admin/nodes", nil, ""); status != http.StatusOK {
		t.Fatalf("scoped node list: status %d", status)
	}
	for _, path := range []string{"/api/v1/admin/nodes", "/api/v1/sites/default/cells/other/admin/nodes"} {
		if status, _ := b.request(t, http.MethodGet, path, nil, ""); status != http.StatusNotFound {
			t.Fatalf("%s: status %d, want 404", path, status)
		}
	}
}

// assertWebApplication loads an application the way a browser does: the page, every
// script, stylesheet and icon it names, and a deep link, which answers with the same page.
func (e environment) assertWebApplication(t *testing.T, origin string) {
	t.Helper()
	b := e.newBrowser(t, origin)
	status, page := b.request(t, http.MethodGet, "/", nil, "")
	if status != http.StatusOK || !bytes.Contains(page, []byte(`<div id="root">`)) {
		t.Fatalf("%s: status %d, page %q", origin, status, page)
	}
	assets := webAsset.FindAllSubmatch(page, -1)
	if len(assets) == 0 {
		t.Fatalf("%s: the page names no assets: %q", origin, page)
	}
	for _, asset := range assets {
		if status, _ = b.request(t, http.MethodGet, string(asset[1]), nil, ""); status != http.StatusOK {
			t.Fatalf("%s%s: status %d", origin, asset[1], status)
		}
	}
	status, deep := b.request(t, http.MethodGet, "/sign-in", nil, "")
	if status != http.StatusOK || !bytes.Equal(deep, page) {
		t.Fatalf("%s/sign-in: status %d, page %q", origin, status, deep)
	}
}

func TestSingleNodeInstall(t *testing.T) {
	outer := t
	e := loadEnvironment(t)
	directory := t.TempDir()
	config := e.configuration(t, directory, "single-node", e.controlImage)

	t.Run("host reaches nothing but the egress recorder", e.egress.assertIsolated)

	t.Run("configuration is validated", func(t *testing.T) {
		if output, err := run(t, e.installer, "validate", "--config", config); err != nil || !strings.Contains(output, "INSTALL_CONFIG_VALID") {
			t.Fatalf("%v %s", err, output)
		}
	})

	t.Run("unsupported topology changes nothing", func(t *testing.T) {
		output, err := run(t, e.installer, "apply", "--config", e.configuration(t, directory, "ha", e.controlImage))
		if err == nil || !strings.Contains(output, "CONFIG_TOPOLOGY_SIZE_NOT_SUPPORTED") {
			t.Fatalf("%v %s", err, output)
		}
		if _, err = os.Stat(e.root); !os.IsNotExist(err) {
			t.Fatal("a refused configuration touched the install root")
		}
	})

	t.Run("missing image fails clearly and can be retried", func(t *testing.T) {
		absent := "ownsolid/control:absent"
		output, err := run(t, e.installer, "apply", "--config", e.configuration(t, directory, "single-node", absent))
		if err == nil || !strings.Contains(output, "INSTALL_IMAGE_MISSING") || !strings.Contains(output, absent) || !strings.Contains(output, "run apply again") {
			t.Fatalf("%v %s", err, output)
		}
	})

	t.Run("apply installs from the archive", func(t *testing.T) { e.apply(t, config) })

	password := e.password(t)

	t.Run("status reports healthy", func(t *testing.T) {
		output, err := run(t, e.installer, "status", "--config", config)
		if err != nil || !strings.Contains(output, portalOrigin+": ready") || !strings.Contains(output, adminOrigin+": ready") {
			t.Fatalf("%v %s", err, output)
		}
	})

	t.Run("the image serves the console and the portal", func(t *testing.T) {
		e.assertWebApplication(t, adminOrigin)
		e.assertWebApplication(t, portalOrigin)
	})

	t.Run("administrator signs in and authentication is enforced", func(t *testing.T) {
		e.assertLogin(t, password)
		wrong := e.newBrowser(t, adminOrigin)
		if status := wrong.login(t, password+"-wrong"); status != http.StatusUnauthorized {
			t.Fatalf("wrong password: status %d", status)
		}
		if status, _ := e.newBrowser(t, adminOrigin).request(t, http.MethodGet, "/api/v1/me", nil, ""); status != http.StatusUnauthorized {
			t.Fatalf("anonymous read: status %d", status)
		}
	})

	var agent *agentProcess
	t.Run("an agent enrolls and runs tasks over the session stream", func(t *testing.T) {
		agent = e.startAgent(t, outer, password)
		agent.runTask(t, 5*time.Second)
		if cursor := agent.cursor(t); cursor != 1 {
			t.Fatalf("agent cursor %d after its first task", cursor)
		}
		e.assertTasksAcknowledged(t, agent.nodeID, 1)
		if !agent.logged("agent_session_transport transport=stream") {
			t.Fatalf("agent did not use the session stream:\n%s", agent.output())
		}
		if output, err := e.compose(t, "logs", "control"); err != nil || !strings.Contains(output, "agent_session_opened") {
			t.Fatalf("control did not open a session: %v", err)
		}
	})

	t.Run("repeated heartbeats wait for the batch and write no state", func(t *testing.T) {
		if agent == nil {
			t.Skip("no agent")
		}
		// The first repeat can take the synchronous path once, as the table learns the state.
		time.Sleep(12 * time.Second)
		query := "SELECT updated_at,last_heartbeat_at,connectivity FROM managed_nodes WHERE id='" + agent.nodeID + "'"
		before := strings.Split(e.sql(t, query), "|")
		time.Sleep(25 * time.Second)
		after := strings.Split(e.sql(t, query), "|")
		if len(before) != 3 || len(after) != 3 || after[0] != before[0] {
			t.Fatalf("a repeated heartbeat changed the node: %q then %q", before, after)
		}
		if after[1] == before[1] || after[2] != "online" {
			t.Fatalf("last seen did not reach the database: %q then %q", before, after)
		}
	})
	t.Run("serving holds no privileged database identity", func(t *testing.T) {
		output, err := e.compose(t, "exec", "--no-TTY", "control", "python3", "-c", "import os,sys;sys.exit(os.path.exists('/run/projectone-operator'))")
		if err != nil {
			t.Fatalf("operator directory is visible to serving: %v %s", err, output)
		}
		output, err = e.compose(t, "exec", "--no-TTY", "postgres", "psql", "-X", "-tA", "-U", "ownsolid_migrator", "-d", "ownsolid", "-c", "SELECT rolsuper FROM pg_roles WHERE rolname='ownsolid_runtime'")
		if err != nil || strings.TrimSpace(output) != "f" {
			t.Fatalf("runtime role: %v %s", err, output)
		}
		output, err = e.compose(t, "exec", "--no-TTY", "control", "python3", "-c", "import os;print(os.getuid())")
		if err != nil || strings.TrimSpace(output) == "0" {
			t.Fatalf("control runs as root: %v %s", err, output)
		}
	})

	t.Run("workload egress is recorded and refused", func(t *testing.T) {
		// Exits zero only when the connection fails.
		target := netip.MustParseAddrPort("192.0.2.2:443")
		probe := fmt.Sprintf("import socket,sys;s=socket.socket();s.settimeout(3);sys.exit(0 if s.connect_ex(('%s',%d)) else 1)", target.Addr(), target.Port())
		if output, err := e.compose(t, "exec", "--no-TTY", "control", "python3", "-c", probe); err != nil {
			t.Fatalf("control reached %s: %v %s", target, err, output)
		}
		e.egress.expectRecorded(t, target)
	})

	t.Run("state survives a restart", func(t *testing.T) {
		if output, err := e.compose(t, "restart", "control"); err != nil {
			t.Fatalf("%v %s", err, output)
		}
		if output, err := e.compose(t, "up", "--detach", "--wait", "--no-build", "control"); err != nil {
			t.Fatalf("%v %s", err, output)
		}
		e.assertLogin(t, password)
	})

	t.Run("the agent reconnects after a control restart", func(t *testing.T) {
		if agent == nil {
			t.Skip("no agent is running")
		}
		// The agent keeps running through the restart and reopens its stream.
		agent.runTask(t, 90*time.Second)
		if !agent.running() {
			t.Fatalf("agent exited:\n%s", agent.output())
		}
		if cursor := agent.cursor(t); cursor != 2 {
			t.Fatalf("agent cursor %d after its second task", cursor)
		}
		agent.stop(t)
	})

	t.Run("an administrator paces the agent at runtime", func(t *testing.T) {
		if agent == nil {
			t.Skip("no agent is running")
		}
		agent.start(t)
		agent.waitFor(t, "agent_session_transport transport=stream", 60*time.Second)
		admin, csrf := e.adminSession(t, password)
		mark := len(agent.output())
		e.setPacing(t, admin, csrf, 7, 0, 2, 30, "Slowing heartbeats in the end-to-end test")
		agent.waitForAfter(t, mark, "agent_pacing_changed heartbeat_seconds=7 jitter_percent=0 reconnect_base_seconds=2 reconnect_max_seconds=30", 30*time.Second)
		// The change applies to the running agent: one interval later it still runs tasks.
		agent.runTask(t, 30*time.Second)
		mark = len(agent.output())
		e.setPacing(t, admin, csrf, 10, 10, 1, 60, "Restoring the default pacing")
		agent.waitForAfter(t, mark, "agent_pacing_changed heartbeat_seconds=10 jitter_percent=10 reconnect_base_seconds=1 reconnect_max_seconds=60", 30*time.Second)
		agent.stop(t)
	})

	t.Run("an agent that lost its task cursor resyncs", func(t *testing.T) {
		if agent == nil {
			t.Skip("no agent is running")
		}
		if err := os.Remove(filepath.Join(agent.stateDirectory, "task-cursor.json")); err != nil {
			t.Fatal(err)
		}
		agent.start(t)
		agent.waitFor(t, "agent_task_resync acknowledged_sequence=3", 60*time.Second)
		agent.runTask(t, 30*time.Second)
		if cursor := agent.cursor(t); cursor != 4 {
			t.Fatalf("agent cursor %d after a resync and one task", cursor)
		}
		e.assertTasksAcknowledged(t, agent.nodeID, 4)
		agent.stop(t)
	})

	t.Run("a silent agent is recorded offline and online again", func(t *testing.T) {
		if agent == nil {
			t.Skip("no agent")
		}
		state := "SELECT connectivity,connectivity_changed_at FROM managed_nodes WHERE id='" + agent.nodeID + "'"
		waitFor := func(want string, within time.Duration) string {
			t.Helper()
			var got string
			for started := time.Now(); time.Since(started) < within; time.Sleep(2 * time.Second) {
				got = e.sql(t, state)
				if strings.HasPrefix(got, want+"|") {
					return got
				}
			}
			t.Fatalf("connectivity %q after %v, want %s", got, within, want)
			return ""
		}
		agent.start(t)
		online := waitFor("online", 40*time.Second)
		agent.stop(t)
		offline := waitFor("offline", 170*time.Second)
		agent.start(t)
		back := waitFor("online", 60*time.Second)
		agent.stop(t)
		if online == offline || offline == back {
			t.Fatalf("change times did not move: %q, %q, %q", online, offline, back)
		}
	})
	t.Run("control sheds an agent over its call limit and the agent waits for the hint", func(t *testing.T) {
		if agent == nil {
			t.Skip("no agent is running")
		}
		// One heartbeat every 30 seconds for each node, against the 10 second interval, so
		// every other heartbeat is refused with a retry hint of about 20 seconds (ADR 0018).
		e.apply(t, e.configurationWith(t, directory, "single-node", e.controlImage, "control_settings:\n  runtime:\n    agent_admission:\n      calls:\n        heartbeat: {per_minute: 2, burst: 1}\n"))
		mark := len(agent.output())
		agent.start(t)
		agent.waitForAfter(t, mark, "agent_session_transport transport=stream", 60*time.Second)
		agent.runTask(t, 90*time.Second)
		agent.waitForAfter(t, mark, "agent_cycle_failed code=RATE_LIMITED", 90*time.Second)
		first := len(agent.output())
		agent.waitForAfter(t, first, "agent_cycle_failed code=RATE_LIMITED", 120*time.Second)
		refusals := refusalTimes(t, agent.output()[mark:])
		// An agent that ignored the hint would retry within its one second base backoff.
		for i := 1; i < len(refusals); i++ {
			if gap := refusals[i].Sub(refusals[i-1]); gap < 19*time.Second {
				t.Fatalf("refused heartbeats %s apart, under the hint:\n%s", gap, agent.output()[mark:])
			}
		}
		// Shed heartbeats leave the node working: a task still runs to completion.
		agent.runTask(t, 120*time.Second)
		if output, err := e.compose(t, "logs", "control"); err != nil || !strings.Contains(output, "agent_call_refused priority=heartbeat reason=node_rate code=RATE_LIMITED") {
			t.Fatalf("control logged no refusal: %v", err)
		}
		agent.stop(t)
		e.apply(t, config)
		mark = len(agent.output())
		agent.start(t)
		agent.waitForAfter(t, mark, "agent_session_transport transport=stream", 60*time.Second)
		agent.runTask(t, 30*time.Second)
		agent.stop(t)
		if strings.Contains(agent.output()[mark:], "code=RATE_LIMITED") {
			t.Fatalf("the default limits refused the agent:\n%s", agent.output()[mark:])
		}
	})

	t.Run("stopped database is reported and repaired by apply", func(t *testing.T) {
		if output, err := e.compose(t, "stop", "postgres"); err != nil {
			t.Fatalf("%v %s", err, output)
		}
		output, err := run(t, e.installer, "status", "--config", config)
		if err == nil || !strings.Contains(output, "INSTALL_NOT_HEALTHY") {
			t.Fatalf("%v %s", err, output)
		}
		e.apply(t, config)
		e.assertLogin(t, password)
	})

	t.Run("control reaches the log store on the internal network", func(t *testing.T) {
		// The fixed address of the default state subnet.
		script := `import json,sys,time,urllib.parse,urllib.request as u
base="http://172.31.254.4:9428"
line=json.dumps({"_msg":"e2e-marker","tenant_id":"e2e","workspace_id":"e2e","node_id":"e2e","component":"e2e"}).encode()+b"\n"
u.urlopen(u.Request(base+"/insert/jsonline?_stream_fields=tenant_id,component",data=line,headers={"Content-Type":"application/stream+json"}),timeout=5)
form=urllib.parse.urlencode({"query":'{tenant_id="e2e"} e2e-marker'}).encode()
for _ in range(30):
    if b"e2e-marker" in u.urlopen(u.Request(base+"/select/logsql/query",data=form),timeout=5).read():
        sys.exit(0)
    time.sleep(1)
sys.exit("the log store accepted the record but never returned it")`
		if output, err := e.compose(t, "exec", "--no-TTY", "control", "python3", "-c", script); err != nil {
			t.Fatalf("%v %s", err, output)
		}
	})

	t.Run("control pushes its process metrics to the metrics store", func(t *testing.T) {
		// The fixed address of the default state subnet. A series is listed once indexed,
		// and control pushes every 15 seconds.
		script := `import json,sys,time,urllib.parse,urllib.request as u
names=["ownsolid_go_goroutines","ownsolid_database_pool_total_connections","ownsolid_telemetry_log_queue_length","ownsolid_agent_admission_seats_in_use","ownsolid_agent_admission_refused_total"]
query=urllib.parse.urlencode([("match[]",n+'{service="control"}') for n in names])
for _ in range(60):
    found={s["__name__"] for s in json.load(u.urlopen("http://172.31.254.3:8428/api/v1/series?"+query,timeout=5))["data"]}
    if found==set(names):
        sys.exit(0)
    time.sleep(1)
sys.exit("control pushed no process metrics to the metrics store")`
		if output, err := e.compose(t, "exec", "--no-TTY", "control", "python3", "-c", script); err != nil {
			t.Fatalf("%v %s", err, output)
		}
	})

	t.Run("losing the telemetry stores leaves the platform serving", func(t *testing.T) {
		if output, err := e.compose(t, "stop", "metrics", "logs"); err != nil {
			t.Fatalf("%v %s", err, output)
		}
		e.assertLogin(t, password)
		output, err := run(t, e.installer, "status", "--config", config)
		if err == nil || !strings.Contains(output, "INSTALL_NOT_HEALTHY") {
			t.Fatalf("%v %s", err, output)
		}
		e.apply(t, config)
		if output, err = run(t, e.installer, "status", "--config", config); err != nil {
			t.Fatalf("%v %s", err, output)
		}
	})

	t.Run("reinstalling over existing data keeps it", func(t *testing.T) {
		if output, err := e.compose(t, "down"); err != nil {
			t.Fatalf("%v %s", err, output)
		}
		e.apply(t, config)
		if e.password(t) != password {
			t.Fatal("a second apply issued a new administrator credential")
		}
		e.assertLogin(t, password)
		output, err := run(t, e.installer, "status", "--config", config)
		if err != nil {
			t.Fatalf("%v %s", err, output)
		}
	})

	backups := filepath.Join(directory, "backups")
	var backup string
	t.Run("backup writes a verified copy while serving continues", func(t *testing.T) {
		if err := os.MkdirAll(backups, 0o700); err != nil {
			t.Fatal(err)
		}
		output, err := run(t, e.installer, "backup", "--config", config, "--output", backups)
		if err != nil || !strings.Contains(output, "BACKUP_COMPLETE") || !strings.Contains(output, "step verify: ok") {
			t.Fatalf("%v %s", err, output)
		}
		_, backup, _ = strings.Cut(output, "BACKUP_COMPLETE path=")
		backup = strings.TrimSpace(backup)
		if _, err = os.Stat(filepath.Join(backup, "manifest.json")); err != nil {
			t.Fatal(err)
		}
		e.assertLogin(t, password)
	})

	t.Run("restore brings back the backup's data and revokes later sessions", func(t *testing.T) {
		if backup == "" {
			t.Skip("no backup was written")
		}
		tasks := e.sql(t, "SELECT count(*)||'/'||coalesce(max(sequence),0) FROM node_tasks")
		// A session created after the backup is a write the restore must undo.
		later := e.newBrowser(t, adminOrigin)
		if status := later.login(t, password); status != http.StatusOK {
			t.Fatalf("login: %d", status)
		}
		sessions := e.sql(t, "SELECT count(*) FROM sessions")
		output, err := run(t, e.installer, "restore", "--config", config, "--from", backup)
		if err != nil || !strings.Contains(output, "RESTORE_COMPLETE") {
			t.Fatalf("%v %s", err, output)
		}
		if got := e.sql(t, "SELECT count(*) FROM sessions"); got == sessions {
			t.Fatalf("sessions %s: the session created after the backup survived", got)
		}
		if got := e.sql(t, "SELECT count(*) FROM sessions WHERE revoked_at IS NULL"); got != "0" {
			t.Fatalf("%s sessions from the backup are still valid", got)
		}
		if got := e.sql(t, "SELECT count(*)||'/'||coalesce(max(sequence),0) FROM node_tasks"); got != tasks {
			t.Fatalf("node tasks %s, want %s", got, tasks)
		}
		if status, _ := later.request(t, http.MethodGet, "/api/v1/me", nil, ""); status != http.StatusUnauthorized {
			t.Fatalf("a session from after the backup answered %d", status)
		}
		e.assertLogin(t, password)
		if output, err = run(t, e.installer, "status", "--config", config); err != nil {
			t.Fatalf("%v %s", err, output)
		}
	})

	t.Run("restore refuses a damaged backup and leaves serving alone", func(t *testing.T) {
		if backup == "" {
			t.Skip("no backup was written")
		}
		damaged := backup + "-damaged"
		if output, err := run(t, "cp", "-a", backup, damaged); err != nil {
			t.Fatalf("%v %s", err, output)
		}
		dump := filepath.Join(damaged, "database.dump")
		raw, err := os.ReadFile(dump)
		if err != nil {
			t.Fatal(err)
		}
		raw[len(raw)/2] ^= 0xff
		if err = os.WriteFile(dump, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		output, err := run(t, e.installer, "restore", "--config", config, "--from", damaged)
		if err == nil || !strings.Contains(output, "INSTALL_BACKUP_CORRUPT") {
			t.Fatalf("%v %s", err, output)
		}
		e.assertLogin(t, password)
	})

	t.Run("restore rebuilds a lost installation from the backup alone", func(t *testing.T) {
		if backup == "" {
			t.Skip("no backup was written")
		}
		if output, err := e.compose(t, "down"); err != nil {
			t.Fatalf("%v %s", err, output)
		}
		if err := os.Rename(e.root, e.root+"-lost"); err != nil {
			t.Fatal(err)
		}
		output, err := run(t, e.installer, "restore", "--config", config, "--from", backup)
		if err != nil || !strings.Contains(output, "RESTORE_COMPLETE") {
			t.Fatalf("%v %s", err, output)
		}
		// The administrator credential comes back with the backup's files.
		if e.password(t) != password {
			t.Fatal("the restored installation has another administrator credential")
		}
		e.assertLogin(t, password)
		if output, err = run(t, e.installer, "status", "--config", config); err != nil {
			t.Fatalf("%v %s", err, output)
		}
	})
}
