//go:build mysql_integration

package mysqlmigrate

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// All model/GPU content here is synthetic metadata. SQL/TLS/journal/constraints
// are real; these rows do not establish model ingestion, deployment or GPU execution.
type modelInferenceSchemaRows struct {
	nodeSchemaRows
	model, importing, deployment, gpu string
}

func modelInferencePrefixDigest(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	return hash(nodeSchemaDataDigest(t, s) + licenseSchemaQueryDigest(t, s,
		`SELECT JSON_ARRAY(step,source_sha256,object_name,object_sha256,state,started_at,applied_at) FROM schema_migrations WHERE step<=45 ORDER BY step`))
}
func modelInferenceDataDigest(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	result := modelInferencePrefixDigest(t, s)
	for _, query := range []string{
		`SELECT JSON_ARRAY(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by,created_at,deleted_at,ready_catalog_digest) FROM model_versions ORDER BY id`,
		`SELECT JSON_ARRAY(id,tenant_id,workspace_id,name,catalog_digest,expected_bytes,uploaded_bytes,state,error_code,attempts,requested_by,requested_epoch,idempotency_key,HEX(fingerprint),reason,model_id,created_at,updated_at,active_workspace_id) FROM model_imports ORDER BY id`,
		`SELECT JSON_ARRAY(import_id,byte_offset,size_bytes,sha256) FROM model_import_chunks ORDER BY import_id,byte_offset`,
		`SELECT JSON_ARRAY(tenant_id,workspace_id,node_id,observation,received_at) FROM inference_nodes ORDER BY node_id`,
		`SELECT JSON_ARRAY(id,tenant_id,workspace_id,node_id,model_id,gpu_uuid,desired_state,observed_state,revision,observation,observed_at,last_task_id,removed_at,created_by,created_at,updated_at,recovery_receipt_sha256,retained_node_id) FROM deployments ORDER BY id`,
		`SELECT JSON_ARRAY(tenant_id,workspace_id,node_id,gpu_uuid,deployment_id,revision,state,updated_at,reserved_node_id,reserved_gpu_uuid) FROM gpu_leases ORDER BY deployment_id`,
	} {
		result += licenseSchemaQueryDigest(t, s, query)
	}
	return hash(result)
}
func modelInferenceSeed(t *testing.T, s *mysqlstore.Store, nodes nodeSchemaRows) modelInferenceSchemaRows {
	t.Helper()
	r := modelInferenceSchemaRows{nodeSchemaRows: nodes, model: uuid.NewString(), importing: uuid.NewString(), deployment: uuid.NewString(), gpu: "GPU-" + uuid.NewString()}
	const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	migrationExec(t, s, "INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by,created_at) VALUES(?,?,?,'Synthetic model \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629','qwen3-0.6b',?,JSON_OBJECT('synthetic_schema',CAST('true' AS JSON),'label','\u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629'),'ready',?,?)", r.model, r.tenant, r.workspace, digest, r.user, r.heartbeat)
	migrationExec(t, s, "INSERT INTO model_imports(id,tenant_id,workspace_id,name,catalog_digest,expected_bytes,uploaded_bytes,state,requested_by,requested_epoch,idempotency_key,fingerprint,reason,model_id,created_at,updated_at) VALUES(?,?,?,'Synthetic import \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629',?,16,8,'uploading',?,1,?,?,'Synthetic schema acceptance',?,?,?)", r.importing, r.tenant, r.workspace, digest, r.user, uuid.NewString(), make([]byte, 32), r.model, r.heartbeat, r.heartbeat)
	migrationExec(t, s, `INSERT INTO model_import_chunks(import_id,byte_offset,size_bytes,sha256) VALUES(?,0,8,?)`, r.importing, digest)
	migrationExec(t, s, `INSERT INTO inference_nodes(tenant_id,workspace_id,node_id,observation,received_at) VALUES(?,?,?,JSON_OBJECT('synthetic_schema',CAST('true' AS JSON)),?)`, r.tenant, r.workspace, r.node, r.heartbeat)
	migrationExec(t, s, `INSERT INTO deployments(id,tenant_id,workspace_id,node_id,model_id,gpu_uuid,desired_state,observed_state,revision,observation,observed_at,last_task_id,created_by,created_at,updated_at) VALUES(?,?,?,?,?,?,'running','running',1,JSON_OBJECT('synthetic_schema',CAST('true' AS JSON)),?,?,?,?,?)`, r.deployment, r.tenant, r.workspace, r.node, r.model, r.gpu, r.heartbeat, r.task, r.user, r.heartbeat, r.heartbeat)
	migrationExec(t, s, `INSERT INTO gpu_leases(tenant_id,workspace_id,node_id,gpu_uuid,deployment_id,revision,state,updated_at) VALUES(?,?,?,?,?,1,'held',?)`, r.tenant, r.workspace, r.node, r.gpu, r.deployment, r.heartbeat)
	return r
}
func modelInferenceReady(t *testing.T) (*mysqlstore.Store, Catalog, modelInferenceSchemaRows) {
	t.Helper()
	s, c, _ := actualMigration(t)
	migrationRun(t, s, c, Apply)
	old := nodeSchemaSeedOld(t, s)
	return s, c, modelInferenceSeed(t, s, nodeSchemaSeedRows(t, s, old))
}
func modelInferenceAssertRows(t *testing.T, s *mysqlstore.Store) {
	t.Helper()
	for _, table := range []string{"model_versions", "model_imports", "model_import_chunks", "inference_nodes", "deployments", "gpu_leases"} {
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); e != nil || count != 1 {
			t.Fatal("actual model/inference retained row count", table, mysqlstore.SafeError(e))
		}
	}
	var name, digest string
	var raw []byte
	var stamp time.Time
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT name,ready_catalog_digest,manifest,created_at FROM model_versions`).Scan(&name, &digest, &raw, &stamp); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	var manifest map[string]any
	if json.Unmarshal(raw, &manifest) != nil || manifest["synthetic_schema"] != true || manifest["label"] != "\u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629" || name != "Synthetic model \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629" || len(digest) != 64 || stamp.Location() != time.UTC || stamp.Nanosecond() != 123456000 {
		t.Fatal("actual model JSON/Unicode/generated/UTC microsecond round trip")
	}
	var fingerprint []byte
	var active, workspace string
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT fingerprint,active_workspace_id,workspace_id FROM model_imports`).Scan(&fingerprint, &active, &workspace); e != nil || len(fingerprint) != 32 || active != workspace {
		t.Fatal("actual import binary/generated scope round trip", mysqlstore.SafeError(e))
	}
	var reservedNode, reservedGPU, node, gpu string
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT reserved_node_id,reserved_gpu_uuid,node_id,gpu_uuid FROM gpu_leases`).Scan(&reservedNode, &reservedGPU, &node, &gpu); e != nil || reservedNode != node || reservedGPU != gpu {
		t.Fatal("actual held node/GPU index reservation round trip", mysqlstore.SafeError(e))
	}
}

func TestActualMySQLModelInferenceSchemaFresh(t *testing.T) {
	s, c, _ := modelInferenceReady(t)
	modelInferenceAssertRows(t, s)
	before, events := modelInferenceDataDigest(t, s), 0
	r, e := Run(t.Context(), s, c, testOptions(), Apply, func(Event) { events++ })
	if e != nil || !r.Ready || events != 0 || modelInferenceDataDigest(t, s) != before {
		t.Fatal("actual fresh model/inference migration no-op changed state", e)
	}
	migrationRun(t, s, c, Readiness)
}

func TestActualMySQLModelInferenceSchemaUpgrade(t *testing.T) {
	s, c, _ := actualMigration(t)
	prefix := c
	prefix.steps = c.steps[:45]
	migrationRun(t, s, prefix, Apply)
	old := nodeSchemaSeedOld(t, s)
	nodes := nodeSchemaSeedRows(t, s, old)
	before := modelInferencePrefixDigest(t, s)
	migrationRun(t, s, c, Apply)
	if modelInferencePrefixDigest(t, s) != before {
		t.Fatal("45-to51 changed retained identity/node/task/journal state")
	}
	modelInferenceSeed(t, s, nodes)
	modelInferenceAssertRows(t, s)
	migrationRun(t, s, c, Readiness)
}
