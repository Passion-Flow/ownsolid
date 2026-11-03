//go:build mysql_integration

package mysqlmigrate

import (
	"context"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// Cancel at actual applying/verified journal boundaries. This does not claim
// process death, power loss or an uncertain COMMIT acknowledgement.
func holdoutSchemaInterrupted(t *testing.T, step int, stage string) {
	t.Helper()
	s, c, _ := actualMigration(t)
	prefix := c
	prefix.steps = c.steps[:85]
	migrationRun(t, s, prefix, Apply)
	r := holdoutSchemaReservationSeed(t, s)
	before, identity := holdoutSchemaPrefixDigest(t, s), licenseSchemaDeployment(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result, e := Run(ctx, s, c, testOptions(), Apply, func(event Event) {
		if event.Step == step && event.Stage == stage {
			cancel()
		}
	})
	if e == nil || result.Ready || result.DirtyStep != step || !result.LockReleased {
		t.Fatal("interrupted held-out migration lost dirty journal/lock", result, e)
	}
	var state string
	if e = s.Pool().QueryRowContext(t.Context(), `SELECT state FROM schema_migrations WHERE step=?`, step).Scan(&state); e != nil || state != Applying {
		t.Fatal("held-out dirty journal must remain applying", mysqlstore.SafeError(e))
	}
	if _, e = Run(t.Context(), s, c, testOptions(), Apply, nil); e == nil || e.Error() != CodeReconciliationRequired {
		t.Fatal("dirty held-out Apply retried implicitly", e)
	}
	if result, e = Run(t.Context(), s, c, testOptions(), Readiness, nil); e == nil || result.Ready || !result.LockReleased {
		t.Fatal("dirty held-out schema served ready", result, e)
	}
	if holdoutSchemaPrefixDigest(t, s) != before {
		t.Fatal("dirty refusal changed old85 graph/journal")
	}
	migrationRun(t, s, c, Reconcile)
	if holdoutSchemaPrefixDigest(t, s) != before || licenseSchemaDeployment(t, s) != identity {
		t.Fatal("reconciliation changed old85 graph/journal/identity")
	}
	holdoutSchemaAssertReservations(t, s)
	holdoutSchemaAssertRows(t, s, holdoutSchemaSeed(t, s, r))
	migrationRun(t, s, c, Readiness)
}

func holdoutSchemaRefuseModes(t *testing.T, s *mysqlstore.Store, c Catalog, code string, digest func() string) {
	t.Helper()
	before, identity := digest(), licenseSchemaDeployment(t, s)
	for _, mode := range []Mode{Readiness, Apply, Reconcile} {
		result, e := Run(t.Context(), s, c, testOptions(), mode, nil)
		if e == nil || e.Error() != code || result.Ready || !result.LockReleased || digest() != before || licenseSchemaDeployment(t, s) != identity {
			t.Fatal("held-out drift accepted or altered state", mode, result, e)
		}
	}
}

func TestActualMySQLHoldoutSchemaUnjournaledHoldout(t *testing.T) {
	s, c, _ := actualMigration(t)
	prefix := c
	prefix.steps = c.steps[:85]
	migrationRun(t, s, prefix, Apply)
	holdoutSchemaReservationSeed(t, s)
	migrationExec(t, s, c.steps[85].sql)
	holdoutSchemaRefuseModes(t, s, c, CodeUnjournaledObject, func() string { return holdoutSchemaPrefixDigest(t, s) })
}

func TestActualMySQLHoldoutSchemaMissingAppliedPrompts(t *testing.T) {
	s, c, r := holdoutSchemaLayer(t, holdoutSchemaStepCount)
	// Delete only the explicitly disposable owned prompt leaf by dropping its
	// table. Fingerprint every remaining business table and journal row.
	migrationExec(t, s, `DROP TABLE model_holdout_prompts`)
	digest := func() string {
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('model_holdouts','model_holdout_sources')`).Scan(&count); e != nil || count != 2 {
			t.Fatal("drift lost another held-out table")
		}
		parents := trainingSchemaColumnDigest(t, s, "model_holdouts", holdoutSchemaDigestColumns(t, s, "model_holdouts"))
		sources := trainingSchemaColumnDigest(t, s, "model_holdout_sources", []string{"tenant_id", "workspace_id", "holdout_id", "HEX(source_sha256)"})
		return hash(holdoutSchemaPrefixDigest(t, s) + parents + sources + licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(step,source_sha256,object_name,object_sha256,state,started_at,applied_at) FROM schema_migrations WHERE step>=86 ORDER BY step`))
	}
	holdoutSchemaRefuseModes(t, s, c, CodeDefinitionDrift, digest)
	var remaining int
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM model_holdouts WHERE id=?`, r.holdout).Scan(&remaining); e != nil || remaining != 1 {
		t.Fatal("missing prompt drift removed retained parent")
	}
}

func holdoutSchemaDigestColumns(t *testing.T, s *mysqlstore.Store, table string) []string {
	t.Helper()
	if !identifier.MatchString(table) {
		t.Fatal("owned digest table required")
	}
	rows, e := s.Pool().QueryContext(t.Context(), `SELECT column_name,data_type FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? ORDER BY ordinal_position`, table)
	if e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var name, kind string
		if e = rows.Scan(&name, &kind); e != nil || !identifier.MatchString(name) {
			t.Fatal("owned digest column", mysqlstore.SafeError(e))
		}
		q := "`" + name + "`"
		if strings.Contains(kind, "binary") || strings.Contains(kind, "blob") {
			q = "HEX(" + q + ")"
		}
		columns = append(columns, q)
	}
	if rows.Err() != nil || len(columns) == 0 {
		t.Fatal("complete owned digest columns required")
	}
	return columns
}

func TestActualMySQLHoldoutSchemaWrongDefinition(t *testing.T) {
	s, c, _ := holdoutSchemaLayer(t, holdoutSchemaStepCount)
	migrationExec(t, s, `ALTER TABLE model_holdouts ADD INDEX unexpected_holdout_index(created_at)`)
	holdoutSchemaRefuseModes(t, s, c, CodeDefinitionDrift, func() string { return holdoutSchemaDataDigest(t, s) })
}
func TestActualMySQLHoldoutSchemaWrongSourceChecksum(t *testing.T) {
	s, c, _ := holdoutSchemaLayer(t, holdoutSchemaStepCount)
	migrationExec(t, s, `UPDATE schema_migrations SET source_sha256=REPEAT('b',64) WHERE step=86`)
	holdoutSchemaRefuseModes(t, s, c, CodeChecksumMismatch, func() string { return holdoutSchemaDataDigest(t, s) })
}
func TestActualMySQLHoldoutSchemaWrongTrigger(t *testing.T) {
	s, c, _ := holdoutSchemaLayer(t, holdoutSchemaStepCount)
	migrationExec(t, s, `DROP TRIGGER model_holdout_prompt_immutable_update`)
	migrationExec(t, s, "CREATE TRIGGER model_holdout_prompt_immutable_update BEFORE UPDATE ON model_holdout_prompts FOR EACH ROW\nBEGIN\n    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='SYNTHETIC_CHANGED_TRIGGER';\nEND;")
	holdoutSchemaRefuseModes(t, s, c, CodeDefinitionDrift, func() string { return holdoutSchemaDataDigest(t, s) })
}

func TestActualMySQLHoldoutSchemaPrepareRecovery(t *testing.T) {
	s, c, _ := actualMigration(t)
	migrationRun(t, s, c, Apply)
	r := holdoutSchemaReservationSeed(t, s)
	holdoutSchemaAssertRows(t, s, holdoutSchemaSeed(t, s, r))
	holdoutSchemaAssertReservations(t, s)
	migrationRun(t, s, c, Readiness)
	t.Log("MYSQL_HOLDOUT_DATA_SHA256=" + holdoutSchemaDataDigest(t, s))
}
func TestActualMySQLHoldoutSchemaReadback(t *testing.T) {
	s, c, _ := actualMigration(t)
	migrationRun(t, s, c, Readiness)
	for _, table := range holdoutSchemaTables {
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); e != nil || count != 1 {
			t.Fatal("restored complete held-out graph", table, mysqlstore.SafeError(e))
		}
	}
	for _, table := range trainingSchemaTables {
		want := 1
		if table == "training_uploads" {
			want = 2
		}
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); e != nil || count != want {
			t.Fatal("restored complete training graph", table, mysqlstore.SafeError(e))
		}
	}
	holdoutSchemaAssertReservations(t, s)
	var r holdoutSchemaRows
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT id,tenant_id,workspace_id,created_at FROM model_holdouts`).Scan(&r.holdout, &r.tenant, &r.workspace, &r.heartbeat); e != nil {
		t.Fatal("restored held-out identity", mysqlstore.SafeError(e))
	}
	holdoutSchemaAssertRows(t, s, r)
	holdoutSchemaRefuses(t, s, 1644, `UPDATE model_holdouts SET reason='Synthetic forbidden change' WHERE id=?`, r.holdout)
	for _, item := range []struct{ table, column string }{{"model_holdout_sources", "source_sha256"}, {"model_holdout_prompts", "prompt_sha256"}} {
		holdoutSchemaRefuses(t, s, 1644, "UPDATE "+item.table+" SET "+item.column+"=REPEAT('z',32) WHERE holdout_id=?", r.holdout)
	}
	t.Log("MYSQL_HOLDOUT_DATA_SHA256=" + holdoutSchemaDataDigest(t, s))
}

func TestActualMySQLHoldoutSchemaBefore86(t *testing.T) { holdoutSchemaInterrupted(t, 86, Applying) }

func TestActualMySQLHoldoutSchemaAfter86(t *testing.T) { holdoutSchemaInterrupted(t, 86, Verified) }

func TestActualMySQLHoldoutSchemaBefore87(t *testing.T) { holdoutSchemaInterrupted(t, 87, Applying) }

func TestActualMySQLHoldoutSchemaAfter87(t *testing.T) { holdoutSchemaInterrupted(t, 87, Verified) }

func TestActualMySQLHoldoutSchemaBefore88(t *testing.T) { holdoutSchemaInterrupted(t, 88, Applying) }

func TestActualMySQLHoldoutSchemaAfter88(t *testing.T) { holdoutSchemaInterrupted(t, 88, Verified) }

func TestActualMySQLHoldoutSchemaBefore89(t *testing.T) { holdoutSchemaInterrupted(t, 89, Applying) }

func TestActualMySQLHoldoutSchemaAfter89(t *testing.T) { holdoutSchemaInterrupted(t, 89, Verified) }

func TestActualMySQLHoldoutSchemaBefore90(t *testing.T) { holdoutSchemaInterrupted(t, 90, Applying) }

func TestActualMySQLHoldoutSchemaAfter90(t *testing.T) { holdoutSchemaInterrupted(t, 90, Verified) }

func TestActualMySQLHoldoutSchemaBefore91(t *testing.T) { holdoutSchemaInterrupted(t, 91, Applying) }

func TestActualMySQLHoldoutSchemaAfter91(t *testing.T) { holdoutSchemaInterrupted(t, 91, Verified) }

func TestActualMySQLHoldoutSchemaBefore92(t *testing.T) { holdoutSchemaInterrupted(t, 92, Applying) }

func TestActualMySQLHoldoutSchemaAfter92(t *testing.T) { holdoutSchemaInterrupted(t, 92, Verified) }

func TestActualMySQLHoldoutSchemaBefore93(t *testing.T) { holdoutSchemaInterrupted(t, 93, Applying) }

func TestActualMySQLHoldoutSchemaAfter93(t *testing.T) { holdoutSchemaInterrupted(t, 93, Verified) }

func TestActualMySQLHoldoutSchemaBefore94(t *testing.T) { holdoutSchemaInterrupted(t, 94, Applying) }

func TestActualMySQLHoldoutSchemaAfter94(t *testing.T) { holdoutSchemaInterrupted(t, 94, Verified) }
