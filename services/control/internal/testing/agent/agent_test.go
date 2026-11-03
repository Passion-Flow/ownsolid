package agent_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

func TestM03ActualAgentUbuntuAMD64Emulation(t *testing.T) {
	binary := os.Getenv("PROJECT_ONE_AGENT_AMD64_BINARY")
	if binary == "" {
		t.Skip("explicit Linux amd64 agent binary required; emulation is separate from target qualification")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("absolute agent binary required")
	}
	s := fixture.NewM03(t)
	password := fixture.Bootstrap(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 175*time.Second)
	defer cancel()
	admin := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	service := &nodes.Service{DB: s.DB, Identity: s, Workspaces: &workspaces.Service{DB: s.DB, Identity: s}}
	cert, ca, _, _ := smtpsink.Certificate(t, t.TempDir(), "host.docker.internal")
	handler := transport.New(s)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	s.Config.Server.AdminOrigin = "https://host.docker.internal:" + port
	server.StartTLS()
	defer server.Close()
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	disk, e := filepath.EvalSymlinks("/Volumes/DevData/DevData/Docker/DockerDesktop/Docker.raw")
	if e != nil || !strings.HasPrefix(disk, "/Volumes/DevData/") {
		t.Fatal("external Docker VM disk is required for owned agent state")
	}
	volume := "projectone-m03-state-" + uuid.NewString()[:8]
	if exec.CommandContext(ctx, "docker", "volume", "create", "--label", "projectone.owner=mvp-development", volume).Run() != nil {
		t.Fatal("owned agent-state volume creation failed")
	}
	defer func() {
		label, _ := exec.Command("docker", "volume", "inspect", "--format", `{{index .Labels "projectone.owner"}}`, volume).Output()
		if strings.TrimSpace(string(label)) == "mvp-development" {
			exec.Command("docker", "volume", "rm", volume).Run()
		}
	}()
	if exec.CommandContext(ctx, "docker", "run", "--rm", "--pull", "never", "--platform", "linux/amd64", "--mount", "type=volume,src="+volume+",dst=/agent-state", "ubuntu@sha256:534baea6a22c03a63003dbc8dbe78fe34bc0d7e595d9a9dc9834884ff530eb55", "/usr/bin/install", "-d", "-m", "0700", "-o", strconv.Itoa(os.Getuid()), "-g", strconv.Itoa(os.Getgid()), "/agent-state/private").Run() != nil {
		t.Fatal("owned agent-state directory initialization failed")
	}
	publicCA, e := os.ReadFile(ca)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "ca.pem"), publicCA, 0600); e != nil {
		t.Fatal(e)
	}
	token, e := service.Mint(ctx, admin, admin.Me.Workspace.ID, "Actual emulated Ubuntu agent", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "join-token"), []byte(token.Token), 0600); e != nil {
		t.Fatal(e)
	}
	config := map[string]string{"control_origin": s.Config.Server.AdminOrigin, "ca_file": "/fixture/ca.pem", "state_directory": "/agent-state/private", "disk_path": "/", "join_token_file": "/fixture/join-token"}
	b, _ := json.Marshal(config)
	if e = os.WriteFile(filepath.Join(dir, "config.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	image := "ubuntu@sha256:534baea6a22c03a63003dbc8dbe78fe34bc0d7e595d9a9dc9834884ff530eb55"
	common := []string{"run", "--rm", "--pull", "never", "--platform", "linux/amd64", "--memory", "128m", "--cpus", "1", "--read-only", "--user", strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()), "--label", "projectone.owner=mvp-development", "--mount", "type=bind,src=" + binary + ",dst=/agent,readonly", "--mount", "type=bind,src=" + dir + ",dst=/fixture", "--mount", "type=volume,src=" + volume + ",dst=/agent-state"}
	run := func(args ...string) {
		t.Helper()
		command := exec.CommandContext(ctx, "docker", args...)
		if output, e := command.CombinedOutput(); e != nil {
			_ = output
			t.Fatal("ACTUAL_AGENT_CONTAINER_FAILED")
		}
	}
	enroll := append(append([]string{}, common...), image, "/agent", "enroll", "--config", "/fixture/config.json")
	run(enroll...)
	listing, e := service.List(ctx, admin, "", url.Values{})
	if e != nil || len(listing.Items) != 1 {
		t.Fatal("actual enrolled node not found", e)
	}
	local := struct{ NodeID string }{listing.Items[0].ID}
	task, e := service.CreateTask(ctx, admin, local.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	container := "projectone-m03-agent-" + uuid.NewString()[:8]
	defer exec.Command("docker", "rm", "--force", container).Run()
	retained := []string{}
	for _, argument := range common {
		if argument != "--rm" {
			retained = append(retained, argument)
		}
	}
	start := append(retained, "--name", container, "--detach", image, "/agent", "run", "--config", "/fixture/config.json")
	run(start...)
	deadline := time.Now().Add(25 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		status, _ := exec.Command("docker", "inspect", "--format", "{{.State.Status}}", container).Output()
		if strings.TrimSpace(string(status)) == "exited" {
			break
		}
		var state string
		if e = s.DB.QueryRow(ctx, "SELECT state FROM node_tasks WHERE id=$1", task.ID).Scan(&state); e != nil {
			t.Fatal(e)
		}
		if state == "succeeded" {
			completed = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !completed {
		diagnostic := append(append([]string{}, common...), image, "/usr/bin/stat", "-c", "%a %u %g %F", "/agent-state/private", "/fixture/config.json")
		metadata, _ := exec.Command("docker", diagnostic...).Output()
		t.Log("container file metadata:", strings.TrimSpace(string(metadata)))
		logs, _ := exec.Command("docker", "logs", container).CombinedOutput()
		codes := regexp.MustCompile(`code=([A-Z0-9_]+)`).FindAllStringSubmatch(string(logs), -1)
		for _, code := range codes {
			t.Log("agent error code:", code[1])
		}
		for _, line := range strings.Split(string(logs), "\n") {
			if regexp.MustCompile(`^[A-Z][A-Z0-9_]+$`).MatchString(strings.TrimSpace(line)) {
				t.Log("agent exit code:", strings.TrimSpace(line))
			}
		}
		status, _ := exec.Command("docker", "inspect", "--format", "{{.State.Status}} {{.State.ExitCode}}", container).Output()
		t.Log("container disposition:", strings.TrimSpace(string(status)))
		var state string
		var attempt int
		s.DB.QueryRow(ctx, "SELECT state,attempt FROM node_tasks WHERE id=$1", task.ID).Scan(&state, &attempt)
		t.Logf("durable task state=%s attempt=%d", state, attempt)
		t.Fatal("actual agent did not complete discovery task")
	}
	n, e := service.Get(ctx, admin, "", local.NodeID)
	if e != nil || n.Facts.OSID != "ubuntu" || n.Facts.OSVersion != "24.04" || n.Facts.Architecture != "amd64" || n.Connectivity != "online" {
		t.Fatal("real agent observation invalid", e)
	}
	run("stop", "--time", "3", container)
	// Exercise real wall-clock disconnect thresholds; no fixture timestamp is rewritten.
	sawUnknown, sawOffline := false, false
	disconnectDeadline := time.Now().Add(130 * time.Second)
	for time.Now().Before(disconnectDeadline) {
		observed, e := service.Get(ctx, admin, "", local.NodeID)
		if e != nil {
			t.Fatal(e)
		}
		if observed.Connectivity == "unknown" {
			sawUnknown = true
		}
		if observed.Connectivity == "offline" {
			sawOffline = true
			break
		}
		time.Sleep(time.Second)
	}
	if !sawUnknown || !sawOffline {
		t.Fatal("actual disconnection did not progress through Unknown/Offline")
	}
	// A second process resumes the same durable identity and does not enroll again.
	run("start", container)
	reconnected := false
	reconnectDeadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(reconnectDeadline) {
		observed, e := service.Get(ctx, admin, "", local.NodeID)
		if e != nil {
			t.Fatal(e)
		}
		if observed.Connectivity == "online" {
			reconnected = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !reconnected {
		t.Fatal("actual restarted agent did not reconcile connectivity")
	}
	if _, e = service.Update(ctx, admin, n.ID, n.Name, "removed", n.Version, "Synthetic integration revocation", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	wait := exec.CommandContext(ctx, "docker", "wait", container)
	exit, e := wait.Output()
	if e != nil || string(exit) != "77\n" {
		t.Fatal("revoked actual agent did not exit with protected status")
	}
	t.Log("actual Ubuntu 24.04 amd64 agent CLI enrollment, protected state, TLS, heartbeat, task completion, process restart and credential removal passed under Docker Desktop emulation; physical amd64/systemd qualification remains pending")
}
