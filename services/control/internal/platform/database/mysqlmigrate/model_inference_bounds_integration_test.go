//go:build mysql_integration

package mysqlmigrate

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

func TestActualMySQLModelInferenceSchemaBounds(t *testing.T) {
	s, c, r := modelInferenceReady(t)
	for _, item := range []struct {
		name, query string
		args        []any
		number      uint16
	}{
		{"model_name_length", `UPDATE model_versions SET name=? WHERE id=?`, []any{strings.Repeat("\u4e16", 101), r.model}, 1406},
		{"model_creation_null", `UPDATE model_versions SET created_at=NULL WHERE id=?`, []any{r.model}, 1048},
		{"model_workspace_null", `UPDATE model_versions SET workspace_id=NULL WHERE id=?`, []any{r.model}, 1048},
		{"import_uuid", modelInferenceInsertImport, []any{"invalid", r.tenant, r.workspace, strings.Repeat("a", 64), "cancelled", r.user, uuid.NewString(), make([]byte, 32), nil}, 3819},
		{"import_idempotency_uuid", `UPDATE model_imports SET idempotency_key='invalid' WHERE id=?`, []any{r.importing}, 3819},
		{"import_digest", `UPDATE model_imports SET catalog_digest='invalid' WHERE id=?`, []any{r.importing}, 3819},
		{"import_name_length", `UPDATE model_imports SET name=? WHERE id=?`, []any{strings.Repeat("\u4e16", 101), r.importing}, 1406},
		{"import_reason_length", `UPDATE model_imports SET reason=? WHERE id=?`, []any{strings.Repeat("\u4e16", 501), r.importing}, 1406},
		{"import_error_length", `UPDATE model_imports SET error_code=? WHERE id=?`, []any{strings.Repeat("x", 65), r.importing}, 1406},
		{"import_expected_upper_bound", `UPDATE model_imports SET expected_bytes=2147483649 WHERE id=?`, []any{r.importing}, 3819},
		{"import_expected_null", `UPDATE model_imports SET expected_bytes=NULL WHERE id=?`, []any{r.importing}, 1048},
		{"chunk_offset_null", `UPDATE model_import_chunks SET byte_offset=NULL WHERE import_id=?`, []any{r.importing}, 1048},
		{"chunk_size_null", `UPDATE model_import_chunks SET size_bytes=NULL WHERE import_id=?`, []any{r.importing}, 1048},
		{"chunk_digest_null", `UPDATE model_import_chunks SET sha256=NULL WHERE import_id=?`, []any{r.importing}, 1048},
		{"inference_observation_null", `UPDATE inference_nodes SET observation=NULL WHERE node_id=?`, []any{r.node}, 1048},
		{"inference_observation_JSON_null", `UPDATE inference_nodes SET observation=CAST('null' AS JSON) WHERE node_id=?`, []any{r.node}, 3819},
		{"inference_observation_invalid", `UPDATE inference_nodes SET observation=? WHERE node_id=?`, []any{"{", r.node}, 3140},
		{"deployment_gpu_uuid", `UPDATE deployments SET gpu_uuid='invalid' WHERE id=?`, []any{r.deployment}, 3819},
		{"deployment_uuid", modelInferenceInsertDeployment, []any{"invalid", r.tenant, r.workspace, r.node, r.model, r.gpu, r.user, r.heartbeat}, 3819},
		{"deployment_model_null", `UPDATE deployments SET model_id=NULL WHERE id=?`, []any{r.deployment}, 1048},
		{"deployment_observation_JSON_null", `UPDATE deployments SET observation=CAST('null' AS JSON) WHERE id=?`, []any{r.deployment}, 3819},
		{"lease_deployment_missing", `UPDATE gpu_leases SET deployment_id=? WHERE deployment_id=?`, []any{uuid.NewString(), r.deployment}, 1452},
		{"lease_node_null", `UPDATE gpu_leases SET node_id=NULL WHERE deployment_id=?`, []any{r.deployment}, 1048},
	} {
		t.Run(item.name, func(t *testing.T) { modelInferenceRefuses(t, s, item.number, item.query, item.args...) })
	}
	t.Run("exact_JSON_byte_boundaries", func(t *testing.T) {
		for _, item := range []struct {
			table, column, key, id string
			maximum                int
		}{
			{"model_versions", "manifest", "id", r.model, 16384},
			{"inference_nodes", "observation", "node_id", r.node, 32768},
			{"deployments", "observation", "id", r.deployment, 8192},
		} {
			value := strings.Repeat("x", item.maximum-len(`{"v": ""}`))
			query := "UPDATE " + item.table + " SET " + item.column + "=JSON_OBJECT('v',?) WHERE " + item.key + "=?"
			migrationExec(t, s, query, value, item.id)
			var size int
			if e := s.Pool().QueryRowContext(t.Context(), "SELECT OCTET_LENGTH(CAST("+item.column+" AS CHAR CHARACTER SET utf8mb4)) FROM "+item.table+" WHERE "+item.key+"=?", item.id).Scan(&size); e != nil || size != item.maximum {
				t.Fatal("actual exact JSON byte limit", item.table, size, mysqlstore.SafeError(e))
			}
			modelInferenceRefuses(t, s, 3819, query, value+"x", item.id)
		}
	})
	t.Run("valid_nullable_fields_and_integer_limits", func(t *testing.T) {
		migrationExec(t, s, `UPDATE model_imports SET expected_bytes=2147483648,uploaded_bytes=2147483648,attempts=3,requested_epoch=0,error_code=NULL,model_id=NULL,reason=? WHERE id=?`, strings.Repeat("\u4e16", 500), r.importing)
		migrationExec(t, s, `UPDATE deployments SET observation=NULL,observed_at=NULL,last_task_id=NULL,recovery_receipt_sha256=NULL,revision=9007199254740991 WHERE id=?`, r.deployment)
		migrationExec(t, s, `UPDATE gpu_leases SET revision=9007199254740991 WHERE deployment_id=?`, r.deployment)
		var bytes, revision int64
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT uploaded_bytes FROM model_imports WHERE id=?`, r.importing).Scan(&bytes); e != nil || bytes != 2147483648 {
			t.Fatal("exact approved import ceiling", mysqlstore.SafeError(e))
		}
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT revision FROM gpu_leases WHERE deployment_id=?`, r.deployment).Scan(&revision); e != nil || revision != 9007199254740991 {
			t.Fatal("exact safe revision ceiling", mysqlstore.SafeError(e))
		}
	})
	migrationRun(t, s, c, Readiness)
}
