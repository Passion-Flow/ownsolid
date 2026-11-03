//go:build repository_integration && postgres_maintenance_integration

package postgresmaintenance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Uses the original privately marked empty native fixture and original held
// session. It proves current new-lineage SQL admission, not a stopped serving
// graph, populated stores, native pg_dump or a fresh restored runtime.
func TestActualMVPMaintenanceFullLineageAndOriginalReadOnlySession(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	pool := actualMaintenancePool(t, "full")
	if e := database.Migrate(ctx, pool, MVPApplicationProfile); e != nil {
		t.Fatal("actual MVP forward lineage", e)
	}
	lease := actualMaintenanceLease(t, ctx, pool)
	tenant, workspace, user := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO public.tenants(id,name,status) VALUES($1,'Synthetic MVP maintenance tenant','active')`, []any{tenant}},
		{`INSERT INTO public.workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic MVP Workspace','active')`, []any{workspace, tenant}},
		{`INSERT INTO public.users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,'mvp-maintenance@example.invalid','mvp-maintenance@example.invalid','Synthetic MVP actor','en','active',true)`, []any{user}},
		{`UPDATE public.installation_state SET bootstrap_complete=true,tenant_id=$1,default_workspace_id=$2,primary_admin_user_id=$3,bootstrapped_at=clock_timestamp() WHERE id=1`, []any{tenant, workspace, user}},
	} {
		if _, e := pool.Exec(ctx, q.sql, q.args...); e != nil {
			t.Fatal(e)
		}
	}
	check := func(mode pgx.TxAccessMode, profile string) (Observation, error) {
		tx, e := lease.BeginTx(ctx, pgx.TxOptions{AccessMode: mode})
		if e != nil {
			t.Fatal(e)
		}
		out, result := CheckWithin(ctx, transaction.PostgreSQLTransaction(tx), profile)
		if e = tx.Rollback(ctx); e != nil {
			t.Fatal(e)
		}
		return out, result
	}
	out, e := check(pgx.ReadOnly, MVPApplicationProfile)
	if e != nil || out.SchemaVersion != database.MVPPostgreSQLCPUV1SchemaVersion || out.TenantID != tenant || out.ApplicationProfile != MVPApplicationProfile || !out.CurrentLockHeld || len(out.Checks) != len(checkKinds)+len(mvpCheckKinds) {
		t.Fatal("actual full MVP quiescence", out, e)
	}
	var original int
	if e = lease.QueryRow(ctx, `SELECT pg_catalog.pg_backend_pid()`).Scan(&original); e != nil || original != out.BackendPID {
		t.Fatal("original held session changed", e)
	}
	if _, e = check(pgx.ReadWrite, MVPApplicationProfile); e == nil || e.Error() != "POSTGRES_MAINTENANCE_LOCKED_READ_ONLY_SESSION_REQUIRED" {
		t.Fatal("read-write caller admitted", e)
	}
	if _, e = check(pgx.ReadOnly, ApplicationProfile); e == nil || e.Error() != "POSTGRES_MAINTENANCE_SCHEMA_INCOMPATIBLE" {
		t.Fatal("new lineage relabeled as legacy releases22", e)
	}
	var tables int
	if e = lease.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_tables WHERE schemaname='public'`).Scan(&tables); e != nil || tables != database.MVPPostgreSQLCPUV1TableCount {
		t.Fatal("actual new native full catalog", tables, e)
	}
	t.Logf("actual MVP native version=%d tables=%d checks=%d original_session=%d", out.SchemaVersion, tables, len(out.Checks), original)
}
