//go:build dataset_recovery_integration

package recovery_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestM08ActualNativeDatasetRestartFileSnapshotAndLogicalRestore(t *testing.T) {
	binary, python, container := os.Getenv("PROJECT_ONE_CONTROL_BINARY"), os.Getenv("PROJECT_ONE_DATA_PYTHON"), os.Getenv("PROJECT_ONE_FIXTURE_CONTAINER")
	if binary == "" || python == "" || container == "" {
		t.Fatal("explicit owned native control/Python/database fixture required")
	}
	fixture.PreflightOwnedPostgres(t)
	var e error
	account := fixture.New(t)
	owned := fixture.OwnedPostgres(t, account.DB)
	password := fixture.Bootstrap(t, account)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	a := fixture.Login(t, account, "admin@example.invalid", password, "portal")
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	write := func(name string, raw []byte) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if e := os.WriteFile(p, raw, 0600); e != nil {
			t.Fatal(e)
		}
		return p
	}
	dsn, e := url.Parse(os.Getenv("PROJECT_ONE_TEST_DSN"))
	if e != nil || dsn.Scheme != "postgres" || !strings.HasSuffix(account.DB.Config().ConnConfig.Database, "_test") {
		t.Fatal("isolated URL DSN required")
	}
	q := dsn.Query()
	q.Set("search_path", account.DB.Config().ConnConfig.RuntimeParams["search_path"])
	dsn.RawQuery = q.Encode()
	dsnFile := write("operator-dsn", []byte(dsn.String()))
	keys, _ := json.Marshal(account.Mail.Keys)
	keyring := write("keyring.json", keys)
	hmac := write("auth-hmac", account.HMACKey)
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
	sourceRoot := filepath.Join(dir, "source-datasets")
	os.Mkdir(sourceRoot, 0700)
	runtimeConfiguration := config.DatasetConfiguration{ArtifactRoot: sourceRoot, PythonExecutable: python, ProcessorFile: filepath.Join(repo, "runtimes/data-processing/process.py")}
	account.Config.Datasets = runtimeConfiguration
	values := map[string]any{"server": map[string]any{"listen_address": address, "portal_origin": origin, "admin_origin": "https://localhost:" + port, "tls_certificate_file": cert, "tls_private_key_file": key, "portal_assets": filepath.Join(repo, "web/apps/portal/dist"), "admin_assets": filepath.Join(repo, "web/apps/console/dist")}, "database": map[string]any{"dsn_file": dsnFile, "max_open_connections": 8, "max_idle_connections": 1, "statement_timeout_seconds": 5}, "security": map[string]any{"auth_hmac_key_file": hmac, "mail_keyring_file": keyring, "password_blocklist_file": blocklist}, "smtp": map[string]any{"host": "127.0.0.1", "port": 1, "from_address": "platform@example.invalid", "tls_mode": "starttls_required", "connect_timeout_seconds": 1, "operation_timeout_seconds": 2}, "datasets": map[string]any{"artifact_root": sourceRoot, "python_executable": python, "processor_file": runtimeConfiguration.ProcessorFile}}
	save := func() string { raw, _ := json.Marshal(values); return write("control.json", raw) }
	configuration := save()
	command := func(name string, args ...string) error {
		cmd := exec.CommandContext(ctx, binary, append([]string{name, "--config", configuration, "--reason", "Actual isolated dataset and PostgreSQL recovery"}, args...)...)
		raw, e := cmd.CombinedOutput()
		if e != nil {
			t.Log("operator stable failure", strings.TrimSpace(string(raw)))
		}
		return e
	}
	if e = command("initialize-dataset-store", "--dataset-store-root", sourceRoot, "--confirm-empty-dataset-store", "--owner-uid", fmt.Sprint(os.Geteuid()), "--owner-gid", fmt.Sprint(os.Getegid())); e != nil {
		t.Fatal("actual operator initialization", e)
	}
	dataService := &datasets.Service{DB: account.DB, Identity: account, Workspaces: &workspaces.Service{DB: account.DB, Identity: account}}
	if e = dataService.OpenConfiguredStore(ctx); e != nil {
		t.Fatal(e)
	}
	data, e := dataService.Create(ctx, a, datasets.CreateRequest{WorkspaceID: a.Me.Workspace.ID, Name: "Native recovery corpus", Origin: "Synthetic private local source", License: "Synthetic CC0", RightsAsserted: true, Reason: "Actual native recovery"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	raw := []byte("{\"source_id\":\"a\",\"prompt\":\"one\",\"completion\":\"first\"}\n{\"source_id\":\"b\",\"prompt\":\"two\",\"completion\":\"second\"}\n")
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	makeDraft := func() datasets.Draft {
		t.Helper()
		d, e := dataService.CreateDraft(ctx, a, a.Me.Workspace.ID, data.ID, datasets.DraftRequest{SourceName: "fixture.jsonl", SourceSHA256: digest, ExpectedBytes: int64(len(raw)), Reason: "Actual immutable source"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		d, e = dataService.Upload(ctx, a, a.Me.Workspace.ID, data.ID, d.ID, digest, int64(len(raw)), bytes.NewReader(raw), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		return d
	}
	draft := makeDraft()
	if worked, e := dataService.ProcessNext(ctx); e != nil || !worked {
		t.Fatal(e)
	}
	job, e := dataService.Publish(ctx, a, a.Me.Workspace.ID, data.ID, draft.ID, datasets.PublicationRequest{ExpectedRevision: 1, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Native retained publication"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if worked, e := dataService.ProcessNext(ctx); e != nil || !worked {
		t.Fatal(e)
	}
	dataService.Store.Close()
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
		process = exec.CommandContext(ctx, binary, "serve", "--config", configuration)
		process.Stdout = os.Stdout
		process.Stderr = os.Stderr
		if e := process.Start(); e != nil {
			t.Fatal(e)
		}
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			response, e := client.Get(origin + "/health/ready")
			if e == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					return
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("DATASET_NATIVE_READINESS_TIMEOUT")
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
			t.Fatal("native login", response.StatusCode)
		}
	}
	assertVersion := func() {
		t.Helper()
		response, e := client.Get(origin + "/api/v1/sites/default/cells/default/workspaces/" + a.Me.Workspace.ID + "/datasets/" + data.ID + "/versions/" + *job.VersionID)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 65537))
		if response.StatusCode != 200 || !bytes.Contains(raw, []byte(*job.VersionID)) {
			t.Fatal("native version lost", response.StatusCode)
		}
	}
	start()
	login()
	assertVersion()
	stop()
	start()
	assertVersion()
	backup := filepath.Join(dir, "dataset-files")
	if command("export-dataset-files", "--backup-root", backup) == nil {
		t.Fatal("live control permitted operator backup")
	}
	if _, e = os.Stat(backup); !os.IsNotExist(e) {
		t.Fatal("failed lock changed backup scope")
	}
	stop()
	if e = dataService.OpenConfiguredStore(ctx); e != nil {
		t.Fatal(e)
	}
	pending := makeDraft()
	dataService.Store.Close()
	if e = command("export-dataset-files", "--backup-root", backup); e != nil {
		t.Fatal("actual dataset export", e)
	}
	archive := owned.DumpSchema(t, ctx)
	write("backup.dump", archive)
	restored := owned.RestoreFresh(t, ctx, account.DB, archive)
	target := restored.Config().ConnConfig.Database
	dsn.Path = "/" + target
	write("operator-dsn", []byte(dsn.String()))
	restoredRoot := filepath.Join(dir, "restored-datasets")
	os.Mkdir(restoredRoot, 0700)
	values["datasets"].(map[string]any)["artifact_root"] = restoredRoot
	save()
	if e = command("invalidate-restored-auth"); e != nil {
		t.Fatal(e)
	}
	if e = command("verify-dataset-store"); e == nil {
		t.Fatal("missing restored store accepted")
	}
	if e = command("import-dataset-files", "--backup-root", backup); e != nil {
		t.Fatal("actual file import", e)
	}
	if e = command("verify-dataset-store"); e != nil {
		t.Fatal(e)
	}
	var state string
	if e = restored.QueryRow(ctx, "SELECT state FROM dataset_jobs WHERE id=$1", *pending.CurrentJobID).Scan(&state); e != nil || state != "cancelled" {
		t.Fatal("restored delegated job active", e, state)
	}
	var exports, imports int
	if e = restored.QueryRow(ctx, "SELECT count(*) FILTER(WHERE action='dataset.files_exported'),count(*) FILTER(WHERE action='dataset.files_imported') FROM audit_events WHERE audience='operator'").Scan(&exports, &imports); e != nil || exports != 1 || imports != 1 {
		t.Fatal("operator file audit lost", e)
	}
	start()
	response, e := client.Get(origin + "/api/v1/me")
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("restored session remained usable")
	}
	login()
	assertVersion()
	stop()
	var attempt string
	if e = restored.QueryRow(ctx, "SELECT attempt_id::text FROM dataset_versions WHERE id=$1", *job.VersionID).Scan(&attempt); e != nil {
		t.Fatal(e)
	}
	protected := filepath.Join(restoredRoot, "versions", *job.VersionID, attempt, "train.jsonl")
	preserved := protected + ".preserved"
	if e = os.Rename(protected, preserved); e != nil {
		t.Fatal(e)
	}
	invalid := exec.CommandContext(ctx, binary, "serve", "--config", configuration)
	out, e := invalid.CombinedOutput()
	if e == nil || !bytes.Contains(out, []byte("DATASET_FILE_INVALID")) {
		t.Fatal("native startup accepted missing file")
	}
	if e = os.Rename(preserved, protected); e != nil {
		t.Fatal(e)
	}
	start()
	login()
	assertVersion()
	stop()
	t.Log("Actual native restart, private operator initialization, live-owner lock refusal, dataset file snapshot, PostgreSQL pg_dump/pg_restore into a new isolated database, restored authority invalidation and missing-file startup recovery passed. No target hardware qualification.")
}
