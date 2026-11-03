//go:build repository_integration

package main

import (
	"context"
	"debug/elf"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
)

// Real production ELF/CLI, fresh private public-schema database and restricted
// runtime login. File stores/provider assets in the example are references only;
// this does not claim a complete positive serving graph or browser qualification.
func TestMVPActivationNativeCLIAndOriginalRuntimeAuthority(t *testing.T) {
	container := os.Getenv("PROJECT_ONE_ACTIVATION_POSTGRES_CONTAINER")
	if !strings.HasPrefix(container, "p1-20261103-platform-composition-activation-") || os.Getenv("GOTMPDIR") != "" || os.Getenv("TMPDIR") == "" {
		t.Fatal("EXACT_DURABLE_OWNED_MVP_ACTIVATION_FIXTURE_REQUIRED")
	}
	binary := os.Getenv("PROJECT_ONE_TEST_CONTROL_BINARY")
	info, e := os.Lstat(binary)
	if e != nil || !filepath.IsAbs(binary) || !info.Mode().IsRegular() {
		t.Fatal("ACTUAL_CONTROL_ELF_REQUIRED")
	}
	executable, e := elf.Open(binary)
	if e != nil {
		t.Fatal("actual control ELF", e)
	}
	executable.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	root := t.TempDir()
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	private := func(name string, body []byte) string {
		p := filepath.Join(root, name)
		if e := os.WriteFile(p, body, 0600); e != nil {
			t.Fatal(e)
		}
		return p
	}
	raw, e := os.ReadFile("../../config/examples/control/mvp-postgresql-cpu-v1.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	c, e := config.Decode(raw)
	if e != nil {
		t.Fatal(e)
	}
	base, e := config.Secret(os.Getenv("PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE"))
	if e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(strings.TrimSpace(string(base)))
	if e != nil || u.User == nil || u.Hostname() != "127.0.0.1" || !strings.HasSuffix(u.Path, "_test") {
		t.Fatal("ISOLATED_LOOPBACK_DATABASE_REQUIRED")
	}
	q := u.Query()
	q.Set("search_path", "public")
	u.RawQuery = q.Encode()
	c.Database.DSNFile = private("operator-dsn", []byte(u.String()))
	legacy := config.ReleasesCacheV1Profile
	c.Server.ApplicationProfile = &legacy
	operatorPath := private("operator.yaml", mustActivationYAML(t, c))
	operator, e := database.Open(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(operator.Close)
	var tables int
	if e = operator.QueryRow(ctx, "SELECT count(*) FROM pg_catalog.pg_tables WHERE schemaname='public'").Scan(&tables); e != nil || tables != 0 {
		t.Fatal("exact disposable database was not empty", e)
	}
	if e = database.Migrate(ctx, operator, "releases"); e != nil {
		t.Fatal("actual account22 migration", e)
	}
	rows, e := operator.Query(ctx, "SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname='public'")
	if e != nil {
		t.Fatal(e)
	}
	var originalTables []string
	for rows.Next() {
		var n string
		if e = rows.Scan(&n); e != nil {
			t.Fatal(e)
		}
		originalTables = append(originalTables, n)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	role := "mvp_activation_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password, e := databaseaccess.NewCredential()
	if e != nil {
		t.Fatal(e)
	}
	u.User = url.UserPassword(role, password)
	runtimeConfig := c
	runtimeConfig.Database.DSNFile = private("runtime-dsn", []byte(u.String()))
	runtimePath := private("runtime.yaml", mustActivationYAML(t, runtimeConfig))
	input, e := databaseaccess.LoadRuntime(runtimePath)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer stop()
		var exists bool
		if e := operator.QueryRow(cleanup, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", role).Scan(&exists); e != nil {
			t.Error("owned role existence readback", e)
			return
		}
		if !exists {
			return
		}
		if _, e := operator.Exec(cleanup, "DROP OWNED BY "+pgx.Identifier{role}.Sanitize()); e != nil {
			t.Error("owned runtime grant cleanup", e)
			return
		}
		if _, e := operator.Exec(cleanup, "DROP ROLE "+pgx.Identifier{role}.Sanitize()); e != nil {
			t.Error("owned runtime role cleanup", e)
		}
	})
	lease, e := operator.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	initialLeaseHeld := true
	t.Cleanup(func() {
		if initialLeaseHeld {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer stop()
			_, _ = lease.Exec(cleanup, "SELECT pg_advisory_unlock($1)", database.InstallationLockID)
			lease.Release()
		}
	})
	var locked bool
	if e = lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&locked); e != nil || !locked {
		t.Fatal("initial owned lease", e)
	}
	if _, e = databaseaccess.Prepare(ctx, operator, lease, input); e != nil {
		t.Fatal("prepare retained account22 runtime", e)
	}
	var originalOID uint32
	var originalVerifier string
	if e = lease.QueryRow(ctx, "SELECT oid,rolpassword FROM pg_authid WHERE rolname=$1", role).Scan(&originalOID, &originalVerifier); e != nil {
		t.Fatal(e)
	}
	if _, e = lease.Exec(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID); e != nil {
		t.Fatal(e)
	}
	lease.Release()
	initialLeaseHeld = false
	tenant, workspace, user := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, row := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO public.tenants(id,name,status) VALUES($1,'Retained CLI tenant','active')`, []any{tenant}},
		{`INSERT INTO public.workspaces(id,tenant_id,name,status) VALUES($1,$2,'Retained CLI Workspace','active')`, []any{workspace, tenant}},
		{`INSERT INTO public.users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,'activation@example.invalid','activation@example.invalid','Retained CLI user','en','active',true)`, []any{user}},
		{`UPDATE public.installation_state SET bootstrap_complete=true,tenant_id=$1,default_workspace_id=$2,primary_admin_user_id=$3,bootstrapped_at=clock_timestamp() WHERE id=1`, []any{tenant, workspace, user}},
	} {
		if _, e = operator.Exec(ctx, row.sql, row.args...); e != nil {
			t.Fatal(e)
		}
	}

	environment := private("profile.env", []byte("PROJECT_ONE_APPLICATION_PROFILE=mvp-postgresql-cpu-v1\nPROJECT_ONE_MODEL_CACHE_ENABLED=false\nPROJECT_ONE_HTTP_INSTALLATION_LEASE_INTERVAL_SECONDS=2\n"))
	cli := func(command string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, command, "--config", operatorPath, "--runtime-config", runtimePath, "--env-file", environment)
		output, e := cmd.Output()
		if e != nil {
			if failure, ok := e.(*exec.ExitError); ok {
				t.Fatalf("actual CLI failed %s: %v %s", command, e, string(failure.Stderr))
			}
			t.Fatal(command, e)
		}
		return strings.TrimSpace(string(output))
	}
	if got := cli(databaseaccess.MVPPostgreSQLCPUV1InspectCommand); got != databaseaccess.MVPPostgreSQLCPUV1UpgradeRequired {
		t.Fatal("actual pre-upgrade inspection", got)
	}
	for i := 0; i < 2; i++ {
		if got := cli(databaseaccess.MVPPostgreSQLCPUV1UpgradeCommand); got != databaseaccess.MVPPostgreSQLCPUV1UpgradeComplete {
			t.Fatal("actual upgrade/noop", got)
		}
	}
	if got := cli(databaseaccess.MVPPostgreSQLCPUV1InspectCommand); got != databaseaccess.MVPPostgreSQLCPUV1UpgradeComplete {
		t.Fatal("actual completed inspection", got)
	}
	var currentOID uint32
	var currentVerifier string
	if e = operator.QueryRow(ctx, "SELECT oid,rolpassword FROM pg_authid WHERE rolname=$1", role).Scan(&currentOID, &currentVerifier); e != nil || currentOID != originalOID || currentVerifier != originalVerifier {
		t.Fatal("existing runtime principal changed")
	}
	var retained bool
	if e = operator.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.tenants t JOIN public.workspaces w ON w.tenant_id=t.id JOIN public.users u ON u.id=$3 WHERE t.id=$1 AND w.id=$2 AND t.name='Retained CLI tenant' AND u.display_name='Retained CLI user')`, tenant, workspace, user).Scan(&retained); e != nil || !retained {
		t.Fatal("CLI upgrade lost original rows/IDs", e)
	}
	effective, e := config.LoadProtectedWithEnvironment(runtimePath, environment)
	if e != nil || !effective.MVPPostgreSQLCPUV1Only() || effective.AccountRuntime() || effective.ModelCache.Enabled || effective.Policy().HTTP.InstallationLeaseIntervalSeconds != 2 {
		t.Fatal("effective runtime projection", e)
	}
	runtime, e := openSelectedDatabase(ctx, effective, "serve")
	if e != nil {
		t.Fatal("actual restricted runtime login", e)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	var originalPID, currentPID int
	if e = runtime.pgLease.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&originalPID); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e = verifyServingDatabase(ctx, effective, runtime); e != nil {
			t.Fatal("strict startup/readiness with original session", e)
		}
	}
	if e = runtime.pgLease.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&currentPID); e != nil || currentPID != originalPID {
		t.Fatal("serving check replaced original lease", e)
	}
	if _, e = runtime.postgres.Exec(ctx, "CREATE TABLE public.unapproved_activation_ddl(id integer)"); e == nil {
		t.Fatal("runtime acquired persistent DDL")
	}
	var newTable string
	if e = operator.QueryRow(ctx, "SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname='public' AND NOT(tablename=ANY($1::text[])) ORDER BY tablename LIMIT 1", originalTables).Scan(&newTable); e != nil {
		t.Fatal(e)
	}
	if _, e = operator.Exec(ctx, "REVOKE SELECT ON "+pgx.Identifier{newTable}.Sanitize()+" FROM "+pgx.Identifier{role}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	if e = verifyServingDatabase(ctx, effective, runtime); e == nil {
		t.Fatal("catalog-only/grants-pending principal admitted serving")
	}
	if e = runtime.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e = operator.Exec(ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON "+pgx.Identifier{newTable}.Sanitize()+" TO "+pgx.Identifier{role}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	fresh, e := openSelectedDatabase(ctx, effective, "serve")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = fresh.Close() })
	if e = verifyServingDatabase(ctx, effective, fresh); e != nil {
		t.Fatal("explicit fresh strict runtime", e)
	}
	if e = fresh.Close(); e != nil {
		t.Fatal(e)
	}
	adminConfig, e := config.LoadProtectedWithEnvironment(operatorPath, environment)
	if e != nil {
		t.Fatal(e)
	}
	admin, e := openSelectedDatabase(ctx, adminConfig, "serve")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = admin.Close() })
	if e = serveSelected(ctx, ctx, adminConfig, admin, nil, nil); e == nil || e.Error() != "MVP_POSTGRESQL_RUNTIME_PRIVILEGES_INCOMPATIBLE" || admin.workers != nil {
		t.Fatal("superuser passed startup before full graph", e)
	}
	if e = admin.Close(); e != nil {
		t.Fatal(e)
	}
	observation, e := observePostgresMaintenance(ctx, adminConfig)
	if e != nil || observation.ApplicationProfile != config.MVPPostgreSQLCPUV1Profile || observation.SchemaVersion != 29 || observation.CurrentLockHeld {
		t.Fatal("actual full-profile locked maintenance projection", e)
	}
	t.Log("ACTUAL_MVP_CLI env_only22_to29_108_same_role_OID_SCRAM_old_rows strict_runtime_original_lease ddl_denied_partial_grant_refused_superuser_before_graph_refused maintenance_exact_profile")
}

func mustActivationYAML(t *testing.T, c config.Config) []byte {
	t.Helper()
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
