package models

import (
	"context"
	"database/sql"
	"errors"

	lifecyclewire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations/snapshot"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const derivedLineageGraph = `
FROM model_derivations d
JOIN model_preparations p ON p.tenant_id=d.tenant_id AND p.workspace_id=d.workspace_id
 AND p.id=d.preparation_id AND p.model_id=d.base_model_id AND p.derived_model_id=d.model_id
 AND p.current_attempt_id=d.attempt_id AND p.review_id=d.review_id AND p.state='approved'
JOIN model_preparation_attempts a ON a.tenant_id=d.tenant_id AND a.workspace_id=d.workspace_id
 AND a.preparation_id=p.id AND a.id=d.attempt_id AND a.outcome_sha256=d.report_sha256 AND a.state='prepared'
JOIN model_preparation_artifacts f ON f.tenant_id=d.tenant_id AND f.workspace_id=d.workspace_id
 AND f.preparation_id=p.id AND f.attempt_id=a.id AND f.id=d.artifact_id AND f.manifest_sha256=d.report_sha256
JOIN model_preparation_reviews r ON r.tenant_id=d.tenant_id AND r.workspace_id=d.workspace_id
 AND r.id=d.review_id AND r.preparation_id=p.id AND r.attempt_id=a.id AND r.artifact_id=f.id
 AND r.model_id=d.model_id AND r.report_sha256=d.report_sha256 AND r.decision='approve'
JOIN model_versions b ON b.tenant_id=d.tenant_id AND b.workspace_id=d.workspace_id AND b.id=d.base_model_id AND b.kind='approved_base'
JOIN training_jobs j ON j.tenant_id=p.tenant_id AND j.workspace_id=p.workspace_id
 AND j.id=p.training_job_id AND j.model_id=b.id AND j.model_catalog_sha256=b.catalog_digest
JOIN training_artifacts t ON t.tenant_id=p.tenant_id AND t.workspace_id=p.workspace_id
 AND t.id=p.training_artifact_id AND t.job_id=j.id AND t.attempt_id=p.training_attempt_id
JOIN model_holdouts h ON h.tenant_id=p.tenant_id AND h.workspace_id=p.workspace_id AND h.id=p.heldout_id
JOIN dataset_versions v ON v.tenant_id=h.tenant_id AND v.workspace_id=h.workspace_id
 AND v.dataset_id=h.dataset_id AND v.id=h.dataset_version_id AND v.manifest_sha256=h.manifest_sha256
`

// The caller has already checked the actor and workspace and locked the matching
// model and derivation with the existing catalog read.
func derivedLineageWithin(ctx context.Context, q transaction.Executor, catalog lifecyclewire.DerivedCatalog) (*snapshot.DatasetLineage, error) {
	var raw []byte
	var snapshotSHA, adapterSHA, holdoutAttempt string
	binding := snapshot.Binding{ModelID: catalog.BaseModelID}
	binding.TrainingScope.TenantID, binding.TrainingScope.WorkspaceID = catalog.Scope.TenantID, catalog.Scope.WorkspaceID
	var stored snapshot.DatasetLineage
	err := q.QueryRow(ctx, modelSQL(q,
		`SELECT p.input_snapshot,p.input_sha256,p.training_job_id::text,p.training_attempt_id::text,p.training_artifact_id::text,p.heldout_id::text,
 j.dataset_id::text,j.dataset_version_id::text,j.dataset_manifest_sha256,h.dataset_id::text,h.dataset_version_id::text,h.manifest_sha256,t.manifest_sha256,v.attempt_id::text`+
			derivedLineageGraph+`WHERE d.tenant_id=$1 AND d.workspace_id=$2 AND d.model_id=$3 AND b.catalog_digest=$4
 AND (d.site_id,d.cell_id)=(local_site_id(),local_cell_id())
 AND (p.site_id,p.cell_id)=(local_site_id(),local_cell_id())
 AND (a.site_id,a.cell_id)=(local_site_id(),local_cell_id())
 AND (f.site_id,f.cell_id)=(local_site_id(),local_cell_id())
 AND (r.site_id,r.cell_id)=(local_site_id(),local_cell_id())
 AND (b.site_id,b.cell_id)=(local_site_id(),local_cell_id())
 AND (j.site_id,j.cell_id)=(local_site_id(),local_cell_id())
 AND (t.site_id,t.cell_id)=(local_site_id(),local_cell_id())
 AND (h.site_id,h.cell_id)=(local_site_id(),local_cell_id())
 AND (v.site_id,v.cell_id)=(local_site_id(),local_cell_id())`,
		`SELECT p.input_snapshot,p.input_sha256,p.training_job_id,p.training_attempt_id,p.training_artifact_id,p.heldout_id,
 j.dataset_id,j.dataset_version_id,j.dataset_manifest_sha256,h.dataset_id,h.dataset_version_id,h.manifest_sha256,t.manifest_sha256,v.attempt_id`+
			derivedLineageGraph+`WHERE d.tenant_id=? AND d.workspace_id=? AND d.model_id=? AND b.catalog_digest=?`,
		catalog.Scope.TenantID, catalog.Scope.WorkspaceID, catalog.ModelID, catalog.BaseCatalogSHA256)).Scan(
		&raw, &snapshotSHA, &binding.TrainingScope.JobID, &binding.TrainingAttemptID, &binding.TrainingArtifactID, &binding.HoldoutID,
		&stored.Training.DatasetID, &stored.Training.DatasetVersionID, &stored.Training.ManifestSHA256,
		&stored.Holdout.DatasetID, &stored.Holdout.DatasetVersionID, &stored.Holdout.ManifestSHA256, &adapterSHA, &holdoutAttempt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fail("MODEL_DERIVED_CATALOG_INVALID")
	}
	if err != nil {
		return nil, modelReadError(err)
	}
	inputs, err := snapshot.Decode(raw, snapshotSHA)
	if err != nil || !inputs.Matches(binding) || inputs.Adapter.ManifestSHA256 != adapterSHA || inputs.DatasetAttemptID != holdoutAttempt {
		return nil, fail("MODEL_DERIVED_CATALOG_INVALID")
	}
	lineage := inputs.Lineage()
	if lineage != stored {
		return nil, fail("MODEL_DERIVED_CATALOG_INVALID")
	}
	return &lineage, nil
}
