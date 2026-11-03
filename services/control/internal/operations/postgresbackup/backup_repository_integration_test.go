//go:build repository_integration && postgres_backup_integration

package postgresbackup

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type actualPostgresBackupFixture struct {
	ctx                       context.Context
	pool                      *pgxpool.Pool
	lease                     *pgxpool.Conn
	operator, runtime         config.Config
	operatorPath, requestPath string
	request                   release.PostgreSQLBackupRequest
	releaseLease              func()
	close                     func()
	write                     func(string, []byte)
}

func newActualPostgresBackupFixture(t *testing.T, deadline time.Duration, operationSeconds int, maximumStoreBytes int64, maximumStoreFiles int, maximumSQLBytes int64) actualPostgresBackupFixture {
	return newActualPostgresBackupFixtureWithTools(t, deadline, operationSeconds, maximumStoreBytes, maximumStoreFiles, maximumSQLBytes, actualPostgresFixtureTools{})
}

type actualPostgresFixtureTools struct {
	clientDistribution string
	packagedImage      bool
	applicationProfile string
	configure          func(*config.Config)
}

func newActualPostgresBackupFixtureWithTools(t *testing.T, deadline time.Duration, operationSeconds int, maximumStoreBytes int64, maximumStoreFiles int, maximumSQLBytes int64, tools actualPostgresFixtureTools) actualPostgresBackupFixture {
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	t.Cleanup(cancel)
	root := os.Getenv("PROJECT_ONE_TEST_POSTGRES_BACKUP_ROOT")
	if root != "/fixture" || os.Geteuid() != 65534 {
		t.Fatal("exact isolated non-root Linux client fixture required")
	}
	private, e := openPrivateRoot(root)
	if e != nil {
		t.Fatal(e)
	}
	private.Close()
	dsnFile := filepath.Join(root, "source-dsn")
	raw, e := readPrivateBytes(dsnFile, 16<<10)
	if e != nil {
		t.Fatal(e)
	}
	pc, e := pgxpool.ParseConfig(strings.TrimSpace(string(raw)))
	endpointOK := e == nil && pc.ConnConfig.Host == "postgres" && pc.ConnConfig.Port == 5432
	if tools.applicationProfile == release.MVPPostgreSQLCPUApplicationProfile && e == nil {
		endpointOK = pc.ConnConfig.Host == "127.0.0.1" && pc.ConnConfig.Port >= 44900 && pc.ConnConfig.Port <= 44999
	}
	if !endpointOK || pc.ConnConfig.User != "projectone_operator" || !regexp.MustCompile(`^m18_backup_[a-f0-9]{32}_test$`).MatchString(pc.ConnConfig.Database) {
		t.Fatal("exact owned source connection required")
	}
	scope := strings.TrimSuffix(strings.TrimPrefix(pc.ConnConfig.Database, "m18_backup_"), "_test")
	pc.MaxConns = 3
	pc.MinConns = 0
	pc.ConnConfig.RuntimeParams["search_path"] = "public"
	write := func(path string, b []byte) {
		t.Helper()
		if os.WriteFile(path, b, 0600) != nil {
			t.Fatal("private fixture input")
		}
	}
	mkdir := func(name string) string {
		t.Helper()
		path := filepath.Join(root, name)
		if os.Mkdir(path, 0700) != nil {
			t.Fatal("exclusive fixture directory", name)
		}
		return path
	}
	mkdir("source")
	configuration := mkdir("configuration")
	scratch := mkdir("scratch")
	backups := mkdir("backups")
	template, e := readPrivateBytes(filepath.Join(root, "releases-template.yaml"), 256<<10)
	if e != nil {
		t.Fatal(e)
	}
	c, e := emptyBackupFixtureConfiguration(template, root)
	if e != nil {
		t.Fatal("typed fixture configuration", e)
	}
	if tools.applicationProfile != "" {
		if tools.applicationProfile != release.MVPPostgreSQLCPUApplicationProfile {
			t.Fatal("unknown explicit fixture lineage")
		}
		profile := tools.applicationProfile
		c.Server.ApplicationProfile = &profile
	}
	if tools.configure != nil {
		tools.configure(&c)
	}
	key := make([]byte, 32)
	if _, e = rand.Read(key); e != nil {
		t.Fatal(e)
	}
	write(c.Security.AuthHMACKeyFile, key)
	k, _ := json.Marshal(map[string]any{"active": "synthetic", "keys": map[string]string{"synthetic": base64.StdEncoding.EncodeToString(key)}})
	write(c.Security.MailKeyringFile, k)
	write(c.Security.PasswordBlocklistFile, []byte("synthetic prohibited credential\n"))
	for _, path := range []string{c.Server.TLSCertificateFile, c.Server.TLSPrivateKeyFile, c.SMTP.CAFile} {
		write(path, []byte("Synthetic non-serving backup input, no listener or TLS qualification\n"))
	}
	write(c.ReleaseViewConfigFile, []byte("{}\n"))
	if e = c.Validate(); e != nil {
		t.Fatal("typed fixture config", e)
	}
	operatorPath := filepath.Join(root, "operator.yaml")
	b, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	write(operatorPath, b)
	runtime := c
	u, e := url.Parse(strings.TrimSpace(string(raw)))
	if e != nil {
		t.Fatal(e)
	}
	runtimeUser := "m18_runtime_" + scope
	runtimeCredential, e := databaseaccess.NewCredential()
	if e != nil {
		t.Fatal("existing runtime credential producer", e)
	}
	u.User = url.UserPassword(runtimeUser, runtimeCredential)
	runtime.Database.DSNFile = filepath.Join(root, "runtime-dsn")
	write(runtime.Database.DSNFile, []byte(u.String()))
	runtimePath := filepath.Join(configuration, "runtime.yaml")
	b, e = yaml.Marshal(runtime)
	if e != nil {
		t.Fatal(e)
	}
	write(runtimePath, b)
	// Exercise the same protected typed snapshots as the native dispatcher;
	// serialized nil/empty collections retain the loader's canonical values.
	c, e = config.LoadProtected(operatorPath)
	if e != nil {
		t.Fatal("protected operator configuration", e)
	}
	runtime, e = config.LoadProtected(runtimePath)
	if e != nil {
		t.Fatal("protected runtime configuration", e)
	}
	input, e := databaseaccess.LoadRuntime(runtimePath)
	if e != nil {
		t.Fatal("real runtime input", e)
	}
	tenant, workspace, user := uuid.NewString(), uuid.NewString(), uuid.NewString()
	p := policyFixture()
	p.DumpFile = "/usr/local/bin/pg_dump"
	p.RestoreFile = "/usr/local/bin/pg_restore"
	p.ControlFile = "/operator/control"
	if tools.packagedImage {
		p.ControlFile = "/usr/local/bin/control"
	}
	p.ClientDistribution = tools.clientDistribution
	p.ScratchRoot = scratch
	p.OperationSeconds = operationSeconds
	p.MaximumStoreBytes = maximumStoreBytes
	p.MaximumStoreFiles = maximumStoreFiles
	p.MaximumSQLBytes = maximumSQLBytes
	p.MaximumManifestBytes = 1 << 20
	for _, tool := range []struct {
		path string
		sha  *string
	}{{p.DumpFile, &p.DumpSHA256}, {p.RestoreFile, &p.RestoreSHA256}, {p.ControlFile, &p.ControlSHA256}} {
		f, e := os.Open(tool.path)
		if e != nil {
			t.Fatal("actual client artifact")
		}
		sha, _, e := hashStream(ctx, nil, f, 128<<20)
		f.Close()
		if e != nil {
			t.Fatal(e)
		}
		*tool.sha = sha
	}
	r := release.PostgreSQLBackupRequest{FormatVersion: 1, BackupID: strings.ReplaceAll(uuid.NewString(), "-", ""), SourceScopeID: scope, MaintenanceSHA256: strings.Repeat("a", 64), SourceManifestSHA256: strings.Repeat("b", 64), ControlImageSHA256: strings.Repeat("c", 64), PostgresImageSHA256: strings.Repeat("d", 64), TenantID: tenant, Database: pc.ConnConfig.Database, Operator: pc.ConnConfig.User, RuntimeConfigurationFile: runtimePath, ConfigurationRoot: configuration, BackupRoot: filepath.Join(backups, "direct"), Reason: "Actual synthetic matched backup; no cluster/physical qualification", Policy: p}
	r.ApplicationProfile = tools.applicationProfile
	if r.EffectiveApplicationProfile() == release.MVPPostgreSQLCPUApplicationProfile {
		var artifacts struct {
			Format               string `json:"format"`
			SourceManifestSHA256 string `json:"source_manifest_sha256"`
			ControlImageSHA256   string `json:"control_image_sha256"`
			PostgresImageSHA256  string `json:"postgres_image_sha256"`
		}
		if _, e = readPrivateDocument(filepath.Join(root, "native-artifacts.json"), MaximumRequestBytes, &artifacts); e != nil || artifacts.Format != "projectone.mvp.native-fixture-artifacts.v1" || !release.ValidSHA256(artifacts.SourceManifestSHA256) || !release.ValidSHA256(artifacts.ControlImageSHA256) || !release.ValidSHA256(artifacts.PostgresImageSHA256) {
			t.Fatal("explicit actual native fixture artifact bindings required", e)
		}
		source, e := fileDigest(ctx, filepath.Join(root, "native-source.json"), MaximumRequestBytes)
		if e != nil || source.SHA256 != artifacts.SourceManifestSHA256 {
			t.Fatal("actual native source descriptor changed", e)
		}
		r.SourceManifestSHA256 = artifacts.SourceManifestSHA256
		r.ControlImageSHA256 = artifacts.ControlImageSHA256
		r.PostgresImageSHA256 = artifacts.PostgresImageSHA256
	}
	requestPath := filepath.Join(root, "request-direct.json")
	b, _ = canonical(r)
	write(requestPath, b)
	// Complete native input admission occurs before opening any SQL connection.
	if _, e = configurationSnapshot(c, runtime, operatorPath, r); e != nil {
		t.Fatal("non-SQL configuration closure", e)
	}
	if e = AdmitConnection(dsnFile, r); e != nil {
		t.Fatal("non-SQL operator connection admission", e)
	}
	if e = verifyTools(ctx, p, false); e != nil {
		t.Fatal("actual pinned native clients before SQL", e)
	}
	if e = trustedTool(ctx, p.ControlFile, p.ControlSHA256); e != nil {
		t.Fatal("actual current Control before SQL", e)
	}
	floor := max(c.Policy().Datasets.MinimumFreeBytes, c.Policy().Training.MinimumFreeBytes, c.Policy().ModelLifecycle.MinimumFreeBytes)
	if e = requireSpace(root, 1<<30, floor+32<<30); e != nil {
		t.Fatal("actual fixture default domain and fixed-reserve space admission", e)
	}
	inputRoot, e := openPrivateRoot(root)
	if e != nil {
		t.Fatal(e)
	}
	e = writeDocument(inputRoot, "input-preflight.json", map[string]any{"scope": scope, "status": "all_native_inputs_admitted_before_any_SQL_connection", "domain_minimum_free_bytes": floor, "runtime_credentials": "existing canonical private producer; no value in receipt"}, p.MaximumManifestBytes)
	inputRoot.Close()
	if e != nil {
		t.Fatal(e)
	}
	// Explicit local harness barrier: the parent creates and marks one new
	// disposable database only after reading the successful preflight. Missing,
	// malformed or unknown-outcome permission never initiates SQL from this test.
	for {
		value, readError := readPrivateBytes(filepath.Join(root, "allow-sql"), 64)
		if readError == nil {
			if string(value) != scope+"\n" {
				t.Fatal("wrong explicit SQL fixture scope")
			}
			break
		}
		if _, statError := os.Lstat(filepath.Join(root, "allow-sql")); !os.IsNotExist(statError) {
			t.Fatal("invalid private SQL fixture barrier")
		}
		select {
		case <-ctx.Done():
			t.Fatal("no explicit SQL fixture admission before deadline")
		case <-time.After(100 * time.Millisecond):
		}
	}
	pool, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal("fixture SQL open")
	}
	t.Cleanup(pool.Close)
	var marker string
	var tables int
	if pool.QueryRow(ctx, `SELECT pg_catalog.shobj_description((SELECT oid FROM pg_catalog.pg_database WHERE datname=current_database()),'pg_database')`).Scan(&marker) != nil || marker != "projectone:m18-backup:"+scope+":source" || pool.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_tables WHERE schemaname='public'`).Scan(&tables) != nil || tables != 0 {
		t.Fatal("fresh exact disposable database marker required")
	}
	if e = database.Migrate(ctx, pool, r.EffectiveApplicationProfile()); e != nil {
		t.Fatal("complete selected fixture migration", e)
	}
	lease, e := pool.Acquire(ctx)
	if e != nil {
		t.Fatal("lease")
	}
	held := false
	if lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&held) != nil || !held {
		lease.Release()
		t.Fatal("original installation lock")
	}
	releaseLease := func() {
		if !held {
			return
		}
		var unlocked bool
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if lease.QueryRow(cleanup, "SELECT pg_advisory_unlock($1)", database.InstallationLockID).Scan(&unlocked) != nil || !unlocked {
			t.Error("lease release unconfirmed")
		}
		lease.Release()
		held = false
	}
	t.Cleanup(releaseLease)
	if status, e := databaseaccess.Prepare(ctx, pool, lease, input); e != nil || status != databaseaccess.Verified {
		t.Fatal("real runtime principal", status, e)
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO public.tenants(id,name,status) VALUES($1,'Synthetic matched backup tenant','active')`, []any{tenant}},
		{`INSERT INTO public.workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic matched backup Workspace','active')`, []any{workspace, tenant}},
		{`INSERT INTO public.users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,'backup@example.invalid','backup@example.invalid','Synthetic backup actor','en','active',true)`, []any{user}},
		{`INSERT INTO public.workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES($1,$2,$3,'owner')`, []any{tenant, workspace, user}},
		{`UPDATE public.installation_state SET bootstrap_complete=true,tenant_id=$1,default_workspace_id=$2,primary_admin_user_id=$3,bootstrapped_at=clock_timestamp() WHERE id=1`, []any{tenant, workspace, user}},
	} {
		if _, e = pool.Exec(ctx, q.sql, q.args...); e != nil {
			t.Fatal("actual graph seed", e)
		}
	}
	for _, path := range storeRoots(c) {
		if os.Mkdir(path, 0700) != nil {
			t.Fatal("distinct empty source root")
		}
	}
	if e = models.InitializeStore(c.Models.ArtifactRoot, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	for _, initialize := range []func() error{func() error {
		return datasets.InitializeStore(c.Datasets.ArtifactRoot, tenant, os.Geteuid(), os.Getegid())
	}, func() error {
		return training.InitializeStore(c.Training.ArtifactRoot, tenant, os.Geteuid(), os.Getegid())
	}, func() error {
		return modelpreparations.InitializeStore(c.ModelPreparation.ArtifactRoot, tenant, os.Geteuid(), os.Getegid())
	}} {
		if e = initialize(); e != nil {
			t.Fatal(e)
		}
	}
	closeFixture := func() { releaseLease(); pool.Close(); cancel() }
	return actualPostgresBackupFixture{ctx, pool, lease, c, runtime, operatorPath, requestPath, r, releaseLease, closeFixture, write}
}

func TestActualPostgresMatchedBackupNativeCLIAndReadback(t *testing.T) {
	f := newActualPostgresBackupFixture(t, 180*time.Second, 120, 1<<30, 100, 4<<20)
	defer f.close()
	ctx, pool, lease, c, runtime := f.ctx, f.pool, f.lease, f.operator, f.runtime
	operatorPath, requestPath, r, releaseLease, write := f.operatorPath, f.requestPath, f.request, f.releaseLease, f.write
	root, p := "/fixture", r.Policy
	backups := filepath.Join(root, "backups")
	var b []byte
	var e error
	if _, e = Backup(ctx, pool, lease, c, runtime, operatorPath, requestPath, "", r); e != nil {
		t.Fatal("actual matched export", e)
	}
	var complete Completion
	_, e = readPrivateDocument(filepath.Join(r.BackupRoot, completeName), p.MaximumManifestBytes, &complete)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Verify(ctx, r, complete.ManifestSHA256, true); e != nil {
		t.Fatal("actual independent complete readback", e)
	}
	if _, e = Backup(ctx, pool, lease, c, runtime, operatorPath, requestPath, "", r); e == nil || e.Error() != "POSTGRES_BACKUP_DESTINATION_EXISTS" {
		t.Fatal("backup overwrite not refused", e)
	}
	// The caller still owns its live installation session after the entire path.
	var same bool
	if lease.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND objid=$1::oid AND granted)`, database.InstallationLockID).Scan(&same) != nil || !same {
		t.Fatal("backup released caller lock")
	}
	releaseLease()
	r.BackupID = strings.ReplaceAll(uuid.NewString(), "-", "")
	r.BackupRoot = filepath.Join(backups, "native")
	requestPath = filepath.Join(root, "request-native.json")
	b, _ = canonical(r)
	write(requestPath, b)
	response := &limitedBuffer{maximum: p.MaximumManifestBytes}
	if e = runNative(ctx, p, p.ControlFile, []string{"backup-kubernetes-postgres", "--config", operatorPath, "--request", requestPath}, nil, nil, response); e != nil {
		t.Fatal("actual current native master", e)
	}
	if json.Unmarshal(response.Bytes(), &complete) != nil || complete.Format != "projectone.postgres.backup-complete.v1" {
		t.Fatal("native completion DTO")
	}
	if _, e = Verify(ctx, r, complete.ManifestSHA256, true); e != nil {
		t.Fatal("native complete readback", e)
	}
	if _, e = pool.Exec(ctx, `SELECT 1`); e != nil {
		t.Fatal("source preserved")
	}
	write(filepath.Join(root, "actual-receipt.json"), []byte("{\"status\":\"actual_empty-domain-stores_22_86_direct-and-native_matched-backups\",\"limitation\":\"No populated domain artifacts, fresh restore, Kubernetes or TLS qualification in this test\"}\n"))
}
