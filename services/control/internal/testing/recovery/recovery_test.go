package recovery_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	recovery "github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/logstorefixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

func TestM01ActualProcessRestartAndLogicalRestore(t *testing.T) {
	binary := os.Getenv("PROJECT_ONE_CONTROL_BINARY")
	if binary == "" {
		t.Skip("set PROJECT_ONE_CONTROL_BINARY for the isolated executable recovery profile")
	}
	fixture.PreflightOwnedPostgres(t)
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	ctx := context.Background()
	oldPassword := password
	if e := s.RequestReset(ctx, "admin@example.invalid", "portal", "restart-fixture", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	reset := fixture.ResetToken(t, s, "admin@example.invalid")
	password = fixture.Password()
	if e := s.Reset(ctx, reset, password, "portal", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	a := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	workspaceService := &workspaces.Service{DB: s.DB, Identity: s}
	workspace, e := workspaceService.Create(ctx, a, "Durable workspace", a.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	portalActor := fixture.Login(t, s, "admin@example.invalid", password, "portal")
	note, e := workspaceService.CreateNote(ctx, portalActor, workspace.ID, "Durable note", "SYNTHETIC_RESTORE_NOTE", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	nodeService := &nodes.Service{DB: s.DB, Identity: s, Workspaces: workspaceService}
	join, e := nodeService.Mint(ctx, a, workspace.ID, "Logical restore node", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	syntheticFacts := wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-logical-restore", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 1024, DiskAvailableBytes: 512, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}
	enrolled, e := nodeService.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: wire.Handshake{ProtocolVersion: wire.Version, Capabilities: []string{wire.CollectFacts}, Facts: syntheticFacts}}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	pendingJoin, e := nodeService.Mint(ctx, a, workspace.ID, "Pending logical restore node", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	nodeTask, e := nodeService.CreateTask(ctx, a, enrolled.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	// Component logs live in the log store, which a database restore leaves in place (ADR 0012).
	logStore := &logstorefixture.Memory{}
	componentService := &components.Service{Nodes: nodeService, LogStore: logStore}
	nodeService.Components = componentService
	componentJoin, e := nodeService.MintForProfile(ctx, a, workspace.ID, "Component restore node", wire.SystemdComponentsProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	componentHandshake := wire.Handshake{ProtocolVersion: wire.ManagementVersion, ExecutionProfile: wire.SystemdComponentsProfile, Capabilities: wire.Capabilities(wire.SystemdComponentsProfile), Facts: syntheticFacts}
	componentNode, e := nodeService.Enroll(ctx, wire.Enrollment{JoinToken: componentJoin.Token, Handshake: componentHandshake}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	// Synthetic typed observation verifies database backup integrity, not actual hardware metrics.
	componentHandshake.Component = &wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "running", Managed: true, ObservedAt: time.Now().UTC(), Metrics: &wire.ComponentMetrics{CPUSecondsTotal: 100, CPUIdleSecondsTotal: 80, MemoryTotalBytes: 1024, MemoryAvailableBytes: 512, FilesystemTotalBytes: 4096, FilesystemAvailableBytes: 2048}}
	if _, e = nodeService.Heartbeat(ctx, componentNode.NodeID, componentNode.Credential, componentHandshake); e != nil {
		t.Fatal(e)
	}
	componentTask, e := componentService.Request(ctx, a, componentNode.NodeID, "stop", uuid.NewString(), "Synthetic component logical restore", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}

	// Explicit synthetic maintenance data passes through real task/result transactions before pg_dump.
	componentService.CatalogDirectory = filepath.Join(os.Getenv("PROJECT_ONE_STATE_ROOT"), "dependencies/components/node-exporter")
	maintenanceJoin, e := nodeService.MintForProfile(ctx, a, workspace.ID, "Maintenance restore node", wire.SystemdMaintenanceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	maintenanceHandshake := wire.Handshake{ProtocolVersion: wire.MaintenanceVersion, ExecutionProfile: wire.SystemdMaintenanceProfile, Capabilities: wire.Capabilities(wire.SystemdMaintenanceProfile), Facts: syntheticFacts}
	maintenanceNode, e := nodeService.Enroll(ctx, wire.Enrollment{JoinToken: maintenanceJoin.Token, Handshake: maintenanceHandshake}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	configuration := wire.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
	maintenanceObservation := *componentHandshake.Component
	maintenanceObservation.Version = "1.12.0"
	maintenanceObservation.Config = &configuration
	maintenanceObservation.PreviousVersion = "1.12.1"
	maintenanceHandshake.Component = &maintenanceObservation
	if _, e = nodeService.Heartbeat(ctx, maintenanceNode.NodeID, maintenanceNode.Credential, maintenanceHandshake); e != nil {
		t.Fatal(e)
	}
	logsTask, e := componentService.Request(ctx, a, maintenanceNode.NodeID, "logs", uuid.NewString(), "Synthetic restore log snapshot", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	logsLease, e := nodeService.Claim(ctx, maintenanceNode.NodeID, maintenanceNode.Credential, uuid.NewString())
	if e != nil || logsLease.Lease == nil {
		t.Fatal("maintenance restore lease missing", e)
	}
	now := time.Now().UTC()
	logs := wire.ComponentLogs{Source: "systemd_journal", Available: true, ObservedAt: now, Since: now.Add(-time.Minute), Entries: []wire.ComponentLogEntry{{Timestamp: now, Priority: 6, Message: "SYNTHETIC_DURABLE_MAINTENANCE_LOG"}}}
	if _, e = nodeService.Complete(ctx, maintenanceNode.NodeID, maintenanceNode.Credential, logsTask.ID, wire.Result{LeaseToken: logsLease.Lease.LeaseToken, Outcome: "success", Component: &maintenanceObservation, Logs: &logs}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	maintenanceTask, e := componentService.RequestDetailed(ctx, a, maintenanceNode.NodeID, components.Request{Action: "upgrade", Version: "1.12.1", Reason: "Synthetic pending restored maintenance"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	i, e := s.Invite(ctx, a, "", "createInvitation", uuid.NewString(), "restore@example.invalid", "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	oldLink := fixture.InvitationToken(t, s, i.ID)
	root, _ := filepath.Abs("../../../../../")
	dir := t.TempDir()
	cert, ca, certFile, keyFile := smtpsink.Certificate(t, dir)
	_ = cert
	writeSecret := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if e := os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
		return p
	}
	dsnURL, e := url.Parse(s.DB.Config().ConnString())
	if e != nil {
		t.Fatal("FIXTURE_DSN_INVALID")
	}
	query := dsnURL.Query()
	query.Set("search_path", s.DB.Config().ConnConfig.RuntimeParams["search_path"])
	dsnURL.RawQuery = query.Encode()
	dsnFile := writeSecret("operator-dsn", []byte(dsnURL.String()))
	hmacFile := writeSecret("auth-hmac", s.HMACKey)
	keys, _ := json.Marshal(s.Mail.Keys)
	keyringFile := writeSecret("keyring.json", keys)
	blocklist := writeSecret("blocklist", []byte("passwordpassword\n"))
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := listener.Addr().String()
	listener.Close()
	_, port, _ := net.SplitHostPort(address)
	origin := "https://127.0.0.1:" + port
	conf := map[string]any{
		"server":   map[string]any{"listen_address": address, "portal_origin": origin, "admin_origin": "https://localhost:" + port, "tls_certificate_file": certFile, "tls_private_key_file": keyFile, "portal_assets": filepath.Join(root, "web/apps/portal/dist"), "admin_assets": filepath.Join(root, "web/apps/console/dist")},
		"database": map[string]any{"dsn_file": dsnFile, "max_open_connections": 10, "max_idle_connections": 1, "statement_timeout_seconds": 5},
		"security": map[string]any{"auth_hmac_key_file": hmacFile, "mail_keyring_file": keyringFile, "password_blocklist_file": blocklist},
		"smtp":     map[string]any{"host": "127.0.0.1", "port": 1, "from_address": "platform@example.invalid", "tls_mode": "starttls_required", "connect_timeout_seconds": 1, "operation_timeout_seconds": 2},
	}
	b, _ := json.Marshal(conf)
	configPath := writeSecret("control.json", b)
	roots := x509.NewCertPool()
	caBytes, _ := os.ReadFile(ca)
	roots.AppendCertsFromPEM(caBytes)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, Jar: jar, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	var process *exec.Cmd
	stop := func() {
		if process != nil {
			process.Process.Signal(syscall.SIGTERM)
			done := make(chan error, 1)
			go func() { done <- process.Wait() }()
			select {
			case <-done:
			case <-time.After(8 * time.Second):
				process.Process.Kill()
				<-done
			}
			process = nil
		}
	}
	defer stop()
	start := func() {
		process = exec.Command(binary, "serve", "--config", configPath)
		process.Stdout = os.Stdout
		process.Stderr = os.Stderr
		if e := process.Start(); e != nil {
			t.Fatal("CONTROL_PROCESS_START_FAILED")
		}
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			r, e := client.Get(origin + "/health/ready")
			if e == nil {
				r.Body.Close()
				if r.StatusCode == 200 {
					return
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("CONTROL_READINESS_TIMEOUT")
	}
	request := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		var payload []byte
		headers := http.Header{}
		if method == "POST" {
			r, e := client.Get(origin + "/api/v1/auth/csrf")
			if e != nil {
				t.Fatal("CSRF_FETCH_FAILED")
			}
			var envelope struct {
				Data struct {
					Token string `json:"token"`
				}
			}
			json.NewDecoder(r.Body).Decode(&envelope)
			r.Body.Close()
			headers.Set("X-CSRF-Token", envelope.Data.Token)
			headers.Set("Origin", origin)
			if body != nil {
				payload, _ = json.Marshal(body)
				headers.Set("Content-Type", "application/json")
			}
		}
		r, _ := http.NewRequest(method, origin+path, bytes.NewReader(payload))
		r.Header = headers
		response, e := client.Do(r)
		if e != nil {
			t.Fatal("CONTROL_REQUEST_FAILED")
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("%s expected %d observed %d", path, want, response.StatusCode)
		}
		if want == 204 {
			return nil
		}
		var envelope map[string]any
		json.NewDecoder(response.Body).Decode(&envelope)
		if want >= 400 {
			return envelope
		}
		return envelope["data"].(map[string]any)
	}
	start()
	request("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200)
	request("GET", "/api/v1/me", nil, 200)
	request("GET", "/api/v1/workspaces/"+workspace.ID+"/notes/"+note.ID, nil, 200)
	for n := 0; n < 9; n++ {
		request("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": "Synthetic wrong passphrase"}, 401)
	}
	request("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 429)
	stop()
	start()
	request("GET", "/api/v1/workspaces/"+workspace.ID+"/notes/"+note.ID, nil, 200)
	request("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 429)
	if _, e = s.DB.Exec(ctx, "UPDATE rate_limit_buckets SET window_started_at=clock_timestamp()-interval '2 hours',expires_at=clock_timestamp()-interval '1 hour'"); e != nil {
		t.Fatal(e)
	}
	request("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200)
	request("GET", "/api/v1/me", nil, 200)
	stop()
	start()
	request("GET", "/api/v1/me", nil, 200)
	request("POST", "/api/v1/auth/logout", nil, 204)
	stop()
	start()
	request("GET", "/api/v1/me", nil, 401)
	request("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": oldPassword}, 401)
	stop()
	// Scope all export/restore/restart actions to the verified disposable fixture.
	ownedDatabase := fixture.OwnedPostgres(t, s.DB)
	container := ownedDatabase.Container
	archive := ownedDatabase.DumpSchema(t, ctx)
	writeSecret("backup.dump", archive)
	restored := ownedDatabase.RestoreFresh(t, ctx, s.DB, archive)
	targetDB := restored.Config().ConnConfig.Database

	if e = database.Check(ctx, restored); e != nil {
		t.Fatal(e)
	}
	recovered := *s
	recovered.DB = restored
	outbox := *s.Mail
	outbox.DB = restored
	recovered.Mail = &outbox
	if e = recovery.InvalidateRestored(ctx, &recovered, "Isolated restore validation"); e != nil {
		t.Fatal(e)
	}
	restoredNodes := &nodes.Service{DB: restored, Identity: &recovered, Workspaces: &workspaces.Service{DB: restored, Identity: &recovered}}
	if _, e = restoredNodes.Heartbeat(ctx, enrolled.NodeID, enrolled.Credential, wire.Handshake{ProtocolVersion: wire.Version, Capabilities: []string{wire.CollectFacts}, Facts: syntheticFacts}); e == nil || e.Error() != "AGENT_UNAUTHORIZED" {
		t.Fatal("restored node credential survived", e)
	}
	if _, e = restoredNodes.Enroll(ctx, wire.Enrollment{JoinToken: pendingJoin.Token, Handshake: wire.Handshake{ProtocolVersion: wire.Version, Capabilities: []string{wire.CollectFacts}, Facts: syntheticFacts}}, uuid.NewString()); e == nil || e.Error() != "TOKEN_UNUSABLE" {
		t.Fatal("restored join token survived", e)
	}
	var restoredTaskState string
	if e = restored.QueryRow(ctx, "SELECT state FROM node_tasks WHERE id=$1", nodeTask.ID).Scan(&restoredTaskState); e != nil || restoredTaskState != "cancelled" {
		t.Fatal("logical restore job still executable", e)
	}
	var retainedHostname string
	if e = restored.QueryRow(ctx, "SELECT facts->>'hostname' FROM managed_nodes WHERE id=$1", enrolled.NodeID).Scan(&retainedHostname); e != nil || retainedHostname != syntheticFacts.Hostname {
		t.Fatal("logical restore lost node inventory", e)
	}
	if _, e = recovered.ValidateInvitation(ctx, oldLink); e == nil {
		t.Fatal("restored invitation authority survived")
	}
	var pending int
	if e = restored.QueryRow(ctx, "SELECT count(*) FROM mail_deliveries WHERE state IN ('queued','sending')").Scan(&pending); e != nil || pending != 0 {
		t.Fatal("restored mail survived")
	}
	verifyComponent := func() {
		if e = restored.QueryRow(ctx, "SELECT state FROM node_tasks WHERE id=$1", componentTask.ID).Scan(&restoredTaskState); e != nil || restoredTaskState != "cancelled" {
			t.Fatal("restored component task remained executable", e)
		}
		restoredComponents := &components.Service{Nodes: restoredNodes, LogStore: logStore}
		freshPortal := fixture.Login(t, &recovered, "admin@example.invalid", password, "portal")
		componentView, e := restoredComponents.Get(ctx, freshPortal, workspace.ID, componentNode.NodeID)
		if e != nil || componentView.DesiredState != "stopped" || componentView.Observation == nil || componentView.Observation.Metrics == nil || componentView.Observation.Metrics.MemoryTotalBytes != 1024 || !componentView.Stale || componentView.ManagementAvailable {
			t.Fatal("component restore lost data or fabricated authority/freshness", e)
		}
	}
	verifyMaintenance := func() {
		if _, e = restoredNodes.Heartbeat(ctx, maintenanceNode.NodeID, maintenanceNode.Credential, maintenanceHandshake); e == nil || e.Error() != "AGENT_UNAUTHORIZED" {
			t.Fatal("restored maintenance authority survived", e)
		}
		if e = restored.QueryRow(ctx, "SELECT state FROM node_tasks WHERE id=$1", maintenanceTask.ID).Scan(&restoredTaskState); e != nil || restoredTaskState != "cancelled" {
			t.Fatal("restored maintenance task executable", e)
		}
		freshAdmin := fixture.Login(t, &recovered, "admin@example.invalid", password, "admin")
		restoredComponents := &components.Service{Nodes: restoredNodes, LogStore: logStore}
		view, e := restoredComponents.Get(ctx, freshAdmin, "", maintenanceNode.NodeID)
		if e != nil || view.Version != "1.12.0" || view.DesiredVersion != "1.12.1" || view.AppliedConfig == nil || *view.AppliedConfig != configuration || view.LastVerifiedAt == nil || view.PreviousVersion == nil || *view.PreviousVersion != "1.12.1" || !view.Stale || view.MaintenanceAvailable {
			t.Fatal("maintenance restore lost data or fabricated authority", e)
		}
		snapshot, e := restoredComponents.Logs(ctx, freshAdmin, maintenanceNode.NodeID)
		if e != nil || snapshot.Snapshot == nil || len(snapshot.Snapshot.Entries) != 1 || snapshot.Snapshot.Entries[0].Message != "SYNTHETIC_DURABLE_MAINTENANCE_LOG" || !snapshot.Stale {
			t.Fatal("maintenance log restore lost bounded snapshot", e)
		}
	}
	verifyComponent()
	verifyMaintenance()
	fixture.Login(t, &recovered, "admin@example.invalid", password, "admin")
	restoredWorkspaces := &workspaces.Service{DB: restored, Identity: &recovered}
	verifyNote := func() {
		actor := fixture.Login(t, &recovered, "admin@example.invalid", password, "portal")
		read, e := restoredWorkspaces.GetNote(ctx, actor, workspace.ID, note.ID)
		if e != nil || read.Text != note.Text || read.Version != note.Version {
			t.Fatal("workspace/note restore lost data", e)
		}
	}
	verifyNote()
	// Restart only the explicitly owned disposable database and verify persistence.
	grace, graceError := strconv.Atoi(os.Getenv("PROJECT_ONE_FIXTURE_RESTART_GRACE_SECONDS"))
	ready, readyError := strconv.Atoi(os.Getenv("PROJECT_ONE_FIXTURE_RECOVERY_SECONDS"))
	if graceError != nil || readyError != nil || grace < 10 || grace > 120 || ready < 15 || ready > 180 {
		t.Fatal("BOUNDED_DEVELOPMENT_DATABASE_RESTART_POLICY_REQUIRED")
	}
	restartContext, restartCancel := context.WithTimeout(ctx, time.Duration(grace+ready)*time.Second)
	defer restartCancel()
	if e = exec.CommandContext(restartContext, "docker", "--context", ownedDatabase.Context, "restart", "--timeout", strconv.Itoa(grace), container).Run(); e != nil {
		t.Fatal("OWNED_DATABASE_RESTART_FAILED")
	}
	readyContext, readyCancel := context.WithTimeout(restartContext, time.Duration(ready)*time.Second)
	defer readyCancel()
	for {
		probeContext, probeCancel := context.WithTimeout(readyContext, time.Second)
		e = restored.Ping(probeContext)
		probeCancel()
		if e == nil {
			break
		}
		select {
		case <-readyContext.Done():
			t.Fatal("OWNED_DATABASE_READINESS_TIMEOUT")
		case <-time.After(100 * time.Millisecond):
		}
	}
	verifyComponent()
	verifyMaintenance()
	fixture.Login(t, &recovered, "admin@example.invalid", password, "admin")
	verifyNote()
	if e = restored.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='operator.auth_invalidated'").Scan(&pending); e != nil || pending != 1 {
		t.Fatal("restore audit not durable")
	}
	t.Logf("actual control restart, PostgreSQL restart, pg_dump/pg_restore into isolated %s, authority invalidation passed; archive not retained in evidence", targetDB)
}
