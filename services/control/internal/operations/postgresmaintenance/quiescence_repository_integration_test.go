//go:build repository_integration && postgres_maintenance_integration

package postgresmaintenance

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func actualMaintenancePool(t *testing.T, role string) *pgxpool.Pool {
	t.Helper()
	variable, prefix := "PROJECT_ONE_TEST_POSTGRES_MAINTENANCE_DSN_FILE", "m18_maintenance_"
	if role == "predicates" {
		variable, prefix = "PROJECT_ONE_TEST_POSTGRES_PREDICATES_DSN_FILE", "m18_predicates_"
	}
	raw, e := config.Secret(os.Getenv(variable))
	if e != nil {
		t.Fatal("private isolated maintenance input required")
	}
	pc, e := pgxpool.ParseConfig(strings.TrimSpace(string(raw)))
	if e != nil || pc.ConnConfig.Host != "127.0.0.1" || pc.ConnConfig.Port < 1024 || pc.ConnConfig.User != "projectone_operator" || !regexp.MustCompile("^"+prefix+"[a-f0-9]{32}_test$").MatchString(pc.ConnConfig.Database) {
		t.Fatal("exact owned local PostgreSQL database required")
	}
	scope := strings.TrimSuffix(strings.TrimPrefix(pc.ConnConfig.Database, prefix), "_test")
	pc.MaxConns = 3
	pc.ConnConfig.RuntimeParams["search_path"] = "public"
	pc.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	pc.ConnConfig.RuntimeParams["lock_timeout"] = "1000"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal("owned PostgreSQL open")
	}
	t.Cleanup(pool.Close)
	var marker string
	if pool.QueryRow(ctx, `SELECT pg_catalog.shobj_description((SELECT oid FROM pg_catalog.pg_database WHERE datname=pg_catalog.current_database()),'pg_database')`).Scan(&marker) != nil || marker != "projectone:m18-maintenance:"+scope+":"+role {
		t.Fatal("exact disposable database ownership marker required")
	}
	var count int
	if pool.QueryRow(ctx, "SELECT count(*) FROM pg_catalog.pg_tables WHERE schemaname='public'").Scan(&count) != nil || count != 0 {
		t.Fatal("fresh empty owned database required; retain failed setup rather than reset")
	}
	return pool
}

func actualMaintenanceLease(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *pgxpool.Conn {
	t.Helper()
	lease, e := pool.Acquire(ctx)
	if e != nil {
		t.Fatal("original lease connection")
	}
	var held bool
	if lease.QueryRow(ctx, "SELECT pg_catalog.pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&held) != nil || !held {
		lease.Release()
		t.Fatal("original installation lock")
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var released bool
		if lease.QueryRow(cleanup, "SELECT pg_catalog.pg_advisory_unlock($1)", database.InstallationLockID).Scan(&released) != nil || !released {
			t.Error("original lock release unconfirmed")
		}
		lease.Release()
	})
	return lease
}

func actualMaintenanceCheck(t *testing.T, ctx context.Context, lease *pgxpool.Conn, mode pgx.TxAccessMode, setup string) (Observation, error) {
	t.Helper()
	tx, e := lease.BeginTx(ctx, pgx.TxOptions{AccessMode: mode})
	if e != nil {
		t.Fatal("borrowed maintenance transaction")
	}
	if setup != "" {
		if _, e = tx.Exec(ctx, setup); e != nil {
			t.Fatal("owned session setup")
		}
	}
	out, result := CheckWithin(ctx, transaction.PostgreSQLTransaction(tx), ApplicationProfile)
	// The production reader has no commit/rollback authority over its caller.
	if result == nil {
		var alive int
		if tx.QueryRow(ctx, "SELECT 1").Scan(&alive) != nil || alive != 1 {
			t.Fatal("successful reader ended caller transaction")
		}
	}
	if e = tx.Rollback(ctx); e != nil {
		t.Fatal("caller read transaction close")
	}
	return out, result
}

func TestActualPostgresMaintenanceCompleteCatalogAuthorityAndSameSessionGate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool := actualMaintenancePool(t, "full")
	if e := database.Migrate(ctx, pool, ApplicationProfile); e != nil {
		t.Fatal("actual complete releases migration", e)
	}
	lease := actualMaintenanceLease(t, ctx, pool)
	if out, e := actualMaintenanceCheck(t, ctx, lease, pgx.ReadOnly, ""); e == nil || e.Error() != "POSTGRES_MAINTENANCE_INSTALLATION_INVALID" || out.CurrentLockHeld {
		t.Fatal("unbootstrapped authority accepted", out, e)
	}
	tenant, workspace, user := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO public.tenants(id,name,status) VALUES($1,'Synthetic maintenance tenant','active')", []any{tenant}},
		{"INSERT INTO public.workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic maintenance Workspace','active')", []any{workspace, tenant}},
		{"INSERT INTO public.users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,'maintenance@example.invalid','maintenance@example.invalid','Synthetic maintenance actor','en','active',true)", []any{user}},
		{"UPDATE public.installation_state SET bootstrap_complete=true,tenant_id=$1,default_workspace_id=$2,primary_admin_user_id=$3,bootstrapped_at=clock_timestamp() WHERE id=1", []any{tenant, workspace, user}},
	} {
		if _, e := pool.Exec(ctx, q.sql, q.args...); e != nil {
			t.Fatal("actual installed authority fixture seed", e)
		}
	}
	out, e := actualMaintenanceCheck(t, ctx, lease, pgx.ReadOnly, "")
	if e != nil || !out.CurrentLockHeld || out.TenantID != tenant || out.SchemaVersion != 22 || len(out.Checks) != 14 || out.ApplicationProfile != ApplicationProfile {
		t.Fatal("actual full catalog/quiescence", out, e)
	}
	if _, e = actualMaintenanceCheck(t, ctx, lease, pgx.ReadWrite, ""); e == nil || e.Error() != "POSTGRES_MAINTENANCE_LOCKED_READ_ONLY_SESSION_REQUIRED" {
		t.Fatal("read-write transaction admitted", e)
	}
	if _, e = actualMaintenanceCheck(t, ctx, lease, pgx.ReadOnly, "SET LOCAL search_path=public,pg_catalog"); e == nil || e.Error() != "POSTGRES_MAINTENANCE_LOCKED_READ_ONLY_SESSION_REQUIRED" {
		t.Fatal("untrusted schema order admitted", e)
	}
	other, e := pool.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = actualMaintenanceCheck(t, ctx, other, pgx.ReadOnly, ""); e == nil || e.Error() != "POSTGRES_MAINTENANCE_LOCKED_READ_ONLY_SESSION_REQUIRED" {
		t.Fatal("other session borrowed lock authority", e)
	}
	other.Release()
	var old string
	if lease.QueryRow(ctx, "SELECT checksum FROM public.schema_migrations WHERE version=22").Scan(&old) != nil {
		t.Fatal("original ledger")
	}
	if _, e = lease.Exec(ctx, "UPDATE public.schema_migrations SET checksum=$1 WHERE version=22", strings.Repeat("0", 64)); e != nil {
		t.Fatal(e)
	}
	if _, e = actualMaintenanceCheck(t, ctx, lease, pgx.ReadOnly, ""); e == nil || e.Error() != "POSTGRES_MAINTENANCE_SCHEMA_INCOMPATIBLE" {
		t.Fatal("changed ledger admitted", e)
	}
	if _, e = lease.Exec(ctx, "UPDATE public.schema_migrations SET checksum=$1 WHERE version=22", old); e != nil {
		t.Fatal("exact owned ledger restore", e)
	}
	if _, e = actualMaintenanceCheck(t, ctx, lease, pgx.ReadOnly, ""); e != nil {
		t.Fatal("restored exact catalog", e)
	}
	var unlocked bool
	if lease.QueryRow(ctx, "SELECT pg_catalog.pg_advisory_unlock($1)", database.InstallationLockID).Scan(&unlocked) != nil || !unlocked {
		t.Fatal("known fixture lock release")
	}
	if _, e = actualMaintenanceCheck(t, ctx, lease, pgx.ReadOnly, ""); e == nil || e.Error() != "POSTGRES_MAINTENANCE_LOCKED_READ_ONLY_SESSION_REQUIRED" {
		t.Fatal("lost session lock admitted", e)
	}
	var held bool
	if lease.QueryRow(ctx, "SELECT pg_catalog.pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&held) != nil || !held {
		t.Fatal("explicit fixture lock reacquisition")
	}
	// Leave the exact full production schema and installed graph for the separate
	// actual native CLI check. External fixture ownership handles final disposal.
	t.Log("Actual complete releases22 PostgreSQL catalog and installed authority; same-session lock/read-only/search-path/catalog controls. No remote process or backup qualification.")
}

type predicateCase struct {
	table, kind, columns string
	blocked, clear       []string
}

func TestActualPostgresMaintenancePredicateSQLFixturesRemainSeparateFromCompleteCatalog(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool := actualMaintenancePool(t, "predicates")
	cases := []predicateCase{
		{"node_tasks", "NODE_TASKS", "state text NOT NULL", []string{"('pending')", "('running')"}, []string{"('succeeded')", "('failed')", "('cancelled')"}},
		{"managed_components", "COMPONENTS", "desired_state text NOT NULL,observed_state text NOT NULL", []string{"('running','running')", "('stopped','unknown')", "('stopped','running')"}, []string{"('stopped','stopped')", "('not_installed','not_installed')"}},
		{"gpu_leases", "GPU_LEASES", "state text NOT NULL", []string{"('held')", "('recovery_required')"}, []string{"('released')"}},
		{"deployments", "INFERENCE", "desired_state text NOT NULL,observed_state text NOT NULL", []string{"('running','running')", "('stopped','loading')", "('stopped','unknown')", "('stopped','failed')"}, []string{"('stopped','stopped')", "('removed','not_installed')"}},
		{"gateway_requests", "GATEWAY_REQUESTS", "state text NOT NULL,capacity_held boolean NOT NULL", []string{"('admitted',true)", "('reported',true)"}, []string{"('reported',false)", "('reconciled',false)"}},
		{"training_jobs", "TRAINING_JOBS", "state text NOT NULL", []string{"('queued')", "('preparing')", "('running')", "('cancelling')", "('recovery_required')"}, []string{"('succeeded')", "('failed')", "('cancelled')"}},
		{"training_attempts", "TRAINING_ATTEMPTS", "state text NOT NULL,physical_stopped_at timestamptz", []string{"('pending',NULL)", "('preparing',NULL)", "('running',NULL)", "('stopping',NULL)", "('recovery_required',NULL)"}, []string{"('stopped',now())", "('succeeded',now())", "('failed',now())", "('cancelled',now())"}},
		{"training_uploads", "TRAINING_UPLOADS", "state text NOT NULL", []string{"('uploading')"}, []string{"('accepted')", "('failed')"}},
		{"model_preparations", "PREPARATIONS", "state text NOT NULL", []string{"('queued')", "('preparing')", "('running')", "('cancelling')", "('recovery_required')"}, []string{"('quality_failed')", "('awaiting_review')", "('approved')", "('rejected')", "('failed')", "('cancelled')"}},
		{"model_preparation_attempts", "PREPARATION_ATTEMPTS", "state text NOT NULL,physical_stopped_at timestamptz", []string{"('pending',NULL)", "('preparing',NULL)", "('running',NULL)", "('stopping',NULL)", "('recovery_required',NULL)"}, []string{"('stopped',now())", "('prepared',now())", "('failed',now())", "('cancelled',now())"}},
		{"model_preparation_uploads", "PREPARATION_UPLOADS", "state text NOT NULL", []string{"('uploading')", "('verifying')"}, []string{"('accepted')", "('failed')"}},
		{"workflow_runs", "WORKFLOWS", "state text NOT NULL,lease_id uuid", []string{"('pending',NULL)", "('running',NULL)", "('interrupted','11111111-1111-4111-8111-111111111111')"}, []string{"('succeeded',NULL)", "('failed',NULL)", "('cancelled',NULL)", "('interrupted',NULL)"}},
		{"workflow_node_traces", "WORKFLOW_TRACES", "state text NOT NULL", []string{"('running')"}, []string{"('not_started')", "('succeeded')", "('failed')", "('cancelled')", "('interrupted')"}},
		{"model_imports", "MODEL_IMPORTS", "state text NOT NULL", []string{"('uploading')", "('queued')", "('verifying')", "('failed')"}, []string{"('ready')", "('cancelled')"}},
	}
	for _, v := range cases {
		if _, e := pool.Exec(ctx, "CREATE TABLE public."+v.table+" ("+v.columns+")"); e != nil {
			t.Fatal("owned explicit SQL predicate fixture", e)
		}
	}
	lease := actualMaintenanceLease(t, ctx, pool)
	if _, e := actualMaintenanceCheck(t, ctx, lease, pgx.ReadOnly, ""); e == nil || e.Error() != "POSTGRES_MAINTENANCE_SCHEMA_INCOMPATIBLE" {
		t.Fatal("predicate fixture qualified as complete catalog", e)
	}
	check := func() error {
		tx, e := lease.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
		if e != nil {
			t.Fatal(e)
		}
		result := checkRemoteWorkWithin(ctx, transaction.PostgreSQLTransaction(tx))
		if tx.Rollback(ctx) != nil {
			t.Fatal("predicate transaction close")
		}
		return result
	}
	if e := check(); e != nil {
		t.Fatal("empty predicate fixture", e)
	}
	for _, v := range cases {
		t.Run(v.kind, func(t *testing.T) {
			for _, group := range []struct {
				values  []string
				blocked bool
			}{{v.blocked, true}, {v.clear, false}} {
				for _, values := range group.values {
					if _, e := pool.Exec(ctx, "INSERT INTO public."+v.table+" VALUES "+values); e != nil {
						t.Fatal("explicit synthetic predicate row", e)
					}
					e := check()
					if group.blocked && (e == nil || e.Error() != "POSTGRES_MAINTENANCE_"+v.kind+"_NOT_QUIESCENT") || !group.blocked && e != nil {
						t.Fatal("actual SQL predicate classification", v.kind, values, e)
					}
					if _, e := pool.Exec(ctx, "DELETE FROM public."+v.table); e != nil {
						t.Fatal("exact owned predicate row cleanup", e)
					}
				}
			}
		})
	}
	t.Log("Real PostgreSQL execution against explicit minimal state-column fixtures only; all14 predicates tested separately. These fixture tables deliberately fail the complete production schema gate.")
}
