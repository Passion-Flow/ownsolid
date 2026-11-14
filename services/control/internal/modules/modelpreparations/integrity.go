package modelpreparations

import (
	"context"
	"encoding/json"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func (s *Service) storageRequired(ctx context.Context) (string, bool, error) {
	store, repositoryError := s.runtimeRepository()
	if repositoryError != nil {
		return "", false, repositoryError
	}
	var tenant string
	var required bool
	e := preparationIDRow(preparationQueryRow(ctx, store, preparationSelectedSQL(store, preparationDM(`SELECT "TENANT_ID",CASE WHEN EXISTS(SELECT 1 FROM "MODEL_PREPARATION_UPLOADS" "U" WHERE "U"."TENANT_ID"="I"."TENANT_ID") OR EXISTS(SELECT 1 FROM "MODEL_PREPARATIONS" "P" WHERE "P"."TENANT_ID"="I"."TENANT_ID" AND "P"."STATE" NOT IN ('quality_failed','approved','rejected','failed','cancelled')) THEN 1 ELSE 0 END FROM "INSTALLATION_STATE" "I" WHERE "ID"=1 AND "BOOTSTRAP_COMPLETE"=1`), `SELECT tenant_id::text,EXISTS(SELECT 1 FROM model_preparation_uploads u WHERE u.tenant_id=i.tenant_id) OR EXISTS(SELECT 1 FROM model_preparations p WHERE p.tenant_id=i.tenant_id AND p.state NOT IN ('quality_failed','approved','rejected','failed','cancelled')) FROM installation_state i WHERE id=1 AND bootstrap_complete`, `SELECT tenant_id,EXISTS(SELECT 1 FROM model_preparation_uploads u WHERE u.tenant_id=i.tenant_id) OR EXISTS(SELECT 1 FROM model_preparations p WHERE p.tenant_id=i.tenant_id AND p.state NOT IN ('quality_failed','approved','rejected','failed','cancelled')) FROM installation_state i WHERE id=1 AND bootstrap_complete`)), 0).Scan(&tenant, &required)
	if e != nil {
		return "", false, preparationInstallationFailure(store, e)
	}
	return tenant, required, nil
}

func (s *Service) OpenConfiguredStore(ctx context.Context) error {
	c := s.Identity.Config
	if !c.ModelPreparation.Enabled() {
		return s.VerifyStored(ctx)
	}
	if c.Models.ArtifactRoot == "" || s.Datasets == nil || !s.Datasets.PublishedSourcesAvailable() || s.Training == nil || s.Training.Store == nil {
		return fail("MODEL_PREPARATION_INPUT_STORES_REQUIRED")
	}
	tenant, _, e := s.storageRequired(ctx)
	if e != nil {
		return e
	}
	store, e := OpenStore(c.ModelPreparation.ArtifactRoot, tenant, s.policy().MinimumFreeBytes, wire.MaximumModelBytes, wire.MaximumModelFiles)
	if e != nil {
		return e
	}
	s.Store = store
	if e = s.VerifyStored(ctx); e != nil {
		store.Close()
		s.Store = nil
		return e
	}
	return nil
}

// VerifyStored checks immutable accepted records and physical hashes, never adopting an unfinished stage.
// Completed acceptance already owns semantic tensor verification; this check does not requalify hardware.
func (s *Service) VerifyStored(ctx context.Context) error {
	store, repositoryError := s.runtimeRepository()
	if repositoryError != nil {
		return repositoryError
	}
	tenant, required, e := s.storageRequired(ctx)
	if e != nil {
		return e
	}
	if s.Store == nil {
		if required {
			return fail("MODEL_PREPARATION_STORAGE_REQUIRED")
		}
		return nil
	}
	if tenant != s.Store.namespace.TenantID {
		return fail("MODEL_PREPARATION_STORE_NAMESPACE_INVALID")
	}
	if e = s.Store.VerifyNamespace(); e != nil {
		return e
	}
	if store.Dialect() == transaction.Dameng {
		return s.verifyNativePreparationOutputs(ctx, store, tenant)
	}
	rows, e := preparationQuery(ctx, store, preparationSQL(store, `SELECT a.id::text,a.workspace_id::text,a.preparation_id::text,a.attempt_id::text,a.manifest_sha256,a.descriptor,a.report_bytes,a.storage_path FROM model_preparation_artifacts a WHERE a.tenant_id=$1 ORDER BY a.id`, `SELECT a.id,a.workspace_id,a.preparation_id,a.attempt_id,a.manifest_sha256,a.descriptor,a.report_bytes,a.storage_path FROM model_preparation_artifacts a WHERE a.tenant_id=? ORDER BY a.id`, tenant))
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var id, workspace, preparation, attempt, sha, path string
		var descriptor, report []byte
		if e = rows.Scan(&id, &workspace, &preparation, &attempt, &sha, &descriptor, &report, &path); e != nil {
			return e
		}
		var d UploadDescriptor
		var result wire.PreparedResult
		if wire.DecodeStrict(descriptor, &d) != nil || d.Validate(wire.MaximumModelBytes, wire.MaximumModelFiles) != nil || d.UploadID != id || d.Scope != (wire.Scope{TenantID: tenant, WorkspaceID: workspace, PreparationID: preparation}) || d.AttemptID != attempt || d.ManifestSHA256 != sha || path != publishedDirectory(d) || int64(len(report)) > wire.MaximumResultBytes || hash(report) != sha || wire.DecodeStrict(report, &result) != nil || result.Status != "prepared" || result.TenantID != tenant || result.WorkspaceID != workspace || result.PreparationID != preparation || result.AttemptID != attempt {
			return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
		}
		expected, _ := json.Marshal(d.Files)
		actual, _ := json.Marshal(result.ModelFiles)
		if string(actual) != string(expected) {
			return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
		}
		if e = s.Store.VerifyPublished(ctx, d); e != nil {
			return e
		}
	}
	return rows.Err()
}
