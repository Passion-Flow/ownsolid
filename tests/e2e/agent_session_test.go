package e2e

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	license "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

const (
	agentNodeName = "e2e-agent"
	nodesPath     = "/api/v1/sites/default/cells/default/admin/nodes"
)

// agentProcess is a real agent binary enrolled into the installed control plane.
type agentProcess struct {
	cmd            *exec.Cmd
	log            *lockedBuffer
	done           chan struct{}
	admin          *browser
	csrf           string
	nodeID         string
	binary         string
	configFile     string
	stateDirectory string
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// adminSession signs in and returns the browser with a CSRF token for writes.
func (e environment) adminSession(t *testing.T, password string) (*browser, string) {
	t.Helper()
	b := e.newBrowser(t, adminOrigin)
	if status := b.login(t, password); status != http.StatusOK {
		t.Fatalf("administrator login: status %d", status)
	}
	status, raw := b.request(t, http.MethodGet, "/api/v1/auth/csrf", nil, "")
	var csrf struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if status != http.StatusOK || json.Unmarshal(raw, &csrf) != nil || csrf.Data.Token == "" {
		t.Fatalf("csrf after login: %d %s", status, raw)
	}
	return b, csrf.Data.Token
}

func decode(t *testing.T, status, want int, raw []byte, out any) {
	t.Helper()
	if status != want {
		t.Fatalf("status %d, want %d: %s", status, want, raw)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		t.Fatal(err)
	}
}

func writePrivate(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

// installLicense signs a whole-platform license for this deployment with the test's key
// and imports it, as an operator does before adding nodes.
func (e environment) installLicense(t *testing.T, admin *browser, csrf string) {
	t.Helper()
	var state struct {
		DeploymentID string `json:"deployment_id"`
		Current      *struct {
			LicenseID string `json:"license_id"`
		} `json:"current"`
	}
	status, raw := admin.request(t, http.MethodGet, "/api/v1/admin/license", nil, "")
	decode(t, status, http.StatusOK, raw, &state)
	if state.Current != nil {
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	artifact, err := license.Issue(license.Claims{FormatVersion: license.FormatVersion, LicenseID: newUUID(t), CustomerReference: "End to end test", DeploymentID: state.DeploymentID,
		IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), NotBefore: now.Add(-time.Minute).Format(time.RFC3339),
		ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339), GraceUntil: now.Add(48 * time.Hour).Format(time.RFC3339), Scope: license.WholePlatformScope}, e.licenseKey)
	if err != nil {
		t.Fatal(err)
	}
	status, raw = admin.request(t, http.MethodPost, "/api/v1/admin/license", map[string]string{"artifact": string(artifact), "reason": "End to end test license", "expected_current_license_id": ""}, csrf)
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("license import: %d %s", status, raw)
	}
}

// startAgent mints a join token, enrolls the agent binary and starts it. The agent runs
// until owner, the test that spans the later subtests, finishes.
func (e environment) startAgent(t, owner *testing.T, password string) *agentProcess {
	t.Helper()
	if e.agent == "" {
		t.Fatal("OWNSOLID_E2E_AGENT names no agent binary")
	}
	// The agent reaches control by its admin origin, as on a real node.
	hosts, err := os.ReadFile("/etc/hosts")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hosts), "admin.ownsolid.test") {
		hosts = append(hosts, []byte("\n127.0.0.1 admin.ownsolid.test\n")...)
		if err = os.WriteFile("/etc/hosts", hosts, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	admin, csrf := e.adminSession(t, password)
	e.installLicense(t, admin, csrf)
	var workspaces struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	status, raw := admin.request(t, http.MethodGet, "/api/v1/admin/workspaces", nil, "")
	decode(t, status, http.StatusOK, raw, &workspaces)
	if len(workspaces.Items) == 0 {
		t.Fatal("no workspace")
	}
	var token struct {
		Token string `json:"token"`
	}
	status, raw = admin.request(t, http.MethodPost, "/api/v1/sites/default/cells/default/admin/node-join-tokens", map[string]string{"workspace_id": workspaces.Items[0].ID, "node_name": agentNodeName, "execution_profile": "read_only"}, csrf)
	decode(t, status, http.StatusCreated, raw, &token)

	directory, err := os.MkdirTemp("", "ownsolid-agent-")
	if err != nil {
		t.Fatal(err)
	}
	owner.Cleanup(func() { os.RemoveAll(directory) })
	stateDirectory := filepath.Join(directory, "state")
	if err = os.Mkdir(stateDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	authority, err := os.ReadFile(filepath.Join(e.root, "config", "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	writePrivate(t, filepath.Join(directory, "ca.pem"), authority)
	writePrivate(t, filepath.Join(directory, "join-token"), []byte(token.Token+"\n"))
	configuration, _ := json.Marshal(map[string]string{
		"control_origin": adminOrigin, "ca_file": filepath.Join(directory, "ca.pem"), "state_directory": stateDirectory,
		"disk_path": "/", "join_token_file": filepath.Join(directory, "join-token"),
	})
	configFile := filepath.Join(directory, "agent.json")
	writePrivate(t, configFile, configuration)
	if output, err := run(t, e.agent, "enroll", "--config", configFile); err != nil {
		t.Fatalf("enroll: %v\n%s", err, output)
	}

	a := &agentProcess{log: &lockedBuffer{}, admin: admin, csrf: csrf, binary: e.agent, configFile: configFile, stateDirectory: stateDirectory}
	a.start(t)
	owner.Cleanup(func() { a.stop(owner) })

	var nodes struct {
		Items []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	status, raw = admin.request(t, http.MethodGet, nodesPath, nil, "")
	decode(t, status, http.StatusOK, raw, &nodes)
	for _, n := range nodes.Items {
		if n.Name == agentNodeName {
			a.nodeID = n.ID
		}
	}
	if a.nodeID == "" {
		t.Fatalf("enrolled node is not listed: %s", raw)
	}
	return a
}

// runTask asks the agent to collect facts and waits until the task succeeds.
func (a *agentProcess) runTask(t *testing.T, within time.Duration) {
	t.Helper()
	tasks := nodesPath + "/" + a.nodeID + "/tasks"
	var task struct {
		ID string `json:"id"`
	}
	started := time.Now()
	status, raw := a.admin.requestWith(t, http.MethodPost, tasks, map[string]string{"operation": "collect_facts.v1"}, a.csrf, map[string]string{"Idempotency-Key": newUUID(t)})
	decode(t, status, http.StatusAccepted, raw, &task)
	for time.Since(started) < within {
		var list struct {
			Items []struct {
				ID    string `json:"id"`
				State string `json:"state"`
			} `json:"items"`
		}
		status, raw = a.admin.request(t, http.MethodGet, tasks, nil, "")
		decode(t, status, http.StatusOK, raw, &list)
		for _, item := range list.Items {
			if item.ID != task.ID {
				continue
			}
			switch item.State {
			case "succeeded":
				t.Logf("task %s succeeded %s after it was created", task.ID, time.Since(started).Round(time.Millisecond))
				return
			case "failed", "cancelled":
				t.Fatalf("task %s: %s\n%s", task.ID, item.State, a.output())
			}
		}
		select {
		case <-a.done:
			t.Fatalf("agent exited:\n%s", a.output())
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatalf("task %s did not succeed within %s\n%s", task.ID, within, a.output())
}

// start runs the enrolled agent; start again after stop resumes from its state directory.
func (a *agentProcess) start(t *testing.T) {
	t.Helper()
	a.done = make(chan struct{})
	a.cmd = exec.Command(a.binary, "run", "--config", a.configFile)
	a.cmd.Stdout, a.cmd.Stderr = a.log, a.log
	if err := a.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := a.done
	go func() { a.cmd.Wait(); close(done) }()
}

// cursor reads the highest task sequence the agent acknowledged (ADR 0011).
func (a *agentProcess) cursor(t *testing.T) uint64 {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(a.stateDirectory, "task-cursor.json"))
	if err != nil {
		t.Fatalf("task cursor: %v", err)
	}
	var value struct {
		AcknowledgedSequence uint64 `json:"acknowledged_sequence"`
	}
	if err = json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value.AcknowledgedSequence
}

// waitFor waits until the agent logged s.
func (a *agentProcess) waitFor(t *testing.T, s string, within time.Duration) {
	t.Helper()
	for started := time.Now(); time.Since(started) < within; time.Sleep(200 * time.Millisecond) {
		if a.logged(s) {
			return
		}
		if !a.running() {
			t.Fatalf("agent exited:\n%s", a.output())
		}
	}
	t.Fatalf("agent did not log %q within %s:\n%s", s, within, a.output())
}

func (a *agentProcess) output() string       { return a.log.String() }
func (a *agentProcess) logged(s string) bool { return strings.Contains(a.output(), s) }

func (a *agentProcess) running() bool {
	select {
	case <-a.done:
		return false
	default:
		return true
	}
}

func (a *agentProcess) stop(t *testing.T) {
	t.Helper()
	if !a.running() {
		return
	}
	a.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-a.done:
	case <-time.After(30 * time.Second):
		a.cmd.Process.Kill()
		<-a.done
	}
}

// assertTasksAcknowledged checks control's record for the node: every task succeeded on
// an acknowledged attempt, and the sequences are 1 to n.
func (e environment) assertTasksAcknowledged(t *testing.T, node string, n int) {
	t.Helper()
	query := "SELECT count(*) FILTER (WHERE state='succeeded' AND attempt>0 AND acknowledged_attempt=attempt)||'/'||count(*)||'/'||COALESCE(max(sequence),0)||'/'||(SELECT task_acknowledged_sequence FROM managed_nodes WHERE id='" + node + "') FROM node_tasks WHERE node_id='" + node + "'"
	output, err := e.compose(t, "exec", "--no-TTY", "postgres", "psql", "-X", "-tA", "-U", "ownsolid_migrator", "-d", "ownsolid", "-c", query)
	want := fmt.Sprintf("%d/%d/%d/%d", n, n, n, n)
	if err != nil || strings.TrimSpace(output) != want {
		t.Fatalf("task delivery record %q, want %s: %v", strings.TrimSpace(output), want, err)
	}
}

// sql runs one query as the database owner and returns its single trimmed answer.
func (e environment) sql(t *testing.T, query string) string {
	t.Helper()
	output, err := e.compose(t, "exec", "--no-TTY", "postgres", "psql", "-X", "-tA", "-v", "ON_ERROR_STOP=1", "-U", "ownsolid_migrator", "-d", "ownsolid", "-c", query)
	if err != nil {
		t.Fatalf("%s: %v %s", query, err, output)
	}
	return strings.TrimSpace(output)
}
