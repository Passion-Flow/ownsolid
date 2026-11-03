//go:build mysql_integration

package mysqlmigrate

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

func modelInferenceRefuses(t *testing.T, s *mysqlstore.Store, number uint16, query string, args ...any) {
	t.Helper()
	before := modelInferenceDataDigest(t, s)
	_, e := s.Pool().ExecContext(t.Context(), query, args...)
	var safe *mysqlstore.Error
	if !errors.As(mysqlstore.SafeError(e), &safe) || safe.Number != number {
		t.Fatal("actual model/inference refusal category", mysqlstore.SafeError(e))
	}
	if modelInferenceDataDigest(t, s) != before {
		t.Fatal("refused model/inference write changed accepted rows or ownership")
	}
}

func TestActualMySQLModelInferenceSchemaConstraints(t *testing.T) {
	s, c, r := modelInferenceReady(t)
	for _, item := range []struct {
		name, query string
		args        []any
		number      uint16
	}{
		{"model_uuid", `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES('invalid',?,?,'Invalid UUID','qwen3-0.6b',REPEAT('b',64),JSON_OBJECT(),'deleted',?)`, []any{r.tenant, r.workspace, r.user}, 3819},
		{"model_key", `UPDATE model_versions SET model_key='unsupported' WHERE id=?`, []any{r.model}, 3819},
		{"model_digest", `UPDATE model_versions SET catalog_digest=REPEAT('A',64) WHERE id=?`, []any{r.model}, 3819},
		{"model_manifest_array", `UPDATE model_versions SET manifest=JSON_ARRAY() WHERE id=?`, []any{r.model}, 3819},
		{"model_manifest_overflow", `UPDATE model_versions SET manifest=JSON_OBJECT('v',?) WHERE id=?`, []any{strings.Repeat("x", 16384), r.model}, 3819},
		{"model_manifest_invalid", `UPDATE model_versions SET manifest=? WHERE id=?`, []any{"{", r.model}, 3140},
		{"model_manifest_null", `UPDATE model_versions SET manifest=NULL WHERE id=?`, []any{r.model}, 1048},
		{"model_manifest_JSON_null", `UPDATE model_versions SET manifest=CAST('null' AS JSON) WHERE id=?`, []any{r.model}, 3819},
		{"model_state", `UPDATE model_versions SET state='unsupported' WHERE id=?`, []any{r.model}, 3819},
		{"model_creator", `UPDATE model_versions SET created_by=? WHERE id=?`, []any{uuid.NewString(), r.model}, 1452},
		{"import_expected_zero", `UPDATE model_imports SET expected_bytes=0 WHERE id=?`, []any{r.importing}, 3819},
		{"import_uploaded_negative", `UPDATE model_imports SET uploaded_bytes=-1 WHERE id=?`, []any{r.importing}, 3819},
		{"import_uploaded_overflow", `UPDATE model_imports SET uploaded_bytes=17 WHERE id=?`, []any{r.importing}, 3819},
		{"import_state", `UPDATE model_imports SET state='unsupported' WHERE id=?`, []any{r.importing}, 3819},
		{"import_attempts_negative", `UPDATE model_imports SET attempts=-1 WHERE id=?`, []any{r.importing}, 3819},
		{"import_attempts_overflow", `UPDATE model_imports SET attempts=4 WHERE id=?`, []any{r.importing}, 3819},
		{"import_epoch", `UPDATE model_imports SET requested_epoch=-1 WHERE id=?`, []any{r.importing}, 3819},
		{"import_fingerprint", `UPDATE model_imports SET fingerprint=? WHERE id=?`, []any{[]byte{1}, r.importing}, 3819},
		{"import_fingerprint_null", `UPDATE model_imports SET fingerprint=NULL WHERE id=?`, []any{r.importing}, 1048},
		{"import_requester", `UPDATE model_imports SET requested_by=? WHERE id=?`, []any{uuid.NewString(), r.importing}, 1452},
		{"import_model", `UPDATE model_imports SET model_id=? WHERE id=?`, []any{uuid.NewString(), r.importing}, 1452},
		{"chunk_alignment", `UPDATE model_import_chunks SET byte_offset=1 WHERE import_id=?`, []any{r.importing}, 3819},
		{"chunk_negative", `UPDATE model_import_chunks SET byte_offset=-8388608 WHERE import_id=?`, []any{r.importing}, 3819},
		{"chunk_size_zero", `UPDATE model_import_chunks SET size_bytes=0 WHERE import_id=?`, []any{r.importing}, 3819},
		{"chunk_size_overflow", `UPDATE model_import_chunks SET size_bytes=8388609 WHERE import_id=?`, []any{r.importing}, 3819},
		{"chunk_digest", `UPDATE model_import_chunks SET sha256='invalid' WHERE import_id=?`, []any{r.importing}, 3819},
		{"chunk_import", `UPDATE model_import_chunks SET import_id=? WHERE import_id=?`, []any{uuid.NewString(), r.importing}, 1452},
		{"inference_observation_array", `UPDATE inference_nodes SET observation=JSON_ARRAY() WHERE node_id=?`, []any{r.node}, 3819},
		{"inference_observation_overflow", `UPDATE inference_nodes SET observation=JSON_OBJECT('v',?) WHERE node_id=?`, []any{strings.Repeat("x", 32768), r.node}, 3819},
		{"inference_scope", `UPDATE inference_nodes SET workspace_id=? WHERE node_id=?`, []any{uuid.NewString(), r.node}, 1452},
		{"deployment_desired", `UPDATE deployments SET desired_state='unsupported' WHERE id=?`, []any{r.deployment}, 3819},
		{"deployment_observed", `UPDATE deployments SET observed_state='unsupported' WHERE id=?`, []any{r.deployment}, 3819},
		{"deployment_revision_zero", `UPDATE deployments SET revision=0 WHERE id=?`, []any{r.deployment}, 3819},
		{"deployment_revision_overflow", `UPDATE deployments SET revision=9007199254740992 WHERE id=?`, []any{r.deployment}, 3819},
		{"deployment_observation_array", `UPDATE deployments SET observation=JSON_ARRAY() WHERE id=?`, []any{r.deployment}, 3819},
		{"deployment_observation_overflow", `UPDATE deployments SET observation=JSON_OBJECT('v',?) WHERE id=?`, []any{strings.Repeat("x", 8192), r.deployment}, 3819},
		{"deployment_model", `UPDATE deployments SET model_id=? WHERE id=?`, []any{uuid.NewString(), r.deployment}, 1452},
		{"deployment_task_node", `UPDATE deployments SET last_task_id=? WHERE id=?`, []any{r.derived, r.deployment}, 1452},
		{"deployment_creator", `UPDATE deployments SET created_by=? WHERE id=?`, []any{uuid.NewString(), r.deployment}, 1452},
		{"deployment_recovery_digest", `UPDATE deployments SET recovery_receipt_sha256='invalid' WHERE id=?`, []any{r.deployment}, 3819},
		{"lease_state", `UPDATE gpu_leases SET state='unsupported' WHERE deployment_id=?`, []any{r.deployment}, 3819},
		{"lease_revision_zero", `UPDATE gpu_leases SET revision=0 WHERE deployment_id=?`, []any{r.deployment}, 3819},
		{"lease_revision_overflow", `UPDATE gpu_leases SET revision=9007199254740992 WHERE deployment_id=?`, []any{r.deployment}, 3819},
		{"lease_scope", `UPDATE gpu_leases SET workspace_id=? WHERE deployment_id=?`, []any{uuid.NewString(), r.deployment}, 1452},
	} {
		t.Run(item.name, func(t *testing.T) { modelInferenceRefuses(t, s, item.number, item.query, item.args...) })
	}
	migrationRun(t, s, c, Readiness)
}
