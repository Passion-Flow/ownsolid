package postgresbackup

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func policyFixture() release.PostgreSQLBackupPolicy {
	return release.PostgreSQLBackupPolicy{DumpFile: "/tools/pg_dump", DumpSHA256: strings.Repeat("a", 64), RestoreFile: "/tools/pg_restore", RestoreSHA256: strings.Repeat("b", 64), ControlFile: "/tools/control", ControlSHA256: strings.Repeat("c", 64), ExpectedVersion: "18.6", ScratchRoot: "/private/scratch", OperationSeconds: 30, TerminationGraceSeconds: 1, MaximumStoreBytes: 1 << 30, MaximumStoreFiles: 100, MaximumSQLBytes: 1 << 20, MaximumConfigurationBytes: 1 << 20, MaximumManifestBytes: 1 << 20, MinimumFreeBytes: 1 << 20}
}
func privateTestRoot(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	if os.Chmod(p, 0700) != nil {
		t.Fatal("private fixture")
	}
	resolved, e := filepath.EvalSymlinks(p)
	if e != nil {
		t.Fatal(e)
	}
	return resolved
}

func TestPostgresBackupPolicyEnvironmentIsExplicitAndStrict(t *testing.T) {
	p := policyFixture()
	if e := applyEnvironment(&p, []byte("# Explicit typed nondefault\nPROJECT_ONE_POSTGRES_BACKUP_OPERATION_SECONDS=41\nPROJECT_ONE_POSTGRES_BACKUP_MINIMUM_FREE_BYTES=34359738368\n")); e != nil || p.OperationSeconds != 41 || p.MinimumFreeBytes != 32<<30 {
		t.Fatal("typed overrides", e)
	}
	for _, raw := range []string{"PROJECT_ONE_POSTGRES_BACKUP_OPERATION_SECONDS=false\n", "PROJECT_ONE_POSTGRES_BACKUP_OPERATION_SECONDS=\n", "PROJECT_ONE_POSTGRES_BACKUP_UNKNOWN=1\n", "PROJECT_ONE_POSTGRES_BACKUP_OPERATION_SECONDS=41\nPROJECT_ONE_POSTGRES_BACKUP_OPERATION_SECONDS=42\n", "PROJECT_ONE_POSTGRES_BACKUP_OPERATION_SECONDS=041\n", "PROJECT_ONE_POSTGRES_BACKUP_MINIMUM_FREE_BYTES=-1\n", "export PROJECT_ONE_POSTGRES_BACKUP_OPERATION_SECONDS=41\n"} {
		t.Run(strings.Split(raw, "=")[0], func(t *testing.T) {
			v := policyFixture()
			if applyEnvironment(&v, []byte(raw)) == nil {
				t.Fatal("invalid environment accepted")
			}
		})
	}
}

func TestPostgresBackupPrivateTreeCopyLimitsAndEscapes(t *testing.T) {
	root := privateTestRoot(t)
	source := filepath.Join(root, "source")
	if os.Mkdir(source, 0700) != nil {
		t.Fatal("source")
	}
	if os.Mkdir(filepath.Join(source, "nested"), 0700) != nil {
		t.Fatal("nested")
	}
	if os.WriteFile(filepath.Join(source, "nested", "bytes"), []byte("synthetic immutable payload"), 0600) != nil {
		t.Fatal("payload")
	}
	ctx := context.Background()
	before, e := TreeDigest(ctx, source, 1<<20, 100, 4096)
	if e != nil {
		t.Fatal(e)
	}
	copy, e := copyTree(ctx, source, filepath.Join(root, "snapshot"), 1<<20, 100, 4096, 1<<20)
	if e != nil || copy != before {
		t.Fatal("real copy", e)
	}
	if _, e = copyTree(ctx, source, filepath.Join(root, "snapshot"), 1<<20, 100, 4096, 1<<20); e == nil {
		t.Fatal("destination overwritten")
	}
	if _, e = TreeDigest(ctx, source, 1, 100, 4096); e == nil {
		t.Fatal("byte limit ignored")
	}
	if os.Link(filepath.Join(source, "nested", "bytes"), filepath.Join(root, "alias")) != nil {
		t.Fatal("hardlink")
	}
	if _, e = TreeDigest(ctx, source, 1<<20, 100, 4096); e == nil {
		t.Fatal("hardlink accepted")
	}
	if os.Remove(filepath.Join(root, "alias")) != nil {
		t.Fatal("alias cleanup")
	}
	if os.Symlink("/etc/passwd", filepath.Join(source, "escape")) != nil {
		t.Fatal("symlink")
	}
	if _, e = TreeDigest(ctx, source, 1<<20, 100, 4096); e == nil {
		t.Fatal("symlink accepted")
	}
	if os.Remove(filepath.Join(source, "escape")) != nil {
		t.Fatal("escape cleanup")
	}
	if os.Chmod(filepath.Join(source, "nested"), 0755) != nil {
		t.Fatal("mode")
	}
	if _, e = TreeDigest(ctx, source, 1<<20, 100, 4096); e == nil {
		t.Fatal("nonprivate directory accepted")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, e = hashStream(ctx, nil, strings.NewReader("payload"), 100); e == nil {
		t.Fatal("cancelled read succeeded")
	}
}

func TestPostgresBackupNativeProcessHelper(t *testing.T) {
	if os.Getenv("PROJECT_ONE_POSTGRES_BACKUP_TEST_HELPER") != "1" {
		return
	}
	signal.Ignore(syscall.SIGTERM)
	for {
		time.Sleep(time.Second)
	}
}

func TestPostgresBackupNativeCancellationJoinsAndRedacts(t *testing.T) {
	p := policyFixture()
	p.ScratchRoot = privateTestRoot(t)
	file, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	e = runNative(ctx, p, file, []string{"-test.run=^TestPostgresBackupNativeProcessHelper$"}, []string{"PROJECT_ONE_POSTGRES_BACKUP_TEST_HELPER=1"}, nil, io.Discard)
	if e == nil || e.Error() != "POSTGRES_BACKUP_INTERRUPTED" || time.Since(started) > 5*time.Second {
		t.Fatal("bounded real process cancellation", e)
	}
}

func requestFixture() release.PostgreSQLBackupRequest {
	return release.PostgreSQLBackupRequest{FormatVersion: 1, BackupID: strings.Repeat("a", 32), SourceScopeID: strings.Repeat("b", 32), MaintenanceSHA256: strings.Repeat("a", 64), SourceManifestSHA256: strings.Repeat("b", 64), ControlImageSHA256: strings.Repeat("c", 64), PostgresImageSHA256: strings.Repeat("d", 64), TenantID: "11111111-1111-4111-8111-111111111111", Database: "synthetic_database", Operator: "synthetic_operator", RuntimeConfigurationFile: "/private/config/runtime.yaml", ConfigurationRoot: "/private/config", BackupRoot: "/private/backup", Reason: "Synthetic request", Policy: policyFixture()}
}

func TestPostgresBackupConnectionRefusesAmbientAndImplicitTransportBeforeSQL(t *testing.T) {
	root := privateTestRoot(t)
	path := filepath.Join(root, "dsn")
	r := requestFixture()
	valid := "postgresql://synthetic_operator:synthetic_secret@127.0.0.1:5432/synthetic_database?sslmode=disable&search_path=public"
	for name, dsn := range map[string]string{"plain": strings.Replace(valid, "?sslmode=disable&search_path=public", "", 1), "prefer": strings.Replace(valid, "disable", "prefer", 1), "other-database": strings.Replace(valid, "/synthetic_database?", "/other?", 1), "duplicate-mode": valid + "&sslmode=disable", "service": valid + "&service=ambient", "passfile": valid + "&passfile=/private/unknown", "fallback-host": strings.Replace(valid, "127.0.0.1", "127.0.0.1,127.0.0.2", 1), "implicit-client-TLS": strings.Replace(valid, "disable", "verify-full", 1)} {
		t.Run(name, func(t *testing.T) {
			if os.WriteFile(path, []byte(dsn), 0600) != nil {
				t.Fatal("dsn")
			}
			if AdmitConnection(path, r) == nil {
				t.Fatal("unsupported connection admitted")
			}
		})
	}
	if os.WriteFile(path, []byte(valid), 0600) != nil {
		t.Fatal("dsn")
	}
	if e := AdmitConnection(path, r); e != nil {
		t.Fatal("explicit connection refused", e)
	}
	t.Setenv("PGSERVICE", "must-not-be-read")
	if e := AdmitConnection(path, r); e == nil || e.Error() != "POSTGRES_BACKUP_AMBIENT_DATABASE_CONFIGURATION_REFUSED" {
		t.Fatal("ambient input accepted", e)
	}
}

func TestPostgresBackupPrivateConnectionFilesEscapeAndCleanupAfterFailure(t *testing.T) {
	root := privateTestRoot(t)
	r := requestFixture()
	r.Policy.ScratchRoot = root
	c := connection{host: "127.0.0.1", port: "5432", database: r.Database, user: r.Operator, password: "synthetic:password\\value", sslmode: "disable", connectSeconds: 3}
	called := false
	e := c.withFiles(context.Background(), r, func(environment []string) error {
		called = true
		path := filepath.Join(root, "connection-"+r.BackupID)
		pass, e := os.ReadFile(filepath.Join(path, "pgpass"))
		if e != nil || !strings.Contains(string(pass), "synthetic\\:password\\\\value") {
			t.Fatal("password file escaping")
		}
		service, e := os.ReadFile(filepath.Join(path, "pg_service.conf"))
		if e != nil || !strings.Contains(string(service), "host=127.0.0.1\n") || strings.Contains(string(service), "host='") || strings.Contains(string(service), c.password) {
			t.Fatal("literal service projection")
		}
		if len(environment) != 3 {
			t.Fatal("explicit native environment")
		}
		return errors.New("SYNTHETIC_NATIVE_FAILURE")
	})
	if !called || e == nil || !strings.Contains(e.Error(), "SYNTHETIC_NATIVE_FAILURE") {
		t.Fatal("original process failure lost", e)
	}
	if _, e = os.Lstat(filepath.Join(root, "connection-"+r.BackupID)); !os.IsNotExist(e) {
		t.Fatal("exact private connection scope retained after joined failure")
	}
}

// The non-serving fixture preserves the complete default domain policies.
// Its disposable filesystem must satisfy their real free-space floors; no
// training/preparation worker is constructed or capacity asserted.
func emptyBackupFixtureConfiguration(template []byte, root string) (config.Config, error) {
	c, e := config.Decode(template)
	if e != nil {
		return c, e
	}
	source, configuration := filepath.Join(root, "source"), filepath.Join(root, "configuration")
	c.Database.DSNFile = filepath.Join(root, "source-dsn")
	c.Database.MaxOpenConnections = 3
	c.Database.MaxIdleConnections = 0
	c.Models.ArtifactRoot = filepath.Join(source, "models")
	c.Datasets.ArtifactRoot = filepath.Join(source, "datasets")
	c.Training.ArtifactRoot = filepath.Join(source, "training")
	c.ModelPreparation.ArtifactRoot = filepath.Join(source, "preparations")
	// Retain the template's valid workflow executor dependency. This operator
	// never constructs the knowledge worker or executes a workflow.
	c.Knowledge.Enabled = true
	c.Knowledge.Gateways = nil
	c.SSO.Enabled = false
	c.SSO.Providers = nil
	c.SSO.KeyringFile = ""
	c.Localization.PackageDirectory = ""
	c.Localization.RequiredLocales = nil
	c.Security.AuthHMACKeyFile = filepath.Join(configuration, "auth-hmac")
	c.Security.MailKeyringFile = filepath.Join(configuration, "mail-keyring.json")
	c.Security.PasswordBlocklistFile = filepath.Join(configuration, "password-blocklist.txt")
	c.Server.TLSCertificateFile = filepath.Join(configuration, "server.pem")
	c.Server.TLSPrivateKeyFile = filepath.Join(configuration, "server.key")
	c.SMTP.CAFile = filepath.Join(configuration, "smtp-ca.pem")
	c.ReleaseViewConfigFile = filepath.Join(configuration, "release-view.json")
	c.Licensing = nil
	if e = c.Validate(); e != nil {
		return config.Config{}, e
	}
	return c, nil
}

func TestPostgresBackupEmptyFixtureUsesValidTypedPolicy(t *testing.T) {
	raw, e := os.ReadFile("../../../config/examples/control/releases-cumulative.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	c, e := emptyBackupFixtureConfiguration(raw, "/fixture")
	if e != nil {
		t.Fatal("complete non-serving fixture typed configuration", e)
	}
	if !c.Knowledge.Enabled || !c.Workflows.Enabled || c.Runtime.Training.MinimumFreeBytes < c.Runtime.Training.Resources.MaximumOutputBytes || c.Runtime.ModelLifecycle.MinimumFreeBytes < c.Runtime.ModelLifecycle.Resources.MaximumOutputBytes {
		t.Fatal("fixture dependency/capacity contradiction")
	}
}

func TestPostgresBackupProtectedRuntimeProducerAndClosureBeforeSQL(t *testing.T) {
	root := privateTestRoot(t)
	for _, name := range []string{"configuration", "source", "scratch", "backups"} {
		if os.Mkdir(filepath.Join(root, name), 0700) != nil {
			t.Fatal("private input parent")
		}
	}
	template, e := os.ReadFile("../../../config/examples/control/releases-cumulative.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	c, e := emptyBackupFixtureConfiguration(template, root)
	if e != nil {
		t.Fatal(e)
	}
	write := func(path string, value []byte) {
		t.Helper()
		if os.WriteFile(path, value, 0600) != nil {
			t.Fatal("private fixture input")
		}
	}
	operatorCredential, e := databaseaccess.NewCredential()
	if e != nil {
		t.Fatal(e)
	}
	runtimeCredential, e := databaseaccess.NewCredential()
	if e != nil {
		t.Fatal(e)
	}
	u := url.URL{Scheme: "postgresql", Host: "127.0.0.1:65431", Path: "/synthetic_database", User: url.UserPassword("synthetic_operator", operatorCredential), RawQuery: "sslmode=disable&search_path=public"}
	write(c.Database.DSNFile, []byte(u.String()))
	key := make([]byte, 32)
	if _, e = rand.Read(key); e != nil {
		t.Fatal(e)
	}
	write(c.Security.AuthHMACKeyFile, key)
	keys, _ := json.Marshal(map[string]any{"active": "synthetic", "keys": map[string]string{"synthetic": base64.StdEncoding.EncodeToString(key)}})
	write(c.Security.MailKeyringFile, keys)
	write(c.Security.PasswordBlocklistFile, []byte("synthetic disallowed credential\n"))
	for _, path := range []string{c.Server.TLSCertificateFile, c.Server.TLSPrivateKeyFile, c.SMTP.CAFile, c.ReleaseViewConfigFile} {
		write(path, []byte("Synthetic non-serving referenced input\n"))
	}
	operatorPath := filepath.Join(root, "operator.yaml")
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	write(operatorPath, raw)
	runtime := c
	runtime.Database.DSNFile = filepath.Join(root, "runtime-dsn")
	u.User = url.UserPassword("synthetic_runtime", runtimeCredential)
	write(runtime.Database.DSNFile, []byte(u.String()))
	runtimePath := filepath.Join(root, "configuration", "runtime.yaml")
	raw, e = yaml.Marshal(runtime)
	if e != nil {
		t.Fatal(e)
	}
	write(runtimePath, raw)
	c, e = config.LoadProtected(operatorPath)
	if e != nil {
		t.Fatal(e)
	}
	runtime, e = config.LoadProtected(runtimePath)
	if e != nil {
		t.Fatal(e)
	}
	// This existing producer/reader path does not open a SQL connection. The
	// deliberately unserved endpoint must not be needed for input admission.
	if _, e = databaseaccess.LoadRuntime(runtimePath); e != nil {
		t.Fatal("canonical protected runtime input", e)
	}
	r := requestFixture()
	r.Operator = "synthetic_operator"
	r.RuntimeConfigurationFile = runtimePath
	r.ConfigurationRoot = filepath.Join(root, "configuration")
	r.BackupRoot = filepath.Join(root, "backups", "new")
	r.Policy.ScratchRoot = filepath.Join(root, "scratch")
	if e = AdmitConnection(c.Database.DSNFile, r); e != nil {
		t.Fatal("explicit operator connection input", e)
	}
	effective, e := configurationSnapshot(c, runtime, operatorPath, r)
	if e != nil {
		t.Fatal("current protected configuration/keyring/locale closure", e)
	}
	if strings.Contains(string(effective), operatorCredential) || strings.Contains(string(effective), runtimeCredential) {
		t.Fatal("credential in effective configuration")
	}
	wrongOperator, wrongRuntime := c, runtime
	wrongOperator.Security.PasswordBlocklistFile = "/etc/passwd"
	wrongRuntime.Security.PasswordBlocklistFile = "/etc/passwd"
	if _, e = configurationSnapshot(wrongOperator, wrongRuntime, operatorPath, r); e == nil || e.Error() != "POSTGRES_BACKUP_CONFIGURATION_CLOSURE_REQUIRED" {
		t.Fatal("arbitrary path admitted as an image asset", e)
	}
	if os.Remove(c.SMTP.CAFile) != nil {
		t.Fatal("remove one owned reference")
	}
	if _, e = configurationSnapshot(c, runtime, operatorPath, r); e == nil {
		t.Fatal("missing configured reference silently omitted from backup")
	}
	u.User = url.UserPassword("synthetic_runtime", "SyntheticRuntimeCredential123456")
	write(runtime.Database.DSNFile, []byte(u.String()))
	if _, e = databaseaccess.LoadRuntime(runtimePath); e == nil || e.Error() != "RUNTIME_CREDENTIAL_FORMAT_INVALID" {
		t.Fatal("noncanonical runtime credential accepted", e)
	}
}
