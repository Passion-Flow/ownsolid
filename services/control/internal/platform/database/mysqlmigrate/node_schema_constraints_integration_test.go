//go:build mysql_integration

package mysqlmigrate

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

func nodeSchemaReady(t *testing.T) (*mysqlstore.Store, Catalog, nodeSchemaRows) {
	t.Helper()
	s, c, _ := actualMigration(t)
	migrationRun(t, s, c, Apply)
	b := nodeSchemaSeedOld(t, s)
	return s, c, nodeSchemaSeedRows(t, s, b)
}
func nodeSchemaRefuses(t *testing.T, s *mysqlstore.Store, number uint16, query string, args ...any) {
	t.Helper()
	before := nodeSchemaDataDigest(t, s)
	_, e := s.Pool().ExecContext(t.Context(), query, args...)
	var safe *mysqlstore.Error
	if !errors.As(mysqlstore.SafeError(e), &safe) || safe.Number != number {
		t.Fatal("actual node schema refusal category", mysqlstore.SafeError(e))
	}
	if nodeSchemaDataDigest(t, s) != before {
		t.Fatal("refused node schema write changed accepted rows")
	}
}

const nodeSchemaInsertNode = `INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at) VALUES(?,?,?,?,'active',?,'1.0',JSON_OBJECT(),UTC_TIMESTAMP(6))`

func TestActualMySQLNodeSchemaNodeAndJoinConstraints(t *testing.T) {
	s, c, r := nodeSchemaReady(t)
	credential := sha256.Sum256([]byte(uuid.NewString()))
	var originalCredential, originalToken []byte
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT credential_hash FROM managed_nodes WHERE id=?`, r.node).Scan(&originalCredential); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT token_hash FROM node_join_tokens WHERE id=?`, r.join).Scan(&originalToken); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	for _, item := range []struct {
		name, query string
		args        []any
		number      uint16
	}{
		{"node_uuid", nodeSchemaInsertNode, []any{"invalid", r.tenant, r.workspace, "Invalid UUID", credential[:]}, 3819},
		{"node_foreign_tenant", nodeSchemaInsertNode, []any{uuid.NewString(), uuid.NewString(), r.workspace, "Invalid tenant", credential[:]}, 1452},
		{"node_foreign_workspace", nodeSchemaInsertNode, []any{uuid.NewString(), r.tenant, uuid.NewString(), "Invalid Workspace", credential[:]}, 1452},
		{"duplicate_live_name", `UPDATE managed_nodes SET name=? WHERE id=?`, []any{"Node schema \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629", r.peer}, 1062},
		{"duplicate_credential", `UPDATE managed_nodes SET credential_hash=? WHERE id=?`, []any{originalCredential, r.peer}, 1062},
		{"credential_length", `UPDATE managed_nodes SET credential_hash=? WHERE id=?`, []any{[]byte{1}, r.peer}, 3819},
		{"active_missing_credential", `UPDATE managed_nodes SET credential_hash=NULL WHERE id=?`, []any{r.peer}, 3819},
		{"removed_retains_credential", `UPDATE managed_nodes SET status='removed' WHERE id=?`, []any{r.peer}, 3819},
		{"version_zero", `UPDATE managed_nodes SET version=0 WHERE id=?`, []any{r.peer}, 3819},
		{"version_overflow", `UPDATE managed_nodes SET version=9007199254740992 WHERE id=?`, []any{r.peer}, 3819},
		{"unsupported_profile", `UPDATE managed_nodes SET execution_profile='unsupported' WHERE id=?`, []any{r.peer}, 3819},
		{"capabilities_object", `UPDATE managed_nodes SET capabilities=JSON_OBJECT() WHERE id=?`, []any{r.peer}, 3819},
		{"capabilities_overflow", `UPDATE managed_nodes SET capabilities=JSON_ARRAY(1,2,3,4,5,6,7,8,9,10,11,12) WHERE id=?`, []any{r.peer}, 3819},
		{"capabilities_null", `UPDATE managed_nodes SET capabilities=NULL WHERE id=?`, []any{r.peer}, 1048},
		{"facts_null", `UPDATE managed_nodes SET facts=NULL WHERE id=?`, []any{r.peer}, 1048},
		{"facts_invalid_json", `UPDATE managed_nodes SET facts=? WHERE id=?`, []any{"{", r.peer}, 3140},
		{"node_name_overflow", `UPDATE managed_nodes SET name=? WHERE id=?`, []any{strings.Repeat("\u4e16", 101), r.peer}, 1406},
		{"join_uuid", `UPDATE node_join_tokens SET id='invalid' WHERE id=?`, []any{r.join}, 3819},
		{"join_token_length", `UPDATE node_join_tokens SET token_hash=? WHERE id=?`, []any{[]byte{1}, r.join}, 3819},
		{"join_token_null", `UPDATE node_join_tokens SET token_hash=NULL WHERE id=?`, []any{r.join}, 1048},
		{"join_duplicate_token", `INSERT INTO node_join_tokens(id,tenant_id,workspace_id,node_name,token_hash,created_by,expires_at) VALUES(?,?,?,'Duplicate token',?,?,TIMESTAMPADD(SECOND,60,UTC_TIMESTAMP(6)))`, []any{uuid.NewString(), r.tenant, r.workspace, originalToken, r.user}, 1062},
		{"join_missing_creator", `UPDATE node_join_tokens SET created_by=? WHERE id=?`, []any{uuid.NewString(), r.join}, 1452},
		{"join_missing_workspace", `UPDATE node_join_tokens SET node_id=NULL,workspace_id=? WHERE id=?`, []any{uuid.NewString(), r.join}, 1452},
		{"join_missing_node", `UPDATE node_join_tokens SET node_id=? WHERE id=?`, []any{uuid.NewString(), r.join}, 1452},
		{"join_unsupported_profile", `UPDATE node_join_tokens SET execution_profile='unsupported' WHERE id=?`, []any{r.join}, 3819},
		{"join_name_overflow", `UPDATE node_join_tokens SET node_name=? WHERE id=?`, []any{strings.Repeat("\u4e16", 101), r.join}, 1406},
		{"join_expiry_null", `UPDATE node_join_tokens SET expires_at=NULL WHERE id=?`, []any{r.join}, 1048},
	} {
		t.Run(item.name, func(t *testing.T) { nodeSchemaRefuses(t, s, item.number, item.query, item.args...) })
	}
	migrationRun(t, s, c, Readiness)
}

func TestActualMySQLNodeSchemaBinaryUnicodeAndRemovedNameReuse(t *testing.T) {
	s, c, r := nodeSchemaReady(t)
	for _, name := range []string{"node schema \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629", "Node schema \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629 ", strings.Repeat("\u4e16", 100)} {
		migrationExec(t, s, `UPDATE managed_nodes SET name=? WHERE id=?`, name, r.peer)
		var actual string
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT name FROM managed_nodes WHERE id=?`, r.peer).Scan(&actual); e != nil || actual != name {
			t.Fatal("actual binary/case/trailing/Unicode100 readback", mysqlstore.SafeError(e))
		}
	}
	name := "Removed name \u4e16\u754c"
	migrationExec(t, s, `UPDATE managed_nodes SET name=?,status='removed',credential_hash=NULL WHERE id=?`, name, r.peer)
	credential := sha256.Sum256([]byte(uuid.NewString()))
	migrationExec(t, s, nodeSchemaInsertNode, uuid.NewString(), r.tenant, r.workspace, name, credential[:])
	var count int
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM managed_nodes WHERE name=?`, name).Scan(&count); e != nil || count != 2 {
		t.Fatal("removed name was not reusable", mysqlstore.SafeError(e))
	}
	migrationRun(t, s, c, Readiness)
}

func TestActualMySQLNodeSchemaTaskConstraints(t *testing.T) {
	s, c, r := nodeSchemaReady(t)
	for _, item := range []struct {
		name, query string
		args        []any
		number      uint16
	}{
		{"task_uuid", `UPDATE node_tasks SET id='invalid' WHERE id=?`, []any{r.task}, 3819},
		{"task_idempotency_uuid", `UPDATE node_tasks SET idempotency_key='invalid' WHERE id=?`, []any{r.task}, 3819},
		{"task_operation", `UPDATE node_tasks SET operation='unsupported' WHERE id=?`, []any{r.task}, 3819},
		{"task_state", `UPDATE node_tasks SET state='unsupported' WHERE id=?`, []any{r.task}, 3819},
		{"attempt_negative", `UPDATE node_tasks SET attempt=-1 WHERE id=?`, []any{r.task}, 3819},
		{"attempt_overflow", `UPDATE node_tasks SET attempt=4 WHERE id=?`, []any{r.task}, 3819},
		{"running_without_lease", `UPDATE node_tasks SET state='running' WHERE id=?`, []any{r.task}, 3819},
		{"pending_with_active_lease", `UPDATE node_tasks SET lease_token_hash=?,lease_expires_at=TIMESTAMPADD(SECOND,30,UTC_TIMESTAMP(6)) WHERE id=?`, []any{make([]byte, 32), r.task}, 3819},
		{"terminal_without_completion", `UPDATE node_tasks SET state='succeeded' WHERE id=?`, []any{r.task}, 3819},
		{"pending_with_completion", `UPDATE node_tasks SET completed_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{r.task}, 3819},
		{"lease_hash_length", `UPDATE node_tasks SET lease_token_hash=? WHERE id=?`, []any{[]byte{1}, r.task}, 3819},
		{"completed_hash_length", `UPDATE node_tasks SET completed_lease_hash=? WHERE id=?`, []any{[]byte{1}, r.task}, 3819},
		{"result_hash_length", `UPDATE node_tasks SET result_hash=? WHERE id=?`, []any{[]byte{1}, r.task}, 3819},
		{"fingerprint_length", `UPDATE node_tasks SET request_fingerprint=? WHERE id=?`, []any{[]byte{1}, r.task}, 3819},
		{"payload_array", `UPDATE node_tasks SET payload=JSON_ARRAY() WHERE id=?`, []any{r.task}, 3819},
		{"payload_overflow", `UPDATE node_tasks SET payload=JSON_OBJECT('v',?) WHERE id=?`, []any{strings.Repeat("x", 32768), r.task}, 3819},
		{"payload_null", `UPDATE node_tasks SET payload=NULL WHERE id=?`, []any{r.task}, 1048},
		{"missing_requester", `UPDATE node_tasks SET requested_by=? WHERE id=?`, []any{uuid.NewString(), r.task}, 1452},
		{"missing_node", `UPDATE node_tasks SET node_id=? WHERE id=?`, []any{uuid.NewString(), r.task}, 1452},
		{"ordinary_delegation_epoch", `UPDATE node_tasks SET inference_requested_epoch=1 WHERE id=?`, []any{r.task}, 3819},
	} {
		t.Run(item.name, func(t *testing.T) { nodeSchemaRefuses(t, s, item.number, item.query, item.args...) })
	}
	freeNode := uuid.NewString()
	credential := sha256.Sum256([]byte(uuid.NewString()))
	migrationExec(t, s, nodeSchemaInsertNode, freeNode, r.tenant, r.workspace, "Free delegation node", credential[:])
	for _, item := range []struct {
		name  string
		epoch any
	}{{"derived_epoch_null", nil}, {"derived_epoch_zero", int64(0)}} {
		t.Run(item.name, func(t *testing.T) {
			nodeSchemaRefuses(t, s, 3819, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,inference_requested_epoch) VALUES(?,?,?,?,'inference.derived.v1','pending',?,?,UTC_TIMESTAMP(6),?)`, uuid.NewString(), r.tenant, r.workspace, freeNode, uuid.NewString(), r.user, item.epoch)
		})
	}
	t.Run("payload_exact_boundary", func(t *testing.T) {
		var overhead int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT OCTET_LENGTH(CAST(JSON_OBJECT('v','') AS CHAR CHARACTER SET utf8mb4))`).Scan(&overhead); e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		migrationExec(t, s, `UPDATE node_tasks SET payload=JSON_OBJECT('v',?) WHERE id=?`, strings.Repeat("x", 32768-overhead), r.task)
		var size int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT OCTET_LENGTH(CAST(payload AS CHAR CHARACTER SET utf8mb4)) FROM node_tasks WHERE id=?`, r.task).Scan(&size); e != nil || size != 32768 {
			t.Fatal("actual32768 JSON payload boundary", mysqlstore.SafeError(e))
		}
	})
	migrationRun(t, s, c, Readiness)
}

func TestActualMySQLNodeSchemaDerivedImmutability(t *testing.T) {
	s, c, r := nodeSchemaReady(t)
	for _, item := range []struct {
		name, set string
		args      []any
	}{
		{"id", "id=?", []any{uuid.NewString()}},
		{"tenant", "tenant_id=?", []any{uuid.NewString()}},
		{"workspace", "workspace_id=?", []any{uuid.NewString()}},
		{"node", "node_id=?", []any{r.node}},
		{"operation", "operation='collect_facts.v1'", nil},
		{"payload", "payload=JSON_OBJECT('changed',1)", nil},
		{"requester", "requested_by=?", []any{uuid.NewString()}},
		{"epoch", "inference_requested_epoch=2", nil},
		{"idempotency", "idempotency_key=?", []any{uuid.NewString()}},
		{"fingerprint", "request_fingerprint=?", []any{make([]byte, 32)}},
		{"creation", "created_at=TIMESTAMPADD(MICROSECOND,1,created_at)", nil},
	} {
		t.Run(item.name, func(t *testing.T) {
			args := append(append([]any{}, item.args...), r.derived)
			nodeSchemaRefuses(t, s, 1644, "UPDATE node_tasks SET "+item.set+" WHERE id=?", args...)
		})
	}
	t.Run("cannot_become_derived", func(t *testing.T) {
		nodeSchemaRefuses(t, s, 1644, `UPDATE node_tasks SET operation='inference.derived.v1',inference_requested_epoch=1 WHERE id=?`, r.task)
	})
	t.Run("valid_derived_lifecycle", func(t *testing.T) {
		before := licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(id,tenant_id,workspace_id,node_id,operation,payload,requested_by,inference_requested_epoch,idempotency_key,HEX(request_fingerprint),created_at) FROM node_tasks WHERE operation='inference.derived.v1'`)
		lease := sha256.Sum256([]byte(uuid.NewString()))
		migrationExec(t, s, `UPDATE node_tasks SET state='running',attempt=1,lease_token_hash=?,lease_expires_at=TIMESTAMPADD(SECOND,30,UTC_TIMESTAMP(6)) WHERE id=?`, lease[:], r.derived)
		migrationExec(t, s, `UPDATE node_tasks SET state='succeeded',completed_at=UTC_TIMESTAMP(6),completed_lease_hash=lease_token_hash,result_hash=?,lease_token_hash=NULL,lease_expires_at=NULL WHERE id=?`, lease[:], r.derived)
		after := licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(id,tenant_id,workspace_id,node_id,operation,payload,requested_by,inference_requested_epoch,idempotency_key,HEX(request_fingerprint),created_at) FROM node_tasks WHERE operation='inference.derived.v1'`)
		if before != after {
			t.Fatal("valid lifecycle changed immutable command")
		}
	})
	migrationRun(t, s, c, Readiness)
}

const nodeSchemaInsertTask = `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at) VALUES(?,?,?,?,'collect_facts.v1','pending',?,?,TIMESTAMPADD(SECOND,60,UTC_TIMESTAMP(6)))`

func TestActualMySQLNodeSchemaInflightAndIdempotency(t *testing.T) {
	s, c, r := nodeSchemaReady(t)
	nodeSchemaRefuses(t, s, 1062, nodeSchemaInsertTask, uuid.NewString(), r.tenant, r.workspace, r.node, uuid.NewString(), r.user)
	var key string
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT idempotency_key FROM node_tasks WHERE id=?`, r.task).Scan(&key); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	migrationExec(t, s, `UPDATE node_tasks SET state='succeeded',completed_at=UTC_TIMESTAMP(6) WHERE id=?`, r.task)
	nodeSchemaRefuses(t, s, 1062, nodeSchemaInsertTask, uuid.NewString(), r.tenant, r.workspace, r.node, key, r.user)
	migrationExec(t, s, nodeSchemaInsertTask, uuid.NewString(), r.tenant, r.workspace, r.node, uuid.NewString(), r.user)
	var count int
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM node_tasks WHERE node_id=?`, r.node).Scan(&count); e != nil || count != 2 {
		t.Fatal("terminal release/idempotency history invalid", mysqlstore.SafeError(e))
	}
	migrationRun(t, s, c, Readiness)
}
func TestActualMySQLNodeSchemaConcurrentInflight(t *testing.T) {
	s, c, r := nodeSchemaReady(t)
	migrationExec(t, s, `UPDATE node_tasks SET state='cancelled',completed_at=UTC_TIMESTAMP(6) WHERE id=?`, r.task)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	start, done := make(chan struct{}), make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, e := s.Pool().ExecContext(ctx, nodeSchemaInsertTask, uuid.NewString(), r.tenant, r.workspace, r.node, uuid.NewString(), r.user)
			done <- e
		}()
	}
	close(start)
	success, conflict := 0, 0
	for range 2 {
		select {
		case e := <-done:
			if e == nil {
				success++
				continue
			}
			var safe *mysqlstore.Error
			if !errors.As(mysqlstore.SafeError(e), &safe) || safe.Number != 1062 {
				t.Fatal("actual competing inflight outcome", mysqlstore.SafeError(e))
			}
			conflict++
		case <-ctx.Done():
			t.Fatal("actual inflight concurrency did not join")
		}
	}
	var count int
	if success != 1 || conflict != 1 {
		t.Fatal("actual inflight writes did not have exactly one winner")
	}
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM node_tasks WHERE node_id=? AND state IN('pending','running')`, r.node).Scan(&count); e != nil || count != 1 {
		t.Fatal("actual concurrent inflight integrity", mysqlstore.SafeError(e))
	}
	migrationRun(t, s, c, Readiness)
}
