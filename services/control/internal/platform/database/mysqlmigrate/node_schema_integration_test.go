//go:build mysql_integration

package mysqlmigrate

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// These are explicit synthetic schema rows,not actual agent facts,issued License
// artifacts or provider identities. DDL,SQL,TLS,foreign keys and triggers are real.
type nodeSchemaRows struct {
	tenant, user, workspace, node, peer, join, task, derived string
	heartbeat                                                time.Time
}

func nodeSchemaOldDigest(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	journal := licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(step,source_sha256,object_name,object_sha256,state,started_at,applied_at) FROM schema_migrations WHERE step<=41 ORDER BY step`)
	return hash(licenseSchemaDataDigest(t, s) + journal)
}
func nodeSchemaDataDigest(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	nodes := licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(id,tenant_id,workspace_id,name,status,version,HEX(credential_hash),protocol_version,facts,last_heartbeat_at,created_at,updated_at,execution_profile,capabilities,inference_fenced_at,live_name) FROM managed_nodes ORDER BY id`)
	joins := licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(id,tenant_id,workspace_id,node_name,HEX(token_hash),created_by,created_at,expires_at,consumed_at,revoked_at,execution_profile,node_id) FROM node_join_tokens ORDER BY id`)
	tasks := licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,attempt,HEX(lease_token_hash),lease_expires_at,error_code,HEX(completed_lease_hash),HEX(result_hash),created_at,updated_at,deadline_at,completed_at,payload,HEX(request_fingerprint),inference_requested_epoch,inflight_node_id) FROM node_tasks ORDER BY id`)
	return hash(nodeSchemaOldDigest(t, s) + nodes + joins + tasks)
}
func nodeSchemaSeedOld(t *testing.T, s *mysqlstore.Store) triggerBusiness {
	t.Helper()
	b := seedTriggerBusiness(t, s)
	finishTriggerFlow(t, s, b)
	migrationExec(t, s, triggerSessionInsert, triggerSessionArguments(b, uuid.NewString())...)
	licenseSchemaSeedArtifacts(t, s)
	return b
}
func nodeSchemaSeedRows(t *testing.T, s *mysqlstore.Store, b triggerBusiness) nodeSchemaRows {
	t.Helper()
	rows := nodeSchemaRows{tenant: b.tenant, user: b.user, node: uuid.NewString(), peer: uuid.NewString(), join: uuid.NewString(), task: uuid.NewString(), derived: uuid.NewString(), heartbeat: b.now}
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT default_workspace_id FROM installation_state WHERE id=1`).Scan(&rows.workspace); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	for index, id := range []string{rows.node, rows.peer} {
		credential := sha256.Sum256([]byte(uuid.NewString()))
		profile, protocol := "read_only", "1.0"
		if index == 1 {
			profile, protocol = wire.DockerModelLifecycleProfile, wire.ModelLifecycleVersion
		}
		migrationExec(t, s, `INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at,execution_profile) VALUES(?,?,?,?,'active',?,?,JSON_OBJECT('synthetic_schema',CAST('true' AS JSON),'ordinal',?),?,?)`, id, rows.tenant, rows.workspace, []string{"Node schema \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629", "Node schema derived"}[index], credential[:], protocol, index, rows.heartbeat, profile)
	}
	token := sha256.Sum256([]byte(uuid.NewString()))
	migrationExec(t, s, `INSERT INTO node_join_tokens(id,tenant_id,workspace_id,node_name,token_hash,created_by,created_at,expires_at,consumed_at,node_id) VALUES(?,?,?,'Schema consumed token',?,?,?,?,?,?)`, rows.join, rows.tenant, rows.workspace, token[:], rows.user, b.now, b.now.Add(time.Minute), b.now.Add(time.Second), rows.node)
	for index, id := range []string{rows.task, rows.derived} {
		node, operation, epoch := rows.node, "collect_facts.v1", any(nil)
		if index == 1 {
			node, operation, epoch = rows.peer, wire.DerivedInferenceOperation, int64(1)
		}
		fingerprint := sha256.Sum256([]byte(id))
		migrationExec(t, s, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,request_fingerprint,inference_requested_epoch) VALUES(?,?,?,?,?,'pending',?,?,?,JSON_OBJECT('synthetic_schema',CAST('true' AS JSON)),?,?)`, id, rows.tenant, rows.workspace, node, operation, uuid.NewString(), rows.user, b.now.Add(time.Hour), fingerprint[:], epoch)
	}
	return rows
}
func nodeSchemaAssertRows(t *testing.T, s *mysqlstore.Store) {
	t.Helper()
	for table, want := range map[string]int{"managed_nodes": 2, "node_join_tokens": 1, "node_tasks": 2, "platform_license_artifacts": 2} {
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); e != nil || count != want {
			t.Fatal("actual node schema retained row count", table, mysqlstore.SafeError(e))
		}
	}
	var name, profile, live string
	var capabilities, facts, credential []byte
	var heartbeat time.Time
	e := s.Pool().QueryRowContext(t.Context(), `SELECT name,execution_profile,capabilities,facts,credential_hash,last_heartbeat_at,live_name FROM managed_nodes WHERE execution_profile='read_only'`).Scan(&name, &profile, &capabilities, &facts, &credential, &heartbeat, &live)
	var allowed []string
	var observed map[string]any
	if e != nil || json.Unmarshal(capabilities, &allowed) != nil || len(allowed) != 1 || allowed[0] != "collect_facts.v1" || json.Unmarshal(facts, &observed) != nil || observed["synthetic_schema"] != true || name != "Node schema \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629" || live != name || profile != "read_only" || len(credential) != sha256.Size || heartbeat.Nanosecond() != 123456000 || heartbeat.Location() != time.UTC {
		t.Fatal("actual default/generated/JSON/Unicode/binary/UTC microsecond readback", mysqlstore.SafeError(e))
	}
	before := nodeSchemaDataDigest(t, s)
	_, e = s.Pool().ExecContext(t.Context(), `UPDATE node_tasks SET payload=JSON_OBJECT('changed',true) WHERE operation='inference.derived.v1'`)
	var safe *mysqlstore.Error
	if !errors.As(mysqlstore.SafeError(e), &safe) || safe.Number != 1644 || nodeSchemaDataDigest(t, s) != before {
		t.Fatal("actual restored derived command guard missing or changed data", mysqlstore.SafeError(e))
	}
}
func TestActualMySQLNodeSchemaFresh(t *testing.T) {
	s, c, configuration := actualMigration(t)
	migrationRun(t, s, c, Apply)
	b := nodeSchemaSeedOld(t, s)
	nodeSchemaSeedRows(t, s, b)
	nodeSchemaAssertRows(t, s)
	before := nodeSchemaDataDigest(t, s)
	events := 0
	r, e := Run(t.Context(), s, c, testOptions(), Apply, func(Event) { events++ })
	if e != nil || !r.Ready || events != 0 || nodeSchemaDataDigest(t, s) != before {
		t.Fatal("fresh45 repeat changed node/business rows", e)
	}
	reopened, observation, e := mysqlstore.Open(t.Context(), configuration)
	if e != nil || !observation.TLSAuthenticated {
		t.Fatal("independent actual verified TLS node schema reopen", e)
	}
	defer reopened.Close()
	migrationRun(t, reopened, c, Readiness)
	nodeSchemaAssertRows(t, reopened)
	if nodeSchemaDataDigest(t, reopened) != before {
		t.Fatal("independent reopen lost node/business data")
	}
}
func TestActualMySQLNodeSchemaUpgrade(t *testing.T) {
	s, c, _ := actualMigration(t)
	prefix := c
	prefix.steps = c.steps[:41]
	migrationRun(t, s, prefix, Apply)
	b := nodeSchemaSeedOld(t, s)
	before := nodeSchemaOldDigest(t, s)
	migrationRun(t, s, c, Apply)
	if nodeSchemaOldDigest(t, s) != before {
		t.Fatal("41-to45 changed original account/Workspace/SSO/License/journal")
	}
	nodeSchemaSeedRows(t, s, b)
	nodeSchemaAssertRows(t, s)
	migrationRun(t, s, c, Readiness)
	t.Log("MYSQL_NODE_SCHEMA_DATA_SHA256=" + nodeSchemaDataDigest(t, s))
}
func TestActualMySQLNodeSchemaReadback(t *testing.T) {
	s, c, _ := actualMigration(t)
	migrationRun(t, s, c, Readiness)
	nodeSchemaAssertRows(t, s)
	var hash []byte
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT token_hash FROM node_join_tokens LIMIT 1`).Scan(&hash); e != nil || len(hash) != 32 || bytes.Equal(hash, make([]byte, 32)) {
		t.Fatal("retained token hash invalid", mysqlstore.SafeError(e))
	}
	t.Log("MYSQL_NODE_SCHEMA_DATA_SHA256=" + nodeSchemaDataDigest(t, s))
}
