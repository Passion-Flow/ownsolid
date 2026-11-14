//go:build mysql_integration

package mysqlmigrate

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func receiptIndexRows(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	rows, e := s.Pool().QueryContext(context.Background(), "SELECT JSON_ARRAY(id,DATE_FORMAT(occurred_at,'%Y-%m-%dT%H:%i:%s.%fZ'),actor_user_id,subject_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,HEX(identifier_digest),reason,metadata) FROM audit_events ORDER BY id")
	if e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	defer rows.Close()
	values := []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		values = append(values, append(json.RawMessage{}, raw...))
	}
	if rows.Err() != nil {
		t.Fatal(mysqlstore.SafeError(rows.Err()))
	}
	raw, _ := json.Marshal(values)
	return hash(string(raw))
}
func receiptIndexDefinition(t *testing.T, s *mysqlstore.Store) {
	t.Helper()
	rows, e := s.Pool().QueryContext(context.Background(), "SELECT seq_in_index,column_name,non_unique,index_type FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='audit_events' AND index_name='audit_operation_lookup_idx' ORDER BY seq_in_index")
	if e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	defer rows.Close()
	expected := []string{"request_id", "actor_user_id", "audience", "tenant_id"}
	count := 0
	for rows.Next() {
		var ordinal, nonunique int
		var column, kind string
		if rows.Scan(&ordinal, &column, &nonunique, &kind) != nil || count >= len(expected) || ordinal != count+1 || column != expected[count] || nonunique != 1 || kind != "BTREE" {
			t.Fatal("actual ordered nonunique index definition")
		}
		count++
	}
	if rows.Err() != nil || count != 4 {
		t.Fatal("actual index columns", mysqlstore.SafeError(rows.Err()))
	}
}
func receiptIndexWorkloadAndPlan(t *testing.T, s *mysqlstore.Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var user, tenant, workspace string
	if e := s.Pool().QueryRowContext(ctx, "SELECT primary_admin_user_id,tenant_id,default_workspace_id FROM installation_state WHERE id=1").Scan(&user, &tenant, &workspace); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	tx, e := s.Pool().BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	defer tx.Rollback()
	statement, e := tx.PrepareContext(ctx, "INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,'admin','workspace.updated','success','workspace',?,?)")
	if e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	defer statement.Close()
	request := ""
	for range 1000 {
		request = uuid.NewString()
		if _, e = statement.ExecContext(ctx, uuid.NewString(), user, tenant, workspace, workspace, request); e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	migrationExec(t, s, "ANALYZE TABLE audit_events")
	var raw []byte
	if e = s.Pool().QueryRowContext(ctx, `EXPLAIN FORMAT=JSON SELECT action,workspace_id,target_type,target_id FROM audit_events WHERE request_id=? AND actor_user_id=? AND audience=? AND tenant_id=? AND outcome='success' AND action IN ('workspace.created','workspace.updated','workspace.archived','workspace.restored','workspace.member_added','workspace.member_updated','workspace.member_removed','workspace.note_created','workspace.note_updated','workspace.note_deleted') LIMIT 2`, request, user, "admin", tenant).Scan(&raw); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	var plan any
	if json.Unmarshal(raw, &plan) != nil {
		t.Fatal("actual JSON planner output")
	}
	selected := false
	var visit func(any)
	visit = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if v["table_name"] == "audit_events" && v["key"] == "audit_operation_lookup_idx" && v["access_type"] != "ALL" {
				selected = true
			}
			for _, x := range v {
				visit(x)
			}
		case []any:
			for _, x := range v {
				visit(x)
			}
		}
	}
	visit(plan)
	if !selected {
		t.Fatal("actual receipt workload did not select owned index")
	}
	store, e := transaction.FromMySQL(s)
	if e != nil {
		t.Fatal(e)
	}
	accounts := &identity.Service{Storage: store}
	var issued identity.IssuedSession
	if e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = accounts.IssueSessionWithin(ctx, q, user, 1, "admin", "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	a, e := accounts.Authenticate(ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	service := &workspaces.Service{Identity: accounts}
	receipt, e := service.Operation(ctx, a, request)
	if e != nil || receipt.State != workspaces.OperationCommitted || receipt.Event == nil || receipt.Event.WorkspaceID != workspace {
		t.Fatal("actual indexed receipt read", receipt, e)
	}
	var count int
	if e = s.Pool().QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_events WHERE action='workspace.updated'").Scan(&count); e != nil || count != 1000 {
		t.Fatal("actual workload cardinality", count, mysqlstore.SafeError(e))
	}
	t.Log("MYSQL_RECEIPT_INDEX_WORKLOAD_AUDIT_ROWS=1000;actual planner selected audit_operation_lookup_idx;actual current-authority receipt matched;no latency or concurrency claim")
}
func receiptIndexPrefix(t *testing.T) (*mysqlstore.Store, Catalog) {
	t.Helper()
	s, c, _ := actualMigration(t)
	c.steps = c.steps[:36]
	prefix := c
	prefix.steps = c.steps[:35]
	migrationRun(t, s, prefix, Apply)
	seedEvolutionBusiness(t, s)
	return s, c
}
func TestActualMySQLReceiptIndexFreshAndPlan(t *testing.T) {
	s, c, _ := actualMigration(t)
	c.steps = c.steps[:36]
	migrationRun(t, s, c, Apply)
	seedEvolutionBusiness(t, s)
	receiptIndexDefinition(t, s)
	receiptIndexWorkloadAndPlan(t, s)
	before := evolutionBusinessDigest(t, s) + receiptIndexRows(t, s)
	events := 0
	report, e := Run(context.Background(), s, c, testOptions(), Apply, func(Event) { events++ })
	if e != nil || !report.Ready || events != 0 || before != evolutionBusinessDigest(t, s)+receiptIndexRows(t, s) {
		t.Fatal("actual indexed fresh idempotence", report, e)
	}
	migrationRun(t, s, c, Readiness)
}
func TestActualMySQLReceiptIndexUpgradePreservesBusiness(t *testing.T) {
	s, c := receiptIndexPrefix(t)
	before := evolutionBusinessDigest(t, s) + receiptIndexRows(t, s)
	migrationRun(t, s, c, Apply)
	receiptIndexDefinition(t, s)
	if before != evolutionBusinessDigest(t, s)+receiptIndexRows(t, s) {
		t.Fatal("35 to36 changed business/audit values")
	}
	migrationRun(t, s, c, Readiness)
}
func receiptIndexInterrupted(t *testing.T, stage string) {
	t.Helper()
	s, c := receiptIndexPrefix(t)
	before := evolutionBusinessDigest(t, s) + receiptIndexRows(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	report, e := Run(ctx, s, c, testOptions(), Apply, func(event Event) {
		if event.Step == 36 && event.Stage == stage {
			cancel()
		}
	})
	if e == nil || report.DirtyStep != 36 || !report.LockReleased {
		t.Fatal("index interruption journal/release", report, e)
	}
	if _, e = Run(context.Background(), s, c, testOptions(), Apply, nil); e == nil || e.Error() != CodeReconciliationRequired {
		t.Fatal("dirty index automatically replayed", e)
	}
	var columns int
	if e = s.Pool().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='audit_events' AND index_name='audit_operation_lookup_idx'").Scan(&columns); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	if stage == Applying && columns != 0 || stage == Verified && columns != 4 {
		t.Fatal("actual DDL stage did not match observed index", columns)
	}
	migrationRun(t, s, c, Reconcile)
	receiptIndexDefinition(t, s)
	if before != evolutionBusinessDigest(t, s)+receiptIndexRows(t, s) {
		t.Fatal("index recovery changed business/audit values")
	}
	migrationRun(t, s, c, Readiness)
}
func TestActualMySQLReceiptIndexBeforeDDL(t *testing.T) { receiptIndexInterrupted(t, Applying) }
func TestActualMySQLReceiptIndexAfterDDL(t *testing.T)  { receiptIndexInterrupted(t, Verified) }
func TestActualMySQLReceiptIndexWrongDefinitionRepair(t *testing.T) {
	s, c := receiptIndexPrefix(t)
	before := evolutionBusinessDigest(t, s) + receiptIndexRows(t, s)
	report, e := Run(context.Background(), s, c, testOptions(), Apply, func(event Event) {
		if event.Step == 36 && event.Stage == Applying {
			migrationExec(t, s, "ALTER TABLE audit_events ADD INDEX audit_operation_lookup_idx(actor_user_id,request_id,audience,tenant_id), ALGORITHM=INPLACE, LOCK=NONE")
		}
	})
	if e == nil || report.DirtyStep != 36 || !report.LockReleased {
		t.Fatal("wrong index was accepted", report, e)
	}
	if _, e = Run(context.Background(), s, c, testOptions(), Reconcile, nil); e == nil || e.Error() != CodeDefinitionDrift {
		t.Fatal("wrong definition silently reconciled")
	}
	// InnoDB can replace the implicit actor foreign-key index with this wrong
	// actor-leading index. Never disable foreign-key checks to repair the drift.
	_, e = s.Pool().ExecContext(context.Background(), "ALTER TABLE audit_events DROP INDEX audit_operation_lookup_idx, ALGORITHM=INPLACE, LOCK=NONE")
	vendor, typed := mysqlstore.SafeError(e).(*mysqlstore.Error)
	if !typed || vendor.Number != 1553 || string(vendor.SQLState[:]) != "HY000" {
		t.Fatal("foreign-key-support index removal must fail", mysqlstore.SafeError(e))
	}
	t.Logf("foreign-key index removal refused: vendor_number=%d sql_state=%s", vendor.Number, vendor.SQLState)
	var actorIndexes int
	if e = s.Pool().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='audit_events' AND index_name='actor_user_id'").Scan(&actorIndexes); e != nil || actorIndexes != 0 {
		t.Fatal("implicit actor index replacement", mysqlstore.SafeError(e))
	}
	// Rebuild the subsequent FK-support indexes in the same DDL to restore the
	// exact catalog order as well as each definition. Constraints remain enabled.
	migrationExec(t, s, "ALTER TABLE audit_events ADD INDEX actor_user_id(actor_user_id), DROP INDEX audit_operation_lookup_idx, DROP INDEX subject_user_id, DROP INDEX tenant_id, ADD INDEX subject_user_id(subject_user_id), ADD INDEX tenant_id(tenant_id,workspace_id), ALGORITHM=INPLACE, LOCK=NONE")
	// ALTER also normalizes the UUID CHECK literal charsets. Removing the index
	// does not restore the old SHOW CREATE checksum. Restore the exact target
	// using its owned statement; reconciliation must validate the complete target
	// definition before clearing the existing dirty journal. Never edit hashes.
	migrationExec(t, s, c.steps[len(c.steps)-1].sql)
	migrationRun(t, s, c, Reconcile)
	receiptIndexDefinition(t, s)
	if before != evolutionBusinessDigest(t, s)+receiptIndexRows(t, s) {
		t.Fatal("wrong-index repair changed business/audit values")
	}
	migrationRun(t, s, c, Readiness)
}
