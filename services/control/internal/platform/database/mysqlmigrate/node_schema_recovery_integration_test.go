//go:build mysql_integration

package mysqlmigrate

import (
	"context"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// Cancellation occurs at real migration observer boundaries. These tests do not
// represent process death, lost COMMIT acknowledgement or native qualification.
func nodeSchemaInterrupted(t *testing.T, step int, stage string) {
	t.Helper()
	s, c, _ := actualMigration(t)
	prefix := c
	prefix.steps = c.steps[:41]
	migrationRun(t, s, prefix, Apply)
	b := nodeSchemaSeedOld(t, s)
	before, deployment := nodeSchemaOldDigest(t, s), licenseSchemaDeployment(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r, e := Run(ctx, s, c, testOptions(), Apply, func(event Event) {
		if event.Step == step && event.Stage == stage {
			cancel()
		}
	})
	if e == nil || r.Ready || r.DirtyStep != step || !r.LockReleased {
		t.Fatal("interrupted node DDL must retain exact dirty step and release lock", r, e)
	}
	var state string
	if e = s.Pool().QueryRowContext(t.Context(), `SELECT state FROM schema_migrations WHERE step=?`, step).Scan(&state); e != nil || state != Applying {
		t.Fatal("interrupted node journal not applying", mysqlstore.SafeError(e))
	}
	if _, e = Run(t.Context(), s, c, testOptions(), Apply, nil); e == nil || e.Error() != CodeReconciliationRequired {
		t.Fatal("dirty node Apply was silently repeated", e)
	}
	if r, e = Run(t.Context(), s, c, testOptions(), Readiness, nil); e == nil || r.Ready || !r.LockReleased {
		t.Fatal("dirty node schema served ready or retained lock", r, e)
	}
	migrationRun(t, s, c, Reconcile)
	if nodeSchemaOldDigest(t, s) != before || licenseSchemaDeployment(t, s) != deployment {
		t.Fatal("explicit node reconciliation changed prior business,journal or deployment identity")
	}
	nodeSchemaSeedRows(t, s, b)
	nodeSchemaAssertRows(t, s)
	migrationRun(t, s, c, Readiness)
}

func TestActualMySQLNodeSchemaBeforeNodes(t *testing.T) { nodeSchemaInterrupted(t, 42, Applying) }
func TestActualMySQLNodeSchemaAfterNodes(t *testing.T)  { nodeSchemaInterrupted(t, 42, Verified) }
func TestActualMySQLNodeSchemaBeforeJoin(t *testing.T)  { nodeSchemaInterrupted(t, 43, Applying) }
func TestActualMySQLNodeSchemaAfterJoin(t *testing.T)   { nodeSchemaInterrupted(t, 43, Verified) }
func TestActualMySQLNodeSchemaBeforeTasks(t *testing.T) { nodeSchemaInterrupted(t, 44, Applying) }
func TestActualMySQLNodeSchemaAfterTasks(t *testing.T)  { nodeSchemaInterrupted(t, 44, Verified) }
func TestActualMySQLNodeSchemaBeforeDerivedGuard(t *testing.T) {
	nodeSchemaInterrupted(t, 45, Applying)
}
func TestActualMySQLNodeSchemaAfterDerivedGuard(t *testing.T) { nodeSchemaInterrupted(t, 45, Verified) }

func nodeSchemaRefusesModes(t *testing.T, s *mysqlstore.Store, c Catalog, code string) {
	t.Helper()
	before, deployment := nodeSchemaOldDigest(t, s), licenseSchemaDeployment(t, s)
	for _, mode := range []Mode{Readiness, Apply, Reconcile} {
		r, e := Run(t.Context(), s, c, testOptions(), mode, nil)
		if e == nil || e.Error() != code || r.Ready || !r.LockReleased || nodeSchemaOldDigest(t, s) != before || licenseSchemaDeployment(t, s) != deployment {
			t.Fatal("unsafe node catalog mode changed prior state or accepted drift", mode, r, e)
		}
	}
}

func TestActualMySQLNodeSchemaUnjournaledDerivedGuard(t *testing.T) {
	s, c, _ := actualMigration(t)
	prefix := c
	prefix.steps = c.steps[:44]
	migrationRun(t, s, prefix, Apply)
	nodeSchemaSeedOld(t, s)
	migrationExec(t, s, c.steps[44].sql)
	nodeSchemaRefusesModes(t, s, c, CodeUnjournaledObject)
	var count int
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema=DATABASE() AND trigger_name='node_derived_inference_immutable'`).Scan(&count); e != nil || count != 1 {
		t.Fatal("unjournaled node guard was changed", mysqlstore.SafeError(e))
	}
}

func TestActualMySQLNodeSchemaMissingAppliedTasks(t *testing.T) {
	s, c, _ := actualMigration(t)
	migrationRun(t, s, c, Apply)
	nodeSchemaSeedOld(t, s)
	migrationExec(t, s, `DROP TABLE node_tasks`)
	nodeSchemaRefusesModes(t, s, c, CodeDefinitionDrift)
	var count int
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='node_tasks'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("missing applied node task table was silently recreated", mysqlstore.SafeError(e))
	}
}

func TestActualMySQLNodeSchemaWrongDefinition(t *testing.T) {
	s, c, _ := actualMigration(t)
	migrationRun(t, s, c, Apply)
	b := nodeSchemaSeedOld(t, s)
	nodeSchemaSeedRows(t, s, b)
	before := nodeSchemaDataDigest(t, s)
	migrationExec(t, s, `ALTER TABLE managed_nodes ADD INDEX unexpected_node_index(last_heartbeat_at)`)
	nodeSchemaRefusesModes(t, s, c, CodeDefinitionDrift)
	if nodeSchemaDataDigest(t, s) != before {
		t.Fatal("wrong-definition refusal changed node business data")
	}
}

func TestActualMySQLNodeSchemaWrongTriggerContext(t *testing.T) {
	s, c, _ := actualMigration(t)
	migrationRun(t, s, c, Apply)
	b := nodeSchemaSeedOld(t, s)
	nodeSchemaSeedRows(t, s, b)
	before := nodeSchemaDataDigest(t, s)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	connection, e := s.Pool().Conn(ctx)
	if e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	defer connection.Close()
	if _, e = connection.ExecContext(ctx, `DROP TRIGGER node_derived_inference_immutable`); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	if _, e = connection.ExecContext(ctx, `SET SESSION sql_mode=''`); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	_, created := connection.ExecContext(ctx, c.steps[44].sql)
	// Restore the pooled session even if fixture creation fails. Stored trigger
	// metadata retains the intentionally wrong creation context.
	cleanup, finish := context.WithTimeout(context.Background(), 5*time.Second)
	_, restored := connection.ExecContext(cleanup, `SET SESSION sql_mode=?`, mysqlstore.SessionSQLModes)
	finish()
	if restored != nil {
		discard(connection)
	}
	if created != nil || restored != nil {
		t.Fatal("actual node trigger mode drift setup", mysqlstore.SafeError(created), mysqlstore.SafeError(restored))
	}
	if e = connection.Close(); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	nodeSchemaRefusesModes(t, s, c, CodeMetadataInvalid)
	if nodeSchemaDataDigest(t, s) != before {
		t.Fatal("stored-mode refusal changed node data")
	}
}

func TestActualMySQLNodeSchemaPrepareDefinerDrift(t *testing.T) {
	s, c, _ := actualMigration(t)
	migrationRun(t, s, c, Apply)
	b := nodeSchemaSeedOld(t, s)
	nodeSchemaSeedRows(t, s, b)
	t.Log("MYSQL_NODE_SCHEMA_DATA_SHA256=" + nodeSchemaDataDigest(t, s))
}

func TestActualMySQLNodeSchemaDefinerDriftReadback(t *testing.T) {
	s, c, _ := actualMigration(t)
	before := nodeSchemaDataDigest(t, s)
	nodeSchemaRefusesModes(t, s, c, CodeTriggerContextInvalid)
	nodeSchemaAssertRows(t, s)
	if nodeSchemaDataDigest(t, s) != before {
		t.Fatal("definer refusal changed node data")
	}
	t.Log("MYSQL_NODE_SCHEMA_DATA_SHA256=" + before)
}
