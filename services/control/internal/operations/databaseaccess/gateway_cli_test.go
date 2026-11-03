//go:build m07_delivery_integration || m08_delivery_integration || m09_delivery_integration

package databaseaccess

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
	gatewaywire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// Explicitly selected by the M06 delivery runner after building the real CLI.
func TestM07NativeUpgradeCommand(t *testing.T) { testNativeCumulativeUpgrade(t, false) }
func TestM08NativeUpgradeCommand(t *testing.T) { testNativeCumulativeUpgrade(t, true) }
func TestM09NativeUpgradeCommand(t *testing.T) { testNativeCumulativeUpgrade(t, true, true) }
func testNativeCumulativeUpgrade(t *testing.T, data bool, training ...bool) {
	train := len(training) == 1 && training[0]
	current, priorProfile, profile, priorMilestone, upgrade, complete := "M07", "models", "gateway", "M06", GatewayUpgradeCommand, GatewayUpgradeComplete
	if data {
		current, priorProfile, profile, priorMilestone, upgrade, complete = "M08", "gateway", "datasets", "M07", DatasetsUpgradeCommand, DatasetsUpgradeComplete
	}
	if train {
		current, priorProfile, profile, priorMilestone, upgrade, complete = "M09", "datasets", "training", "M08", TrainingUpgradeCommand, TrainingUpgradeComplete
	}
	binary := os.Getenv("PROJECT_ONE_" + current + "_CONTROL_BINARY")
	if !filepath.IsAbs(binary) {
		t.Fatal("ACTUAL_M07_CONTROL_BINARY_REQUIRED")
	}
	f := newActualFixture(t, priorProfile)
	ctx := t.Context()
	if status, e := Prepare(ctx, f.service.DB, f.lease, f.input); e != nil || status != Verified {
		t.Fatal(status, e)
	}
	password := identityfixture.Bootstrap(t, f.service)
	c, e := config.Load(filepath.Clean("../../../config/examples/control/" + profile + ".example.yaml"))
	if e != nil {
		t.Fatal(e)
	}
	c.Database.MaxOpenConnections = f.input.configuration.Database.MaxOpenConnections
	c.Database.MaxIdleConnections = 1
	root := t.TempDir()
	c.Models.ArtifactRoot = f.service.Config.Models.ArtifactRoot
	if train {
		c.Datasets = f.service.Config.Datasets
		c.Datasets.PythonExecutable = os.Getenv("PROJECT_ONE_DATA_PYTHON")
		c.Datasets.ProcessorFile = os.Getenv("PROJECT_ONE_DATA_PROCESSOR_FILE")
		f.service.Config.Datasets = c.Datasets
		c.Training.ArtifactRoot = filepath.Join(root, "training")
		release, e := os.ReadFile(os.Getenv("PROJECT_ONE_TRAINING_RELEASE"))
		if e != nil {
			t.Fatal(e)
		}
		var image struct {
			ImageReference     string `json:"image_reference"`
			ImageID            string `json:"image_id"`
			WorkerSourceSHA256 string `json:"worker_source_sha256"`
		}
		if json.Unmarshal(release, &image) != nil {
			t.Fatal("manufactured training recipe required")
		}
		c.Training.ImageReference, c.Training.ImageID, c.Training.WorkerSourceSHA256 = image.ImageReference, image.ImageID, image.WorkerSourceSHA256
	}
	checkPreserved := seedDeliveredM06GatewayUpgrade(t, f, password, c.Models.ArtifactRoot, data, train)
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	write := func(name string, raw []byte) string {
		p := filepath.Join(root, name)
		if e := os.WriteFile(p, raw, 0600); e != nil {
			t.Fatal(e)
		}
		return p
	}
	ds := func(runtime bool) string {
		source := f.service.DB.Config().ConnConfig
		user, secret := source.User, source.Password
		if runtime {
			user, secret = f.input.connection.ConnConfig.User, f.input.connection.ConnConfig.Password
		}
		u := url.URL{Scheme: "postgresql", Host: source.Host + ":" + strconv.Itoa(int(source.Port)), Path: "/" + source.Database, User: url.UserPassword(user, secret)}
		q := url.Values{"sslmode": {"disable"}, "search_path": {source.RuntimeParams["search_path"]}}
		if source.TLSConfig != nil {
			input := os.Getenv("PROJECT_ONE_TEST_DSN")
			if input == "" {
				raw, e := config.Secret(os.Getenv("PROJECT_ONE_TEST_DSN_FILE"))
				if e != nil {
					t.Fatal(e)
				}
				input = strings.TrimSpace(string(raw))
			}
			original, e := url.Parse(input)
			if e != nil {
				t.Fatal(e)
			}
			q.Set("sslmode", "verify-full")
			q.Set("sslrootcert", original.Query().Get("sslrootcert"))
		}
		u.RawQuery = q.Encode()
		return u.String()
	}
	configuration := func(name, dsn string) string {
		copy := c
		copy.Database.DSNFile = write(name+"-dsn", []byte(dsn))
		raw, e := yaml.Marshal(copy)
		if e != nil {
			t.Fatal(e)
		}
		return write(name+".yaml", raw)
	}
	if data && !train {
		c.Datasets = config.DatasetConfiguration{ArtifactRoot: filepath.Join(root, "datasets"), PythonExecutable: os.Getenv("PROJECT_ONE_DATA_PYTHON"), ProcessorFile: os.Getenv("PROJECT_ONE_DATA_PROCESSOR_FILE")}
	}
	operator, runtime := configuration("operator", ds(false)), configuration("runtime", ds(true))
	// Release the fixture's lease so the production CLI must obtain its own lock.
	if _, e = f.lease.Exec(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID); e != nil {
		t.Fatal(e)
	}
	prior := os.Getenv("PROJECT_ONE_" + priorMilestone + "_CONTROL_BINARY")
	if !filepath.IsAbs(prior) {
		t.Fatal("DELIVERED_M06_BINARY_REQUIRED")
	}
	old := c
	oldProfile := priorProfile
	old.Server.ApplicationProfile = &oldProfile
	old.Models.ArtifactRoot = c.Models.ArtifactRoot
	if !train {
		old.Datasets = config.DatasetConfiguration{}
	}
	old.Training = config.TrainingConfiguration{}
	old.Database.DSNFile = write("old-operator-dsn", []byte(ds(false)))
	oldRaw, _ := yaml.Marshal(old)
	oldOperator := write("old-operator.yaml", oldRaw)
	old.Database.DSNFile = write("old-runtime-dsn", []byte(ds(true)))
	oldRaw, _ = yaml.Marshal(old)
	oldRuntime := write("old-runtime.yaml", oldRaw)
	if output, e := exec.CommandContext(ctx, prior, "check-runtime-access", "--config", oldOperator, "--runtime-config", oldRuntime).CombinedOutput(); e != nil {
		t.Fatalf("delivered M06 existing-role check: %v %s", e, output)
	}
	for i := 0; i < 2; i++ {
		command := exec.CommandContext(ctx, binary, upgrade, "--config", operator, "--runtime-config", runtime)
		output, e := command.CombinedOutput()
		if e != nil || strings.TrimSpace(string(output)) != complete {
			t.Fatalf("native upgrade failed: %v %s", e, output)
		}
	}
	checkPreserved()
	f.input.configuration.Server.ApplicationProfile = &profile
	if train {
		f.input.configuration.Datasets, f.input.configuration.Training = c.Datasets, c.Training
	}
	if _, e = f.lease.Exec(ctx, "SELECT pg_advisory_lock($1)", database.InstallationLockID); e != nil {
		t.Fatal(e)
	}
	if status, e := Check(ctx, f.service.DB, f.lease, f.input); e != nil || status != Verified {
		t.Fatal(status, e)
	}
	f.service.Config.Server.ApplicationProfile = &profile
	f.service.Config.Datasets = c.Datasets
	f.service.Config.Training = c.Training
	if data && !train {
		if e = os.Mkdir(c.Datasets.ArtifactRoot, 0700); e != nil {
			t.Fatal(e)
		}
		if _, e = f.lease.Exec(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID); e != nil {
			t.Fatal(e)
		}
		for _, commandName := range []string{"initialize-dataset-store", "check-dataset-store-initialization"} {
			args := []string{commandName, "--config", operator, "--dataset-store-root", c.Datasets.ArtifactRoot, "--owner-uid", strconv.Itoa(os.Getuid()), "--owner-gid", strconv.Itoa(os.Getgid())}
			if commandName == "initialize-dataset-store" {
				args = append(args, "--confirm-empty-dataset-store", "--reason", "Actual cumulative native dataset namespace")
			}
			output, e := exec.CommandContext(ctx, binary, args...).CombinedOutput()
			if e != nil || strings.TrimSpace(string(output)) != "STORE_INITIALIZATION_VERIFIED" {
				t.Fatalf("actual cumulative dataset initializer %s: %v %s", commandName, e, output)
			}
		}
		if _, e = f.lease.Exec(ctx, "SELECT pg_advisory_lock($1)", database.InstallationLockID); e != nil {
			t.Fatal(e)
		}
	}
	if train {
		if e = os.Mkdir(c.Training.ArtifactRoot, 0700); e != nil {
			t.Fatal(e)
		}
		if _, e = f.lease.Exec(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID); e != nil {
			t.Fatal(e)
		}
		for _, commandName := range []string{"initialize-training-store", "check-training-store-initialization"} {
			args := []string{commandName, "--config", operator, "--training-store-root", c.Training.ArtifactRoot, "--owner-uid", strconv.Itoa(os.Getuid()), "--owner-gid", strconv.Itoa(os.Getgid())}
			if commandName == "initialize-training-store" {
				args = append(args, "--confirm-empty-training-store", "--reason", "Actual cumulative M09 training namespace")
			}
			output, e := exec.CommandContext(ctx, binary, args...).CombinedOutput()
			if e != nil || strings.TrimSpace(string(output)) != "STORE_INITIALIZATION_VERIFIED" {
				t.Fatalf("training initializer %s: %v %s", commandName, e, output)
			}
		}
		if _, e = f.lease.Exec(ctx, "SELECT pg_advisory_lock($1)", database.InstallationLockID); e != nil {
			t.Fatal(e)
		}
	}
	admin := identityfixture.Login(t, f.service, "admin@example.invalid", password, "admin")
	portal := identityfixture.Login(t, f.service, "admin@example.invalid", password, "portal")
	w := workspaces.Service{DB: f.service.DB, Identity: f.service}
	workspace, e := w.Create(ctx, admin, "Native restart Workspace", admin.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	note, e := w.CreateNote(ctx, portal, workspace.ID, "Native retained note", "M07_RESTART_PERSISTED", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, token, _, e := f.service.Login(ctx, "admin@example.invalid", password, "portal", "", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	c.Server.PortalAssets = os.Getenv("PROJECT_ONE_" + current + "_PORTAL_ASSETS")
	c.Server.AdminAssets = os.Getenv("PROJECT_ONE_" + current + "_ADMIN_ASSETS")
	if !filepath.IsAbs(c.Server.PortalAssets) || !filepath.IsAbs(c.Server.AdminAssets) {
		t.Fatal("ACTUAL_M07_ASSETS_REQUIRED")
	}
	socket, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := socket.Addr().(*net.TCPAddr).Port
	socket.Close()
	c.Server.ListenAddress = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	c.Server.TransportMode = config.TransportHTTP
	c.Server.TLSCertificateFile = ""
	c.Server.TLSPrivateKeyFile = ""
	c.Server.PortalOrigin = "http://localhost:" + strconv.Itoa(port)
	c.Server.AdminOrigin = "http://127.0.0.1:" + strconv.Itoa(port)
	machineSocket, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	machineAddress := machineSocket.Addr().String()
	machineSocket.Close()
	_, caFile, certFile, keyFile := smtpsink.Certificate(t, root)
	c.MachineServer = &config.MachineServerConfiguration{ListenAddress: machineAddress, Origin: "https://" + machineAddress, TLSCertificateFile: certFile, TLSPrivateKeyFile: keyFile}
	caRaw, e := os.ReadFile(caFile)
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caRaw) {
		t.Fatal("CA")
	}
	machineClient := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}}
	defer machineClient.CloseIdleConnections()
	n := nodes.Service{DB: f.service.DB, Identity: f.service, Workspaces: &w}
	n.Components = &components.Service{Nodes: &n, ArchiveFile: c.Components.NodeExporterArchiveFile}
	join, e := n.MintForProfile(ctx, admin, workspace.ID, "Native persisted node", wire.SystemdMaintenanceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	facts := wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-native", LogicalCPUs: 2, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 2048, DiskAvailableBytes: 1024, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}
	handshake := wire.Handshake{ProtocolVersion: wire.MaintenanceVersion, ExecutionProfile: wire.SystemdMaintenanceProfile, Capabilities: wire.Capabilities(wire.SystemdMaintenanceProfile), Facts: facts}
	enrolled, e := n.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: handshake}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	task, e := n.Components.(*components.Service).Request(ctx, admin, enrolled.NodeID, "start", uuid.NewString(), "Synthetic native component restart", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	machine := func(path string, input any) json.RawMessage {
		t.Helper()
		raw, _ := json.Marshal(input)
		req, e := http.NewRequestWithContext(ctx, "POST", c.MachineServer.Origin+"/agent/v1/nodes/"+enrolled.NodeID+path, bytes.NewReader(raw))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+enrolled.Credential)
		response, e := machineClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		var body struct {
			Data json.RawMessage `json:"data"`
		}
		if e = json.NewDecoder(response.Body).Decode(&body); e != nil || response.StatusCode != 200 {
			t.Fatal("native verified HTTPS machine", response.StatusCode, e)
		}
		return body.Data
	}
	var lease wire.Claim
	c.Security.AuthHMACKeyFile = write("hmac", f.service.HMACKey)
	c.Security.MailKeyringFile = filepath.Join(filepath.Dir(f.service.Config.Licensing.TrustFile), "keyring.json")
	c.Security.PasswordBlocklistFile = filepath.Join(os.Getenv("PROJECT_ONE_STATE_ROOT"), "dependencies/security/password-blocklist.txt")
	c.Licensing = f.service.Config.Licensing
	c.SMTP.Host = "127.0.0.1"
	c.SMTP.CAFile = ""
	runtime = configuration("native-serve", ds(true))
	client := &http.Client{Timeout: 2 * time.Second}
	policy, _ := httpserver.NewCookiePolicy(config.TransportHTTP)
	get := func(path string) (int, []byte, error) {
		r, e := http.NewRequestWithContext(ctx, "GET", c.Server.PortalOrigin+path, nil)
		if e != nil {
			return 0, nil, e
		}
		r.AddCookie(&http.Cookie{Name: policy.Name("portal", httpserver.SessionCookie), Value: token})
		response, e := client.Do(r)
		if e != nil {
			return 0, nil, e
		}
		defer response.Body.Close()
		raw, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return response.StatusCode, raw, e
	}
	for iteration := 0; iteration < 3; iteration++ {
		if _, e = f.lease.Exec(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID); e != nil {
			t.Fatal(e)
		}
		log, e := os.Create(filepath.Join(os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"), fmt.Sprintf("native-m07-serve-%d.log", iteration)))
		if e != nil {
			t.Fatal(e)
		}
		command := exec.CommandContext(ctx, binary, "serve", "--config", runtime)
		command.Stdout = log
		command.Stderr = log
		if e = command.Start(); e != nil {
			log.Close()
			t.Fatal(e)
		}
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		stopped := false
		stop := func() {
			if stopped {
				return
			}
			stopped = true
			_ = command.Process.Signal(os.Interrupt)
			select {
			case e := <-done:
				if e != nil {
					t.Error("native serving exit", e)
				}
			case <-time.After(10 * time.Second):
				_ = command.Process.Kill()
				<-done
				t.Error("native serving shutdown timeout")
			}
			log.Close()
		}
		t.Cleanup(stop)
		ready := false
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			status, _, e := get("/health/ready")
			if e == nil && status == 200 {
				ready = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !ready {
			t.Fatal("native M07 serving readiness failed; inspect retained serve log")
		}
		for _, path := range []string{"/api/v1/workspaces/" + workspace.ID + "/models", "/api/v1/workspaces/" + workspace.ID + "/deployments", "/api/v1/workspaces/" + workspace.ID + "/distribution/aliases", "/api/v1/workspaces/" + workspace.ID + "/distribution/keys", "/api/v1/workspaces/" + workspace.ID + "/distribution/requests", "/api/v1/workspaces/" + workspace.ID, "/api/v1/workspaces/" + workspace.ID + "/notes/" + note.ID, "/api/v1/workspaces/" + workspace.ID + "/notes/" + note.ID + "/export"} {
			status, raw, e := get(path)
			if e != nil || status != 200 {
				t.Fatal("native M07 persisted read failed", status, e)
			}
			if strings.Contains(path, "/notes/") && !strings.Contains(string(raw), "M07_RESTART_PERSISTED") {
				t.Fatal("native persisted note missing")
			}
		}
		if data {
			if status, _, e := get("/api/v1/workspaces/" + workspace.ID + "/datasets"); e != nil || status != 200 {
				t.Fatal("native cumulative datasets", status, e)
			}
		}
		if train {
			if status, _, e := get("/api/v1/workspaces/" + workspace.ID + "/training/jobs"); e != nil || status != 200 {
				t.Fatal("native cumulative training", status, e)
			}
		}
		status, _, e := get("/api/v1/workspaces/" + workspace.ID + "/nodes/" + enrolled.NodeID)
		if e != nil || status != 200 {
			t.Fatal("native node read", status, e)
		}

		status, raw, e := get("/api/v1/workspaces/" + workspace.ID + "/nodes/" + enrolled.NodeID + "/components/node-exporter")
		if e != nil || status != 200 {
			t.Fatal("native component read", status, e)
		}
		if !bytes.Contains(raw, []byte(task.ID)) {
			t.Fatal("component intent did not survive restart")
		}
		if iteration == 2 && !bytes.Contains(raw, []byte("COMPONENT_SYSTEMD_UNAVAILABLE")) {
			t.Fatal("component current observation did not survive restart")
		}
		machine("/heartbeat", handshake)
		if iteration == 0 {
			if e = json.Unmarshal(machine("/tasks/claims", map[string]any{}), &lease); e != nil || lease.Lease == nil {
				t.Fatal("native claim", e)
			}
		} else {
			machine("/tasks/"+task.ID+"/results", wire.Result{LeaseToken: lease.Lease.LeaseToken, Outcome: "failure", ErrorCode: "COMPONENT_SYSTEMD_UNAVAILABLE", Component: &wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "unknown", ErrorCode: "COMPONENT_SYSTEMD_UNAVAILABLE", ObservedAt: facts.ObservedAt}})
			machine("/tasks/"+task.ID+"/results", wire.Result{LeaseToken: lease.Lease.LeaseToken, Outcome: "failure", ErrorCode: "COMPONENT_SYSTEMD_UNAVAILABLE", Component: &wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "unknown", ErrorCode: "COMPONENT_SYSTEMD_UNAVAILABLE", ObservedAt: facts.ObservedAt}})
		}
		// The shipped executable must reject disabled descendants after successful
		// account/note/export controls, before and after the actual restart.
		disabled := []string{"nodes/" + enrolled.NodeID + "/components/node-exporter/logs", "training", "knowledge-bases", "workflows", "plugins", "middleware", "aws", "private-clusters"}
		if train {
			disabled[1] = "training/jobs/" + uuid.NewString() + "/evaluations"
		}
		if !data {
			disabled = append(disabled, "datasets")
		}
		for _, tail := range disabled {
			status, raw, e := get("/api/v1/workspaces/" + workspace.ID + "/" + tail)
			var problem struct {
				Code   string `json:"code"`
				Status int    `json:"status"`
			}
			if e != nil || status != 404 || json.Unmarshal(raw, &problem) != nil || problem.Code != "RESOURCE_NOT_FOUND" || problem.Status != 404 {
				t.Fatal("native disabled-module routing regression", tail, status, e)
			}
		}
		stop()
		if _, e = f.lease.Exec(ctx, "SELECT pg_advisory_lock($1)", database.InstallationLockID); e != nil {
			t.Fatal(e)
		}
	}

	// The same delivered executable refuses a different installed catalog before serving.
	nodesProfile := "nodes"
	c.Models.ArtifactRoot = ""
	c.Datasets = config.DatasetConfiguration{}
	c.Training = config.TrainingConfiguration{}
	c.Components.NodeExporterCatalogDirectory = ""
	c.Server.ApplicationProfile = &nodesProfile
	incompatible := configuration("incompatible-catalog", ds(true))
	c.Server.ApplicationProfile = &profile
	if _, e = f.lease.Exec(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID); e != nil {
		t.Fatal(e)
	}
	command := exec.CommandContext(ctx, binary, "serve", "--config", incompatible)
	output, refused := command.CombinedOutput()
	if refused == nil || strings.Contains(string(output), "CONTROL_STARTED") || !strings.Contains(string(output), "SCHEMA_") {
		t.Fatalf("incompatible catalog serving refusal: %v %s", refused, output)
	}
	if e = os.WriteFile(filepath.Join(os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"), "native-m07-incompatible-catalog.log"), output, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = f.lease.Exec(ctx, "SELECT pg_advisory_lock($1)", database.InstallationLockID); e != nil {
		t.Fatal(e)
	}

	t.Log(current + " actual delivered " + priorMilestone + " upgrade repeated with original role/data/files/ledger; three serves retain prior authorities, dedicated machine HTTPS and exact disabled-descendant refusal")
}

// Real M06 production APIs create retained state; node/GPU observations are explicitly synthetic.
// Actual approved files are hardlinked without administrative rehash; startup verifies their bytes.
func seedDeliveredM06GatewayUpgrade(t *testing.T, f actualFixture, password, root string, cumulative ...bool) func() {
	t.Helper()
	ctx := t.Context()
	a := identityfixture.Login(t, f.service, "admin@example.invalid", password, "admin")
	w := &workspaces.Service{DB: f.service.DB, Identity: f.service}
	if e := models.InitializeStore(root, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	model := uuid.NewString()
	object := filepath.Join(root, "objects", model)
	if e := os.MkdirAll(object, 0700); e != nil {
		t.Fatal(e)
	}
	source := filepath.Join(os.Getenv("PROJECT_ONE_STATE_ROOT"), "dependencies/models/qwen3-0.6b", wire.ApprovedModel().Revision, "files")
	infos := map[string]os.FileInfo{}
	for _, file := range wire.ApprovedModel().Files {
		p := filepath.Join(object, file.Path)
		if e := os.Link(filepath.Join(source, file.Path), p); e != nil {
			t.Fatal(e)
		}
		v, e := os.Stat(p)
		if e != nil {
			t.Fatal(e)
		}
		infos[p] = v
	}
	manifest, _ := json.Marshal(wire.ApprovedModel())
	if _, e := f.service.DB.Exec(ctx, "INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Actual retained public model',$4,$5,$6,'ready',$7)", model, a.Me.Workspace.TenantID, a.Me.Workspace.ID, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), manifest, a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	m := &models.Service{DB: f.service.DB, Identity: f.service, Workspaces: w, RootDirectory: root}

	n := &nodes.Service{DB: f.service.DB, Identity: f.service, Workspaces: w}
	d := &deployments.Service{Nodes: n, Models: m}
	n.Inference = d
	m.Referenced = d.HasModelReferences
	facts := wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-retained-m06", LogicalCPUs: 2, MemoryTotalBytes: 16 << 30, DiskPath: "/", DiskTotalBytes: 100 << 30, DiskAvailableBytes: 50 << 30, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}
	join, e := n.MintForProfile(ctx, a, a.Me.Workspace.ID, "Retained M06 inference node", wire.DockerInferenceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	handshake := wire.Handshake{ProtocolVersion: wire.InferenceVersion, ExecutionProfile: wire.DockerInferenceProfile, Capabilities: wire.Capabilities(wire.DockerInferenceProfile), Facts: facts}
	node, e := n.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: handshake}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	gpu := "GPU-12345678-1234-1234-1234-123456789abc"
	handshake.Inference = &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{{UUID: gpu, Name: "Synthetic retained GPU", DriverVersion: "580.1", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8, MIGMode: "Disabled", ObservedAt: time.Now().UTC()}}, ObservedAt: time.Now().UTC()}
	if _, e = n.Heartbeat(ctx, node.NodeID, node.Credential, handshake); e != nil {
		t.Fatal(e)
	}
	deployment, e := d.Create(ctx, a, deployments.CreateRequest{NodeID: node.NodeID, ModelID: model, GPUUUID: gpu, Reason: "Synthetic retained M06 deployment"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	claimed, e := n.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
	if e != nil || claimed.Lease == nil {
		t.Fatal(e)
	}
	observed := &wire.DeploymentObservation{DeploymentID: deployment.ID, ModelID: model, CatalogDigest: wire.ModelCatalogDigest(), GPUUUID: gpu, Revision: deployment.Revision, State: "running", Managed: true, Ready: true, ContainerID: strings.Repeat("a", 64), ImageID: wire.ApprovedInferenceRecipe().ImageConfigDigest, Verification: &wire.GenerationVerification{Model: wire.InferenceModelName, PromptSHA256: wire.InferencePromptDigest(), ResponseSHA256: strings.Repeat("b", 64), InputTokens: 10, OutputTokens: 1, ObservedAt: time.Now().UTC()}}
	if _, e = n.Complete(ctx, node.NodeID, node.Credential, claimed.Lease.TaskID, wire.Result{LeaseToken: claimed.Lease.LeaseToken, Outcome: "success", Inference: &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{}, Deployment: observed, ObservedAt: time.Now().UTC()}}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	// Preserve actual M04/M05 domain state and log DTOs alongside the live deployment.
	component := &components.Service{Nodes: n}
	n.Components = component
	management, e := n.MintForProfile(ctx, a, a.Me.Workspace.ID, "Retained M05 component node", wire.SystemdMaintenanceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	mh := wire.Handshake{ProtocolVersion: wire.MaintenanceVersion, ExecutionProfile: wire.SystemdMaintenanceProfile, Capabilities: wire.Capabilities(wire.SystemdMaintenanceProfile), Facts: facts}
	managed, e := n.Enroll(ctx, wire.Enrollment{JoinToken: management.Token, Handshake: mh}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = component.Request(ctx, a, managed.NodeID, "logs", uuid.NewString(), "Synthetic retained component logs", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	lease, e := n.Claim(ctx, managed.NodeID, managed.Credential, uuid.NewString())
	if e != nil || lease.Lease == nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	configuration := wire.DefaultComponentConfig()
	co := &wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "running", Managed: true, Config: &configuration, ObservedAt: now, Metrics: &wire.ComponentMetrics{CPUSecondsTotal: 1, CPUIdleSecondsTotal: 1, MemoryTotalBytes: 1024, MemoryAvailableBytes: 512, FilesystemTotalBytes: 2048, FilesystemAvailableBytes: 1024}}
	logs := &wire.ComponentLogs{Source: "systemd_journal", Available: true, ObservedAt: now, Since: now.Add(-time.Minute), Entries: []wire.ComponentLogEntry{}}
	if _, e = n.Complete(ctx, managed.NodeID, managed.Credential, lease.Lease.TaskID, wire.Result{LeaseToken: lease.Lease.LeaseToken, Outcome: "success", Component: co, Logs: logs}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	archive, e := os.Open(filepath.Join(filepath.Dir(source), "qwen3-0.6b.tar"))
	if e != nil {
		t.Fatal(e)
	}
	defer archive.Close()
	info, e := archive.Stat()
	if e != nil {
		t.Fatal(e)
	}
	// A separate Workspace retains an actual resumable import/chunk without duplicate-ready publication.
	other, e := w.Create(ctx, a, "Retained partial import Workspace", a.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	partial, e := m.StartImport(ctx, a, models.ImportRequest{WorkspaceID: other.ID, Name: "Retained actual partial archive", CatalogDigest: wire.ModelCatalogDigest(), ExpectedBytes: info.Size(), LicenseAcknowledged: true, Reason: "Synthetic M06 upgrade preservation"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	chunk := make([]byte, wire.ModelTransferChunkBytes)
	if _, e = io.ReadFull(archive, chunk); e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256(chunk)
	if _, e = m.UploadChunk(ctx, a, partial.ID, 0, int64(len(chunk)), fmt.Sprintf("%x", hash), bytes.NewReader(chunk)); e != nil {
		t.Fatal(e)
	}

	if len(cumulative) >= 1 && cumulative[0] {
		ds := &distribution.Service{DB: f.service.DB, Identity: f.service, Workspaces: w, Nodes: n, Deployments: d}
		gateway, e := ds.Provision(ctx, a, distribution.ProvisionRequest{WorkspaceID: a.Me.Workspace.ID, NodeID: node.NodeID, Name: "Preserved M07 gateway", ClientOrigin: "https://gateway.example.invalid", Reason: "Synthetic populated upgrade"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		alias, e := ds.Publish(ctx, a, distribution.PublishRequest{WorkspaceID: a.Me.Workspace.ID, GatewayID: gateway.Gateway.ID, DeploymentID: deployment.ID, DeploymentRevision: deployment.Revision, Name: "preserved-model", Reason: "Synthetic populated upgrade"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		key, e := ds.MintKey(ctx, a, distribution.KeyRequest{WorkspaceID: a.Me.Workspace.ID, AliasID: alias.ID, Label: "Preserved key", TTLSeconds: 3600, Reason: "Synthetic upgrade"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		known, unknown := uuid.NewString(), uuid.NewString()
		for _, id := range []string{known, unknown} {
			if _, e = ds.Admit(ctx, gateway.Gateway.ID, *gateway.Secret, gatewaywire.AdmissionRequest{RequestID: id, APIKey: *key.Secret, Model: alias.Name, MaximumOutputTokens: 16}, uuid.NewString()); e != nil {
				t.Fatal(e)
			}
		}
		if _, e = ds.Report(ctx, gateway.Gateway.ID, *gateway.Secret, gatewaywire.Outcome{RequestID: known, Status: "succeeded", Usage: &gatewaywire.Usage{PromptTokens: 7, CompletionTokens: 2, TotalTokens: 9}, ElapsedMilliseconds: 1, OutputStarted: true}, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		if _, e = ds.RevokeGateway(ctx, a, a.Me.Workspace.ID, gateway.Gateway.ID, "Synthetic retained unknown", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		if _, e = ds.CreateRecoveryChallenge(ctx, a, a.Me.Workspace.ID, unknown, "Synthetic retained challenge", uuid.NewString(), uuid.NewString()); e != nil {
			t.Fatal(e)
		}
	}
	tables := []string{"schema_migrations", "tenants", "workspaces", "users", "password_credentials", "workspace_memberships", "installation_state", "sessions", "invitations", "password_reset_tokens", "mail_deliveries", "audit_events", "rate_limit_buckets", "idempotency_records", "platform_license_state", "platform_license_artifacts", "workspace_notes", "node_join_tokens", "managed_nodes", "node_tasks", "managed_components", "component_log_snapshots", "model_versions", "model_imports", "model_import_chunks", "inference_nodes", "deployments", "gpu_leases"}
	if len(cumulative) >= 1 && cumulative[0] {
		tables = append(tables, "gateway_instances", "model_aliases", "model_alias_history", "api_keys", "gateway_requests", "gateway_recoveries", "model_alias_target_versions")
	}
	if len(cumulative) == 2 && cumulative[1] {
		for path, info := range seedPublishedUpgradeDataset(t, f, password) {
			infos[path] = info
		}
		tables = append(tables, "datasets", "dataset_drafts", "dataset_rows", "dataset_row_history", "dataset_jobs", "dataset_job_attempts", "dataset_versions")
	}
	columns := map[string][]string{}
	for _, table := range tables {
		rows, e := f.service.DB.Query(ctx, "SELECT column_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 ORDER BY ordinal_position", table)
		if e != nil {
			t.Fatal(e)
		}
		for rows.Next() {
			var name string
			if e = rows.Scan(&name); e != nil {
				t.Fatal(e)
			}
			columns[table] = append(columns[table], pgx.Identifier{name}.Sanitize())
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			t.Fatal(e)
		}
	}
	snapshot := func() map[string]string {
		out := map[string]string{}
		for _, table := range tables {
			where := ""
			if table == "schema_migrations" {
				where = " WHERE version<=11"
				if len(cumulative) >= 1 && cumulative[0] {
					where = " WHERE version<=12"
				}
				if len(cumulative) == 2 && cumulative[1] {
					where = " WHERE version<=14"
				}
			}
			var raw string
			if e := f.service.DB.QueryRow(context.Background(), "SELECT COALESCE(string_agg(v::text,E'\\n' ORDER BY v::text),'') FROM (SELECT to_jsonb(t) v FROM (SELECT "+strings.Join(columns[table], ",")+" FROM "+table+where+") t) rows").Scan(&raw); e != nil {
				t.Fatal(e)
			}
			out[table] = fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))
		}
		return out
	}
	before := snapshot()
	var oid uint32
	var verifier string
	if e := f.lease.QueryRow(ctx, "SELECT oid,rolpassword FROM pg_authid WHERE rolname=$1", f.role).Scan(&oid, &verifier); e != nil {
		t.Fatal(e)
	}
	return func() {
		after := snapshot()
		for table, value := range before {
			if after[table] != value {
				t.Fatal("M06 installed row changed", table)
			}
		}
		var got uint32
		var secret string
		if e := f.lease.QueryRow(ctx, "SELECT oid,rolpassword FROM pg_authid WHERE rolname=$1", f.role).Scan(&got, &secret); e != nil || got != oid || secret != verifier {
			t.Fatal("original principal changed", e)
		}
		for path, original := range infos {
			current, e := os.Stat(path)
			if e != nil || !os.SameFile(original, current) || original.Size() != current.Size() || !original.ModTime().Equal(current.ModTime()) {
				t.Fatal("immutable model bytes/inode changed", e)
			}
		}
	}
}
