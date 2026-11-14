//go:build training_operator_integration && training_artifact_integration

package training_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type nativeTrainingHarness struct {
	Run                           func(string, ...string) error
	RunWithin                     func(context.Context, string, ...string) error
	SetTrainingRoot               func(string)
	SetPair                       func(string, string, string)
	Configuration, Origin, CAFile string
}

func nativeTrainingOperator(t *testing.T, f scenario) (func(string, ...string) error, func(string)) {
	h := newNativeTrainingOperator(t, f)
	return h.Run, h.SetTrainingRoot
}

func newNativeTrainingOperator(t *testing.T, f scenario) nativeTrainingHarness {
	t.Helper()
	binary := os.Getenv("PROJECT_ONE_CONTROL_BINARY")
	if binary == "" || !filepath.IsAbs(binary) {
		t.Fatal("actual owned native control executable required")
	}
	dir := snapshotTestDirectory(t)
	write := func(name string, raw []byte) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if e := os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
		return path
	}
	rawDSN := os.Getenv("PROJECT_ONE_TEST_DSN")
	if rawDSN == "" {
		raw, e := config.Secret(os.Getenv("PROJECT_ONE_TEST_DSN_FILE"))
		if e != nil {
			t.Fatal("private operator test DSN required")
		}
		rawDSN = strings.TrimSpace(string(raw))
	}
	dsn, e := url.Parse(rawDSN)
	if e != nil || (dsn.Scheme != "postgres" && dsn.Scheme != "postgresql") || dsn.Hostname() != "127.0.0.1" || !strings.HasSuffix(f.s.DB.Config().ConnConfig.Database, "_test") {
		t.Fatal("isolated loopback operator DSN required")
	}
	query := dsn.Query()
	query.Set("search_path", f.s.DB.Config().ConnConfig.RuntimeParams["search_path"])
	dsn.RawQuery = query.Encode()
	dsnFile := write("operator-dsn", []byte(dsn.String()))
	keys, e := json.Marshal(f.s.Identity.Mail.Keys)
	if e != nil {
		t.Fatal(e)
	}
	keyring := write("keyring.json", keys)
	hmac := write("auth-hmac", f.s.Identity.HMACKey)
	blocklist := write("blocklist", []byte("passwordpassword\n"))
	_, ca, cert, key := smtpsink.Certificate(t, dir)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := listener.Addr().String()
	listener.Close()
	_, port, _ := net.SplitHostPort(address)
	repo, e := filepath.Abs("../../../../../")
	if e != nil {
		t.Fatal(e)
	}
	trainingConfig := map[string]any{"artifact_root": f.s.Identity.Config.Training.ArtifactRoot, "image_reference": f.s.Identity.Config.Training.ImageReference, "image_id": f.s.Identity.Config.Training.ImageID, "worker_source_sha256": f.s.Identity.Config.Training.WorkerSourceSHA256}
	values := map[string]any{"server": map[string]any{"listen_address": address, "portal_origin": "https://127.0.0.1:" + port, "admin_origin": "https://localhost:" + port, "tls_certificate_file": cert, "tls_private_key_file": key, "portal_assets": filepath.Join(repo, "web/apps/portal/dist"), "admin_assets": filepath.Join(repo, "web/apps/console/dist")}, "database": map[string]any{"dsn_file": dsnFile, "max_open_connections": 8, "max_idle_connections": 1, "statement_timeout_seconds": 5}, "security": map[string]any{"auth_hmac_key_file": hmac, "mail_keyring_file": keyring, "password_blocklist_file": blocklist}, "smtp": map[string]any{"host": "127.0.0.1", "port": 1, "from_address": "platform@example.invalid", "tls_mode": "starttls_required", "connect_timeout_seconds": 1, "operation_timeout_seconds": 2}, "training": trainingConfig}
	{
		values["models"] = map[string]any{"artifact_root": f.s.Identity.Config.Models.ArtifactRoot}
		values["datasets"] = map[string]any{"artifact_root": f.s.Identity.Config.Datasets.ArtifactRoot, "python_executable": os.Getenv("PROJECT_ONE_DATA_PYTHON"), "processor_file": filepath.Join(repo, "runtimes/data-processing/process.py")}
		values["licensing"] = map[string]any{"trust_file": f.s.Identity.Config.Licensing.TrustFile}
	}
	{
		c := f.s.Identity.Config.ModelPreparation
		values["model_preparation"] = map[string]any{"artifact_root": c.ArtifactRoot, "image_reference": c.ImageReference, "image_id": c.ImageID, "worker_source_sha256": c.WorkerSourceSHA256}
	}
	{
		c := f.s.Identity.Config.SSO
		providers := []map[string]any{}
		for _, p := range c.Providers {
			providers = append(providers, map[string]any{"origin": p.Origin, "issuer": p.Issuer, "dial_addresses": p.DialAddresses, "ca_file": p.CAFile})
		}
		values["sso"] = map[string]any{"enabled": c.Enabled, "keyring_file": c.KeyringFile, "providers": providers}
	}
	{
		c := f.s.Identity.Config.Knowledge
		http := f.s.Identity.Config.Policy().HTTP
		values["runtime"] = map[string]any{"http": map[string]any{"request_seconds": http.RequestSeconds, "startup_seconds": http.StartupSeconds, "shutdown_seconds": http.ShutdownSeconds}}
		values["knowledge"] = map[string]any{"enabled": c.Enabled, "python_executable": c.PythonExecutable, "worker_file": c.WorkerFile, "model_directory": c.ModelDirectory, "scratch_root": c.ScratchRoot, "gateways": []any{}}
	}
	{
		values["workflows"] = map[string]any{"enabled": f.s.Identity.Config.Workflows.Enabled}
	}
	{
		c := f.s.Identity.Config.Plugins
		values["plugins"] = map[string]any{"enabled": c.Enabled, "trusted_keys": c.TrustedKeys}
	}
	{
		c := f.s.Identity.Config.Localization
		values["localization"] = map[string]any{"package_directory": c.PackageDirectory, "required_locales": c.RequiredLocales}
	}
	configuration := ""
	save := func(root string) {
		trainingConfig["artifact_root"] = root
		raw, e := json.Marshal(values)
		if e != nil {
			t.Fatal(e)
		}
		configuration = write("control.json", raw)
	}
	save(f.s.Identity.Config.Training.ArtifactRoot)
	commandWithin := func(ctx context.Context, name string, args ...string) error {
		if _, bounded := ctx.Deadline(); !bounded {
			return fmt.Errorf("BOUNDED_NATIVE_OPERATOR_CONTEXT_REQUIRED")
		}
		process := exec.CommandContext(ctx, binary, append([]string{name, "--config", configuration, "--reason", "Actual isolated native training operator verification"}, args...)...)
		raw, e := process.CombinedOutput()
		if strings.Contains(string(raw), dsn.String()) || strings.Contains(string(raw), string(f.s.Identity.HMACKey)) {
			t.Fatal("operator diagnostic exposed a protected secret")
		}
		if e != nil {
			t.Log("native operator stable error", strings.TrimSpace(string(raw)))
		}
		if e != nil && ctx.Err() != nil {
			return fmt.Errorf("native %s parent context: %w", name, ctx.Err())
		}
		return e
	}
	command := func(name string, args ...string) error {
		ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		return commandWithin(ctx, name, args...)
	}
	setPair := func(databaseURL, modelRoot, datasetRoot string) {
		if databaseURL != "" {
			write("operator-dsn", []byte(databaseURL))
		}
		values["models"] = map[string]any{"artifact_root": modelRoot}
		values["datasets"] = map[string]any{"artifact_root": datasetRoot, "python_executable": os.Getenv("PROJECT_ONE_DATA_PYTHON"), "processor_file": filepath.Join(repo, "runtimes/data-processing/process.py")}
		save(trainingConfig["artifact_root"].(string))
	}
	return nativeTrainingHarness{Run: command, RunWithin: commandWithin, SetTrainingRoot: save, SetPair: setPair, Configuration: configuration, Origin: "https://127.0.0.1:" + port, CAFile: ca}
}
func TestActualNativeTrainingOperatorInitializeVerifyLockAndAudit(t *testing.T) {
	f := setup(t)
	root := f.s.Identity.Config.Training.ArtifactRoot
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	command, _ := nativeTrainingOperator(t, f)
	if command("initialize-training-store", "--training-store-root", root, "--owner-uid", fmt.Sprint(os.Geteuid()), "--owner-gid", fmt.Sprint(os.Getegid())) == nil {
		t.Fatal("missing explicit empty-store confirmation accepted")
	}
	entries, e := os.ReadDir(root)
	if e != nil || len(entries) != 0 {
		t.Fatal("unconfirmed operator changed selected directory", e)
	}
	if e = command("initialize-training-store", "--training-store-root", root, "--confirm-empty-training-store", "--owner-uid", fmt.Sprint(os.Geteuid()), "--owner-gid", fmt.Sprint(os.Getegid())); e != nil {
		t.Fatal("actual native initialize", e)
	}
	if e = command("verify-training-files"); e != nil {
		t.Fatal("actual native verify", e)
	}
	store, e := training.OpenStore(root, f.admin.Me.Workspace.TenantID, 4096, wire.MaximumCheckpointBytes, wire.MaximumCheckpointFiles)
	if e != nil {
		t.Fatal(e)
	}
	if command("verify-training-files") == nil {
		t.Fatal("native operator bypassed live private store lock")
	}
	store.Close()
	if count(t, f, "SELECT count(*) FROM audit_events WHERE audience='operator' AND action IN ('training.store_initialize_verified','training.store_verify_verified')") != 2 {
		t.Fatal("actual native actions/audit mismatch")
	}
}
func TestActualNativeTrainingOperatorAcceptedFilesExportImportAndAuditFailureRetainData(t *testing.T) {
	f, _, _ := acceptedSnapshotCheckpoint(t)
	source := f.s.Identity.Config.Training.ArtifactRoot
	command, save := nativeTrainingOperator(t, f)
	if command("verify-training-files") == nil {
		t.Fatal("operator adopted a held source")
	}
	if e := f.s.Store.Close(); e != nil {
		t.Fatal(e)
	}
	f.s.Store = nil
	if e := command("verify-training-files"); e != nil {
		t.Fatal(e)
	}
	backup := filepath.Join(snapshotTestDirectory(t), "native-snapshot")
	if e := command("export-training-files", "--backup-root", backup); e != nil {
		t.Fatal("actual native export", e)
	}
	destination := snapshotTestDirectory(t)
	save(destination)
	if command("import-training-files", "--backup-root", backup, "--training-store-root", destination) == nil {
		t.Fatal("missing import confirmation accepted")
	}
	entries, e := os.ReadDir(destination)
	if e != nil || len(entries) != 0 {
		t.Fatal("unconfirmed import mutated destination", e)
	}
	if e = command("import-training-files", "--backup-root", backup, "--training-store-root", destination, "--confirm-empty-training-store"); e != nil {
		t.Fatal("actual native import", e)
	}
	if e = command("verify-training-files"); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(filepath.Join(destination, "namespace.json"))
	if e != nil {
		t.Fatal(e)
	}
	original, e := os.ReadFile(filepath.Join(source, "namespace.json"))
	if e != nil || string(before) != string(original) {
		t.Fatal("native restore changed accepted namespace", e)
	}
	if _, e = f.s.DB.Exec(context.Background(), `CREATE FUNCTION reject_native_export() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='training.store_export_verified' THEN RAISE EXCEPTION 'SYNTHETIC_NATIVE_EXPORT_AUDIT_FAILURE'; END IF; RETURN NEW; END$$;CREATE TRIGGER reject_native_export BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_native_export()`); e != nil {
		t.Fatal(e)
	}
	failed := filepath.Join(snapshotTestDirectory(t), "failed-audit-snapshot")
	if command("export-training-files", "--backup-root", failed) == nil {
		t.Fatal("failed native audit reported success")
	}
	if _, e = os.Stat(filepath.Join(failed, "snapshot.json")); e != nil {
		t.Fatal("failed audit discarded completed exact files", e)
	}
	if count(t, f, "SELECT count(*) FROM audit_events WHERE audience='operator' AND action IN ('training.store_export_verified','training.store_import_verified')") != 2 {
		t.Fatal("native snapshot audit was lost or falsely appended")
	}
}

func TestActualNativeTrainingOperatorRemovedNodeChallengeAndReconciliation(t *testing.T) {
	f := setup(t)
	j := dispatched(t, f)
	lease := claimed(t, f)
	h := newNativeTrainingOperator(t, f)
	dir := snapshotTestDirectory(t)
	challengePath := filepath.Join(dir, "challenge.json")
	if e := os.WriteFile(challengePath, []byte("PRESERVE_EXISTING_OPERATOR_FILE"), 0600); e != nil {
		t.Fatal(e)
	}
	if h.Run("prepare-training-removal", "--node-id", f.input.NodeID, "--training-job-id", j.ID, "--challenge-file", challengePath) == nil {
		t.Fatal("operator overwrote challenge")
	}
	if count(t, f, "SELECT count(*) FROM managed_nodes WHERE status='active' AND credential_hash IS NOT NULL") != 1 {
		t.Fatal("existing output refusal fenced node")
	}
	challengePath = filepath.Join(dir, "new-challenge.json")
	if e := h.Run("prepare-training-removal", "--node-id", f.input.NodeID, "--training-job-id", j.ID, "--challenge-file", challengePath); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(challengePath)
	if e != nil {
		t.Fatal(e)
	}
	c, e := wire.DecodeRemovalChallenge(raw, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	// This is an explicit synthetic physical report testing the actual native operator, not node execution.
	r := wire.RemovalReceipt{SchemaVersion: 1, ReceiptID: uuid.NewString(), Challenge: c, ChallengeSHA256: c.Digest(), Stop: stopFixture(t, f, lease), ObservedAt: time.Now().UTC()}
	raw, e = json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	receipt := filepath.Join(dir, "receipt.json")
	if e = os.WriteFile(receipt, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if e = h.Run("reconcile-training-removal", "--receipt-file", receipt); e != nil {
		t.Fatal(e)
	}
	if e = h.Run("reconcile-training-removal", "--receipt-file", receipt); e != nil {
		t.Fatal("actual native exact replay", e)
	}
	if count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='released'") != 1 || count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.removal_reconciled'") != 1 {
		t.Fatal("native reconciliation cardinality")
	}
}
