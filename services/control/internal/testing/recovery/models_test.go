//go:build model_integration

package recovery_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

func TestM06ActualNativeRestartModelFileAndLogicalDatabaseRestore(t *testing.T) {
	binary := os.Getenv("PROJECT_ONE_CONTROL_BINARY")
	if binary == "" {
		t.Fatal("EXPLICIT_NATIVE_BINARY_REQUIRED")
	}
	fixture.PreflightOwnedPostgres(t)
	s := fixture.New(t)
	ownedDatabase := fixture.OwnedPostgres(t, s.DB)
	var e error
	password := fixture.Bootstrap(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	a := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	modelRoot := filepath.Join(dir, "source-models")
	os.Mkdir(modelRoot, 0700)
	model := uuid.NewString()
	object := filepath.Join(modelRoot, "objects", model)
	os.MkdirAll(object, 0700)
	source := filepath.Join(os.Getenv("PROJECT_ONE_STATE_ROOT"), "dependencies/models/qwen3-0.6b", wire.ApprovedModel().Revision, "files")
	// Hardlinks refer to actual frozen public bytes; export/restore below perform independent real copies.
	for _, file := range wire.ApprovedModel().Files {
		if e = os.Link(filepath.Join(source, file.Path), filepath.Join(object, file.Path)); e != nil {
			t.Fatal("actual external model required", e)
		}
	}
	catalog, _ := json.Marshal(wire.ApprovedModel())
	if _, e = s.DB.Exec(ctx, "INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Actual native restore model',$4,$5,$6,'ready',$7)", model, a.Me.Workspace.TenantID, a.Me.Workspace.ID, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), catalog, a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	w := &workspaces.Service{DB: s.DB, Identity: s}
	n := &nodes.Service{DB: s.DB, Identity: s, Workspaces: w}
	m := &models.Service{DB: s.DB, Identity: s, Workspaces: w, RootDirectory: modelRoot}
	d := &deployments.Service{Nodes: n, Models: m}
	n.Inference = d
	m.Referenced = d.HasModelReferences
	h := wire.Handshake{ProtocolVersion: wire.InferenceVersion, ExecutionProfile: wire.DockerInferenceProfile, Capabilities: wire.Capabilities(wire.DockerInferenceProfile), Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-native-restore-node", LogicalCPUs: 4, MemoryTotalBytes: 16 << 30, DiskPath: "/", DiskTotalBytes: 100 << 30, DiskAvailableBytes: 50 << 30, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}
	join, e := n.MintForProfile(ctx, a, a.Me.Workspace.ID, "Synthetic retained GPU restore", wire.DockerInferenceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	node, e := n.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: h}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	gpu := "GPU-12345678-1234-1234-1234-123456789abc"
	h.Inference = &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{{UUID: gpu, Name: "Synthetic restore GPU", DriverVersion: "580.1", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8, MIGMode: "Disabled", ObservedAt: time.Now().UTC()}}, ObservedAt: time.Now().UTC()}
	if _, e = n.Heartbeat(ctx, node.NodeID, node.Credential, h); e != nil {
		t.Fatal(e)
	}
	deployment, e := d.Create(ctx, a, deployments.CreateRequest{NodeID: node.NodeID, ModelID: model, GPUUUID: gpu, Reason: "Synthetic resource ownership in actual logical restore"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	claim, e := n.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil {
		t.Fatal("actual durable restore task", e)
	}
	write := func(name string, data []byte) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if e := os.WriteFile(path, data, 0600); e != nil {
			t.Fatal(e)
		}
		return path
	}
	dsn, e := url.Parse(s.DB.Config().ConnString())
	if e != nil {
		t.Fatal("test DSN invalid")
	}
	query := dsn.Query()
	query.Set("search_path", s.DB.Config().ConnConfig.RuntimeParams["search_path"])
	dsn.RawQuery = query.Encode()
	dsnFile := write("operator-dsn", []byte(dsn.String()))
	hmac := write("auth-hmac", s.HMACKey)
	keys, _ := json.Marshal(s.Mail.Keys)
	keyring := write("keyring.json", keys)
	blocklist := write("blocklist", []byte("passwordpassword\n"))
	_, ca, cert, key := smtpsink.Certificate(t, dir)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := listener.Addr().String()
	listener.Close()
	_, port, _ := net.SplitHostPort(address)
	origin := "https://127.0.0.1:" + port
	repo, _ := filepath.Abs("../../../../../")
	config := map[string]any{"server": map[string]any{"listen_address": address, "portal_origin": origin, "admin_origin": "https://localhost:" + port, "tls_certificate_file": cert, "tls_private_key_file": key, "portal_assets": filepath.Join(repo, "apps/web/apps/portal/dist"), "admin_assets": filepath.Join(repo, "apps/web/apps/admin/dist")}, "database": map[string]any{"dsn_file": dsnFile, "max_open_connections": 8, "max_idle_connections": 1, "statement_timeout_seconds": 5}, "security": map[string]any{"auth_hmac_key_file": hmac, "mail_keyring_file": keyring, "password_blocklist_file": blocklist}, "smtp": map[string]any{"host": "127.0.0.1", "port": 1, "from_address": "platform@example.invalid", "tls_mode": "starttls_required", "connect_timeout_seconds": 1, "operation_timeout_seconds": 2}, "models": map[string]any{"artifact_root": modelRoot}}
	config["server"].(map[string]any)["application_profile"] = "models"
	config["server"].(map[string]any)["portal_assets"] = os.Getenv("PROJECT_ONE_M06_PORTAL_ASSETS")
	config["server"].(map[string]any)["admin_assets"] = os.Getenv("PROJECT_ONE_M06_ADMIN_ASSETS")
	config["licensing"] = map[string]any{"trust_file": s.Config.Licensing.TrustFile}
	saveConfig := func() string { raw, _ := json.Marshal(config); return write("control.json", raw) }
	configPath := saveConfig()
	roots := x509.NewCertPool()
	caBytes, _ := os.ReadFile(ca)
	roots.AppendCertsFromPEM(caBytes)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, Jar: jar, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	var process *exec.Cmd
	stop := func() {
		if process == nil {
			return
		}
		process.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- process.Wait() }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			process.Process.Kill()
			<-done
		}
		process = nil
	}
	defer stop()
	start := func() {
		t.Helper()
		process = exec.CommandContext(ctx, binary, "serve", "--config", configPath)
		process.Stdout = os.Stdout
		process.Stderr = os.Stderr
		if e := process.Start(); e != nil {
			t.Fatal(e)
		}
		until := time.Now().Add(20 * time.Second)
		for time.Now().Before(until) {
			response, e := client.Get(origin + "/health/ready")
			if e == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					return
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("MODEL_STARTUP_READINESS_TIMEOUT")
	}
	command := func(name string, args ...string) error {
		cmd := exec.CommandContext(ctx, binary, append([]string{name, "--config", configPath, "--reason", "Actual isolated model-file and logical backup acceptance"}, args...)...)
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Log("operator failure", strings.TrimSpace(string(out)))
		}
		return e
	}
	login := func() {
		t.Helper()
		response, e := client.Get(origin + "/api/v1/auth/csrf")
		if e != nil {
			t.Fatal(e)
		}
		var envelope struct{ Data struct{ Token string } }
		json.NewDecoder(response.Body).Decode(&envelope)
		response.Body.Close()
		body, _ := json.Marshal(map[string]any{"email": "admin@example.invalid", "password": password})
		request, _ := http.NewRequest("POST", origin+"/api/v1/auth/login", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", origin)
		request.Header.Set("X-CSRF-Token", envelope.Data.Token)
		response, e = client.Do(request)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("restored portal login failed", response.StatusCode)
		}
	}
	assertModel := func() {
		t.Helper()
		response, e := client.Get(origin + "/api/v1/sites/default/cells/default/workspaces/" + a.Me.Workspace.ID + "/models")
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 65537))
		if response.StatusCode != 200 || !bytes.Contains(raw, []byte(model)) {
			t.Fatal("native scoped model inventory lost", response.StatusCode)
		}
	}
	start()
	login()
	assertModel()
	stop()
	start()
	assertModel()
	backup := filepath.Join(dir, "model-files")
	if command("export-model-files", "--backup-root", backup) == nil {
		t.Fatal("live control process permitted operator file backup")
	}
	if _, e = os.Stat(backup); !os.IsNotExist(e) {
		t.Fatal("failed installation lock created backup")
	}
	stop()
	if e = command("export-model-files", "--backup-root", backup); e != nil {
		t.Fatal("actual operator model export", e)
	}
	archive := ownedDatabase.DumpSchema(t, ctx)
	write("backup.dump", archive)
	restored := ownedDatabase.RestoreFresh(t, ctx, s.DB, archive, true)
	target := restored.Config().ConnConfig.Database
	dsn.Path = "/" + target
	write("operator-dsn", []byte(dsn.String()))
	restoredRoot := filepath.Join(dir, "restored-models")
	os.Mkdir(restoredRoot, 0700)
	config["models"].(map[string]any)["artifact_root"] = restoredRoot
	saveConfig()
	if e = command("invalidate-restored-auth", "--reason", "Actual isolated M06 file and database restore"); e != nil {
		t.Fatal(e)
	}
	if e = command("verify-model-store"); e == nil {
		t.Fatal("missing restored files accepted")
	}
	if e = command("import-model-files", "--backup-root", backup); e != nil {
		t.Fatal("actual operator model restore", e)
	}
	if e = command("verify-model-store"); e != nil {
		t.Fatal("actual restored file verification", e)
	}
	var exportedAudits, importedAudits int
	if e = restored.QueryRow(ctx, "SELECT count(*) FILTER(WHERE action='model.files_exported'),count(*) FILTER(WHERE action='model.files_imported') FROM audit_events WHERE audience='operator'").Scan(&exportedAudits, &importedAudits); e != nil || exportedAudits != 1 || importedAudits != 1 {
		t.Fatal("file backup/restore operator audit lost", e)
	}
	var taskState, resourceState, nodeState string
	var credentialRevoked bool
	if e = restored.QueryRow(ctx, "SELECT t.state,g.state,n.status,n.credential_hash IS NULL FROM node_tasks t JOIN deployments d ON d.last_task_id=t.id JOIN gpu_leases g ON g.deployment_id=d.id JOIN managed_nodes n ON n.id=d.node_id WHERE d.id=$1", deployment.ID).Scan(&taskState, &resourceState, &nodeState, &credentialRevoked); e != nil || taskState != "cancelled" || resourceState != "recovery_required" || nodeState != "removed" || !credentialRevoked {
		t.Fatal("restore fabricated free GPU or retained task authority", e)
	}
	start()
	response, e := client.Get(origin + "/api/v1/me")
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("restored session survived", response.StatusCode)
	}
	login()
	assertModel()
	stop()
	protected := filepath.Join(restoredRoot, "objects", model, "config.json")
	saved := protected + ".preserved"
	if e = os.Rename(protected, saved); e != nil {
		t.Fatal(e)
	}
	invalid := exec.CommandContext(ctx, binary, "serve", "--config", configPath)
	if out, e := invalid.CombinedOutput(); e == nil || !bytes.Contains(out, []byte("MODEL_FILE_SET_INVALID")) {
		t.Fatal("native startup accepted missing/unknown model file")
	}
	if e = os.Rename(saved, protected); e != nil {
		t.Fatal(e)
	}
	start()
	assertModel()
	stop()
	t.Log("Actual native process restart, operator process lock, approved immutable files, real pg_dump/pg_restore into a new isolated database, restored authority/resource fencing and missing-file startup recovery passed. GPU observations are explicitly synthetic; no NVIDIA qualification.")
}
