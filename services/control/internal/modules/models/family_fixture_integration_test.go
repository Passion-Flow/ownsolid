//go:build repository_integration

package models_test

import (
	"context"
	"crypto/rand"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.yaml.in/yaml/v3"
)

// Only fixture setup and explicit corruption keep the operator. Every service
// call in familySetup uses a separate real Control login, never SET ROLE.
func familySetup(t *testing.T) modelReadFixture {
	t.Helper()
	f := repositoryfixture.NewUnbootstrapped(t)
	if f.Store.Dialect() != transaction.PostgreSQL {
		t.Fatal("MODEL_FAMILY_EXPLICIT_POSTGRESQL_REQUIRED")
	}
	operator := f.PostgreSQL
	runtime := familyControlPool(t, operator)
	store, err := transaction.FromPostgreSQL(runtime)
	if err != nil {
		t.Fatal(err)
	}
	f.Store, f.PostgreSQL = store, runtime
	familySeedIdentity(t, f)
	x := modelReadSetupOn(t, f)
	x.operator = operator
	familySigningKey(t, x)
	familyAdmitted(t, x)
	return x
}

func familySigningKey(t *testing.T, x modelReadFixture) {
	t.Helper()
	x.owner.HMACKey = make([]byte, 32)
	if _, err := rand.Read(x.owner.HMACKey); err != nil {
		t.Fatal(err)
	}
}

func familySeedIdentity(t *testing.T, f repositoryfixture.Fixture) {
	t.Helper()
	ctx := t.Context()
	if err := f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		for _, statement := range []transaction.Statement{
			transaction.SQL(transaction.PostgreSQL, `INSERT INTO tenants(id,name,status) VALUES($1,'Synthetic model family tenant','active')`, f.Tenant),
			transaction.SQL(transaction.PostgreSQL, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic model family workspace','active')`, f.Workspace, f.Tenant),
			transaction.SQL(transaction.PostgreSQL, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,'family@example.invalid','family@example.invalid','Synthetic family actor','en','active',true)`, f.User),
			transaction.SQL(transaction.PostgreSQL, `UPDATE installation_state SET bootstrap_complete=true,tenant_id=$1,default_workspace_id=$2,primary_admin_user_id=$3,bootstrapped_at=clock_timestamp() WHERE id=1`, f.Tenant, f.Workspace, f.User),
		} {
			if _, err := q.Exec(ctx, statement); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal("synthetic family identity seed", err)
	}
}

func familyPredecessor(t *testing.T) modelReadFixture {
	t.Helper()
	if os.Getenv("PROJECT_ONE_TEST_DSN_FILE") == "" && os.Getenv("PROJECT_ONE_TEST_DSN") == "" {
		t.Fatal("explicit predecessor PostgreSQL fixture required")
	}
	owner := identityfixture.NewAtVersion(t, 60)
	store, err := transaction.FromPostgreSQL(owner.DB)
	if err != nil {
		t.Fatal(err)
	}
	f := repositoryfixture.Fixture{Store: store, PostgreSQL: owner.DB, Tenant: uuid.NewString(), Workspace: uuid.NewString(), User: uuid.NewString()}
	familySeedIdentity(t, f)
	x := modelReadSetupOn(t, f)
	x.operator = owner.DB
	familySigningKey(t, x)
	return x
}

func familyAdmitted(t *testing.T, x modelReadFixture) {
	t.Helper()
	version, err := database.CheckPrefixWithin(x.ctx, x.f.Store)
	if err != nil || version != database.SchemaVersion || version < 61 {
		t.Fatal("formal family lineage is not admitted", version, err)
	}
	familyScopedVersion(t, x, x.model)
}

func familyScopedVersion(t *testing.T, x modelReadFixture, version string) {
	t.Helper()
	var local bool
	if err := x.f.Store.QueryRow(x.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT
 (f.site_id,f.cell_id,m.site_id,m.cell_id,v.site_id,v.cell_id)=
 (local_site_id(),local_cell_id(),local_site_id(),local_cell_id(),local_site_id(),local_cell_id())
 FROM model_versions v JOIN model_family_members m ON m.version_id=v.id
 JOIN model_families f ON f.id=m.family_id WHERE v.id=$1`, version)).Scan(&local); err != nil || !local {
		t.Fatal("formal family/member/version scope", err)
	}
}

func familyOperatorExec(t *testing.T, x modelReadFixture, sql string, args ...any) {
	t.Helper()
	if x.operator == nil {
		t.Fatal("explicit fixture operator required")
	}
	if _, err := x.operator.Exec(x.ctx, sql, args...); err != nil {
		t.Fatal("explicit synthetic corruption", err)
	}
}

func familyControlPool(t *testing.T, operator *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), modelReadFixtureTimeout)
	defer cancel()
	role := "m103_control_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password, err := databaseaccess.NewCredential()
	if err != nil {
		t.Fatal("synthetic runtime credential", err)
	}
	raw, err := config.Secret(os.Getenv("PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE"))
	if err != nil {
		t.Fatal("private fixture DSN required")
	}
	uri, err := url.Parse(strings.TrimSpace(string(raw)))
	if err != nil || uri.Scheme != "postgres" && uri.Scheme != "postgresql" || uri.Hostname() != "127.0.0.1" || !strings.HasSuffix(uri.Path, "_test") {
		t.Fatal("explicit loopback PostgreSQL test URI required")
	}
	uri.User = url.UserPassword(role, password)
	query := uri.Query()
	query.Set("search_path", operator.Config().ConnConfig.RuntimeParams["search_path"])
	uri.RawQuery = query.Encode()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil || os.Chmod(root, 0700) != nil {
		t.Fatal("private fixture parent required")
	}
	var c config.Config
	c.Server.ListenAddress, c.Server.AdminOrigin, c.Server.PortalOrigin = "127.0.0.1:18443", "https://admin.example.invalid", "https://portal.example.invalid"
	c.Database.DSNFile = filepath.Join(root, "runtime-dsn")
	c.Database.MaxOpenConnections, c.Database.MaxIdleConnections, c.Database.StatementTimeoutSeconds = 2, 0, 5
	c.SMTP.Host, c.SMTP.Port, c.SMTP.FromAddress, c.SMTP.TLSMode = "smtp.example.invalid", 587, "platform@example.invalid", "starttls_required"
	c.SMTP.ConnectTimeoutSeconds, c.SMTP.OperationTimeoutSeconds = 5, 20
	if err := os.WriteFile(c.Database.DSNFile, []byte(uri.String()), 0600); err != nil {
		t.Fatal("private runtime DSN write failed")
	}
	configuration := filepath.Join(root, "runtime.yaml")
	encoded, err := yaml.Marshal(c)
	if err != nil || os.WriteFile(configuration, encoded, 0600) != nil {
		t.Fatal("private runtime configuration write failed")
	}
	input, err := databaseaccess.LoadRuntime(configuration)
	if err != nil {
		t.Fatal("runtime input validation", err)
	}
	lease, err := operator.Acquire(ctx)
	if err != nil {
		t.Fatal("fixture operator lease", err)
	}
	defer lease.Release()
	var locked bool
	if err := lease.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, database.InstallationLockID).Scan(&locked); err != nil || !locked {
		t.Fatal("fixture installation lease unavailable", err)
	}
	defer lease.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, database.InstallationLockID)
	var existing bool
	if err := lease.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, role).Scan(&existing); err != nil || existing {
		t.Fatal("fresh owned Control principal required", err)
	}
	// Preserve the disposable principal identity for original-owner cleanup;
	// its credential and URI must never enter test output.
	t.Log("MODEL_FAMILY_OWNED_CONTROL_ROLE", role)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		var exists bool
		if err := operator.QueryRow(cleanup, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, role).Scan(&exists); err != nil {
			t.Error("owned runtime principal inventory failed")
			return
		}
		if !exists {
			return
		}
		if _, err := operator.Exec(cleanup, "DROP OWNED BY "+pgx.Identifier{role}.Sanitize()); err != nil {
			t.Error("owned runtime grants cleanup failed")
			return
		}
		if _, err := operator.Exec(cleanup, "DROP ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
			t.Error("owned runtime principal cleanup failed")
		}
	})
	if state, err := databaseaccess.Prepare(ctx, operator, lease, input); err != nil || state != databaseaccess.Verified {
		t.Fatal("original Control role preparation", state, err)
	}
	if state, err := databaseaccess.Check(ctx, operator, lease, input); err != nil || state != databaseaccess.Verified {
		t.Fatal("original Control role verification", state, err)
	}
	parsed, err := config.LoadProtected(configuration)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := database.Open(ctx, parsed)
	if err != nil {
		t.Fatal("actual Control login", err)
	}
	t.Cleanup(runtime.Close)
	var restricted bool
	if err := runtime.QueryRow(ctx, `SELECT session_user=current_user AND current_user=$1
 AND NOT rolsuper AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolinherit AND NOT rolreplication AND NOT rolbypassrls
 FROM pg_roles WHERE rolname=current_user`, role).Scan(&restricted); err != nil || !restricted {
		t.Fatal("actual restricted Control login required", err)
	}
	store, err := transaction.FromPostgreSQL(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CheckRuntimeWithin(ctx, store); err != nil {
		t.Fatal("strict runtime admission", err)
	}
	return runtime
}
