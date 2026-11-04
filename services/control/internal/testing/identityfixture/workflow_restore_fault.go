package identityfixture

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
)

// ArmLateWorkflowRestoreFailure is a real PostgreSQL fault in an already owned
// synthetic schema. The users statement trigger arms the workflow statement
// trigger only AFTER the operator's exact catalog precheck, inside its transaction.
// Thus earlier owners really execute, and the late server failure rolls back
// their rows AND the newly armed workflow DDL. No product hook is installed.
func ArmLateWorkflowRestoreFailure(t *testing.T, pool *pgxpool.Pool) func() {
	return armLateWorkflowRestoreFailure(t, pool, "users")
}

// Localization also fingerprints users. Arm on the next account-owned statement
// so its strict catalog still passes and all real authority writes roll back.
func ArmLateLocalizationRestoreFailure(t *testing.T, pool *pgxpool.Pool) func() {
	clear := armLateWorkflowRestoreFailure(t, pool, "sessions")
	if e := database.Check(t.Context(), pool); e != nil {
		t.Fatal("fault must retain exact prechecked catalog", e)
	}
	return clear
}
func armLateWorkflowRestoreFailure(t *testing.T, pool *pgxpool.Pool, armTable string) func() {
	t.Helper()
	c := pool.Config().ConnConfig
	schema := c.RuntimeParams["search_path"]
	if c.Host != "127.0.0.1" || !strings.HasSuffix(c.Database, "_test") || !isolatedSchema.MatchString(schema) && !nativeSourceSchema.MatchString(schema) {
		t.Fatal("exact synthetic workflow fault scope required")
	}
	ctx := t.Context()
	if armTable != "users" && armTable != "sessions" {
		t.Fatal("fixed fault table required")
	}
	arm := `CREATE FUNCTION m13_arm_restore_failure() RETURNS trigger LANGUAGE plpgsql AS $arm$
 BEGIN
  EXECUTE $ddl$CREATE FUNCTION m13_reject_restore() RETURNS trigger LANGUAGE plpgsql AS $body$BEGIN RAISE EXCEPTION 'M13_TEST_LATE_WORKFLOW_RESTORE' USING ERRCODE='23514'; END;$body$$ddl$;
  EXECUTE 'CREATE TRIGGER m13_reject_restore BEFORE UPDATE ON workflow_runs FOR EACH STATEMENT EXECUTE FUNCTION m13_reject_restore()';
  RETURN NULL;
 END;$arm$;
 CREATE TRIGGER m13_arm_restore BEFORE UPDATE ON ` + armTable + ` FOR EACH STATEMENT EXECUTE FUNCTION m13_arm_restore_failure()`
	if _, e := pool.Exec(ctx, arm); e != nil {
		t.Fatal("install exact late-owner fault", e)
	}
	var once sync.Once
	clear := func() {
		once.Do(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var absent bool
			if e := pool.QueryRow(cleanup, "SELECT to_regprocedure('m13_reject_restore()') IS NULL AND NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='workflow_runs'::regclass AND tgname='m13_reject_restore')").Scan(&absent); e != nil || !absent {
				t.Error("late workflow DDL escaped rollback", e)
				return
			}
			if _, e := pool.Exec(cleanup, "DROP TRIGGER m13_arm_restore ON "+armTable+"; DROP FUNCTION m13_arm_restore_failure()"); e != nil {
				t.Error("exact late fault cleanup", e)
			}
		})
	}
	t.Cleanup(clear)
	// Prove this is a real late workflow server error, rather than an invalid arm
	// statement or an early readiness refusal. This probe itself always rolls back.
	tx, e := pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	probe := "UPDATE users SET auth_epoch=auth_epoch"
	if armTable == "sessions" {
		probe = "UPDATE sessions SET revoked_at=revoked_at"
	}
	if _, e = tx.Exec(ctx, probe); e != nil {
		tx.Rollback(ctx)
		t.Fatal("arm inside actual transaction", e)
	}
	var armed bool
	if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='workflow_runs'::regclass AND tgname='m13_reject_restore')").Scan(&armed); e != nil || !armed {
		tx.Rollback(ctx)
		t.Fatal("late workflow fault not armed", e)
	}
	_, e = tx.Exec(ctx, "UPDATE workflow_runs SET state=state")
	var actual *pgconn.PgError
	if !errors.As(e, &actual) || actual.Code != "23514" || actual.Message != "M13_TEST_LATE_WORKFLOW_RESTORE" {
		tx.Rollback(ctx)
		t.Fatal("exact late workflow server error not observed")
	}
	if e = tx.Rollback(ctx); e != nil {
		t.Fatal("late fault probe rollback", e)
	}
	return clear
}
