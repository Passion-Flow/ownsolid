package models

import (
	"context"
	"database/sql"
	"errors"

	lifecyclewire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Borrowed access never authenticates, opens or commits a transaction. Its owner
// supplies current server-resolved scope and retains authority/IO rechecks.
func (s *Service) accessExecutor(ctx context.Context, q transaction.Executor, tenant, workspace, model string) error {
	if s == nil || ctx == nil || q == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	if e := ctx.Err(); e != nil {
		return importMutationError(e)
	}
	if !validID(tenant) || !validID(workspace) || !validID(model) {
		return fail("RESOURCE_NOT_FOUND")
	}
	if q.Dialect() != transaction.PostgreSQL && q.Dialect() != transaction.MySQL && q.Dialect() != transaction.Dameng {
		return fail("STORAGE_UNAVAILABLE")
	}
	if s.Identity != nil || s.DB != nil || s.Workspaces != nil {
		configured, e := s.fileRepository()
		if e != nil {
			return e
		}
		if configured.Dialect() != q.Dialect() {
			return fail("MODEL_STORAGE_MISMATCH")
		}
	}
	return nil
}

// ReadyWithin holds the exact approved-base row through the caller's reference insert.
func (s *Service) ReadyWithin(ctx context.Context, q transaction.Executor, tenant, workspace, id string) error {
	if e := s.accessExecutor(ctx, q, tenant, workspace, id); e != nil {
		return e
	}
	var digest string
	var manifest []byte
	e := q.QueryRow(ctx, modelNativeSQL(q, `SELECT CATALOG_DIGEST,MANIFEST FROM MODEL_VERSIONS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=? AND KIND='approved_base' AND STATE='ready' FOR UPDATE`,
		"SELECT catalog_digest,manifest FROM model_versions WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 AND kind='approved_base' AND state='ready' FOR SHARE",
		"SELECT catalog_digest,manifest FROM model_versions WHERE tenant_id=? AND workspace_id=? AND id=? AND kind='approved_base' AND state='ready' FOR SHARE", tenant, workspace, id)).Scan(&digest, &manifest)
	if errors.Is(e, sql.ErrNoRows) {
		return fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return modelReadError(e)
	}
	approved := resolveImportManifest(digest)
	if approved == nil || !approved.matchesStored(manifest) {
		return fail("MODEL_FILE_INVALID")
	}
	return nil
}

func (s *Service) DecodeDerivedVersionWithin(ctx context.Context, q transaction.Executor, tenant, workspace, model string) (lifecyclewire.DerivedCatalog, error) {
	return s.derivedVersionWithin(ctx, q, tenant, workspace, model, true)
}
func (s *Service) RetainedDerivedVersionWithin(ctx context.Context, q transaction.Executor, tenant, workspace, model string) (lifecyclewire.DerivedCatalog, error) {
	return s.derivedVersionWithin(ctx, q, tenant, workspace, model, false)
}
func (s *Service) derivedVersionWithin(ctx context.Context, q transaction.Executor, tenant, workspace, model string, ready bool) (lifecyclewire.DerivedCatalog, error) {
	var zero lifecyclewire.DerivedCatalog
	if e := s.accessExecutor(ctx, q, tenant, workspace, model); e != nil {
		return zero, e
	}
	var raw, record []byte
	var digest, kind string
	var preparation, attempt, artifact, base, report string
	e := q.QueryRow(ctx, modelSQL(q,
		`SELECT v.manifest,v.catalog_digest,v.kind,d.catalog,d.preparation_id::text,d.attempt_id::text,d.artifact_id::text,d.base_model_id::text,d.report_sha256 FROM model_versions v JOIN model_derivations d ON d.tenant_id=v.tenant_id AND d.workspace_id=v.workspace_id AND d.model_id=v.id AND d.catalog_digest=v.catalog_digest WHERE v.tenant_id=$1 AND v.workspace_id=$2 AND v.id=$3 AND (NOT $4::boolean OR v.state='ready') FOR SHARE OF v,d`,
		`SELECT v.manifest,v.catalog_digest,v.kind,d.catalog,d.preparation_id,d.attempt_id,d.artifact_id,d.base_model_id,d.report_sha256 FROM model_versions v JOIN model_derivations d ON d.tenant_id=v.tenant_id AND d.workspace_id=v.workspace_id AND d.model_id=v.id AND d.catalog_digest=v.catalog_digest WHERE v.tenant_id=? AND v.workspace_id=? AND v.id=? AND v.state IN ('ready','deleted') AND (NOT ? OR v.state='ready') FOR SHARE`, tenant, workspace, model, ready)).Scan(&raw, &digest, &kind, &record, &preparation, &attempt, &artifact, &base, &report)
	if errors.Is(e, sql.ErrNoRows) {
		return zero, fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return zero, modelReadError(e)
	}
	c, e := lifecyclewire.DecodeDerivedCatalog(raw)
	if e != nil {
		return zero, fail("MODEL_DERIVED_CATALOG_INVALID")
	}
	actual, e := c.Digest()
	other, otherError := lifecyclewire.DecodeDerivedCatalog(record)
	if e != nil || actual != digest || kind != "merged_lora" || c.Scope.TenantID != tenant || c.Scope.WorkspaceID != workspace || c.ModelID != model || otherError != nil {
		return zero, fail("MODEL_DERIVED_CATALOG_INVALID")
	}
	if c.Scope.PreparationID != preparation || c.AttemptID != attempt || c.ArtifactID != artifact || c.BaseModelID != base || c.ReportSHA256 != report {
		return zero, fail("MODEL_DERIVED_CATALOG_INVALID")
	}
	otherSHA, e := other.Digest()
	if e != nil || otherSHA != actual {
		return zero, fail("MODEL_DERIVED_CATALOG_INVALID")
	}
	return c, nil
}
