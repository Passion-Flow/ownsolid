package recovery

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func reconcileInferenceRemovalSelected(ctx context.Context, accounts *identity.Service, receipt wire.InferenceRemovalReceipt, reason string) error {
	if receipt.Validate() != nil || !operatorReason(reason) {
		return errors.New("VALIDATION_FAILED")
	}
	if receipt.CompletedAt.After(time.Now().Add(receiptFutureSkew)) || receipt.CompletedAt.Before(time.Now().Add(-receiptMaximumAge)) {
		return errors.New("RECOVERY_RECEIPT_EXPIRED")
	}
	return restoreTransaction(ctx, accounts, func(ctx context.Context, q transaction.Executor) error {
		if accounts.Config.ModelsRuntime() {
			if e := database.CheckAccountProfileWithin(ctx, q, accounts.Config.Server.Profile()); e != nil {
				return e
			}
		}
		var tenant, workspace string
		e := q.QueryRow(ctx, recoverySQL(q, `SELECT n.tenant_id::text,n.workspace_id::text FROM managed_nodes n JOIN installation_state i ON i.tenant_id=n.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND n.id=$1 AND n.execution_profile IN ($2,$3,$4)`, `SELECT n.tenant_id,n.workspace_id FROM managed_nodes n JOIN installation_state i ON i.tenant_id=n.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND n.id=? AND n.execution_profile IN (?,?,?)`, receipt.NodeID, wire.DockerInferenceProfile, wire.DockerAIProfile, wire.DockerModelLifecycleProfile)).Scan(&tenant, &workspace)
		if errors.Is(e, sql.ErrNoRows) {
			return errors.New("RESOURCE_NOT_FOUND")
		}
		if e != nil {
			return e
		}
		var locked string
		if e = q.QueryRow(ctx, recoverySQL(q, `SELECT w.id::text FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND w.tenant_id=$1 AND w.id=$2 FOR UPDATE OF w`, `SELECT w.id FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND w.tenant_id=? AND w.id=? FOR UPDATE`, tenant, workspace)).Scan(&locked); e != nil {
			return e
		}
		var fenced *time.Time
		var revoked bool
		if e = q.QueryRow(ctx, recoverySQL(q, `SELECT status='removed' AND credential_hash IS NULL,inference_fenced_at FROM managed_nodes WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE`, `SELECT status='removed' AND credential_hash IS NULL,inference_fenced_at FROM managed_nodes WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE`, tenant, workspace, receipt.NodeID)).Scan(&revoked, &fenced); e != nil {
			return e
		}
		if !revoked || fenced == nil || receipt.CompletedAt.Before(*fenced) {
			return errors.New("RECOVERY_NODE_NOT_FENCED")
		}
		var model, gpu, kind, catalog, desired, observed string
		var recipe, previous *string
		var revision int64
		var removed *time.Time
		e = q.QueryRow(ctx, recoverySQL(q, `SELECT model_id::text,gpu_uuid,revision,recovery_receipt_sha256,model_kind,catalog_digest,serving_recipe_sha256,desired_state,observed_state,removed_at FROM deployments WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4 FOR UPDATE`, `SELECT model_id,gpu_uuid,revision,recovery_receipt_sha256,model_kind,catalog_digest,serving_recipe_sha256,desired_state,observed_state,removed_at FROM deployments WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? FOR UPDATE`, tenant, workspace, receipt.NodeID, receipt.DeploymentID)).Scan(&model, &gpu, &revision, &previous, &kind, &catalog, &recipe, &desired, &observed, &removed)
		if errors.Is(e, sql.ErrNoRows) {
			return errors.New("RESOURCE_NOT_FOUND")
		}
		if e != nil {
			return e
		}
		matches := kind == "approved_base" && recipe == nil && receipt.Derived == nil
		if receipt.Derived != nil {
			matches = kind == wire.DerivedModelKind && recipe != nil && *recipe == receipt.Derived.ServingRecipeSHA256 && receipt.Derived.Catalog.Scope.TenantID == tenant && receipt.Derived.Catalog.Scope.WorkspaceID == workspace
		}
		if model != receipt.ModelID || gpu != receipt.GPUUUID || catalog != receipt.CatalogDigest || !matches || receipt.LocalRevision > revision {
			return errors.New("INFERENCE_OWNERSHIP_CONFLICT")
		}
		var leaseState string
		if e = q.QueryRow(ctx, recoverySQL(q, `SELECT state FROM gpu_leases WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND deployment_id=$4 AND gpu_uuid=$5 AND revision=$6 FOR UPDATE`, `SELECT state FROM gpu_leases WHERE tenant_id=? AND workspace_id=? AND node_id=? AND deployment_id=? AND gpu_uuid=? AND revision=? FOR UPDATE`, tenant, workspace, receipt.NodeID, receipt.DeploymentID, gpu, revision)).Scan(&leaseState); e != nil {
			return e
		}
		raw, _ := json.Marshal(receipt)
		sum := sha256.Sum256(raw)
		digest := hex.EncodeToString(sum[:])
		if previous != nil {
			if *previous != digest {
				return errors.New("IDEMPOTENCY_CONFLICT")
			}
			if desired != "removed" || observed != "not_installed" || removed == nil || leaseState != "released" {
				return errors.New("RECOVERY_RECEIPT_STATE_INVALID")
			}
			return nil
		}
		var inflight bool
		if e = q.QueryRow(ctx, recoverySQL(q, `SELECT EXISTS(SELECT 1 FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND state IN ('pending','running'))`, `SELECT EXISTS(SELECT 1 FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND state IN ('pending','running'))`, tenant, workspace, receipt.NodeID)).Scan(&inflight); e != nil {
			return e
		}
		if inflight {
			return errors.New("RECOVERY_NODE_NOT_FENCED")
		}
		changed, e := q.Exec(ctx, recoverySQL(q, `UPDATE deployments SET desired_state='removed',observed_state='not_installed',observation=NULL,observed_at=clock_timestamp(),removed_at=COALESCE(removed_at,clock_timestamp()),recovery_receipt_sha256=$1,updated_at=clock_timestamp() WHERE tenant_id=$2 AND workspace_id=$3 AND node_id=$4 AND id=$5 AND revision=$6`, `UPDATE deployments SET desired_state='removed',observed_state='not_installed',observation=NULL,observed_at=UTC_TIMESTAMP(6),removed_at=COALESCE(removed_at,UTC_TIMESTAMP(6)),recovery_receipt_sha256=?,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? AND revision=?`, digest, tenant, workspace, receipt.NodeID, receipt.DeploymentID, revision))
		if e != nil {
			return e
		}
		if changed != 1 {
			return errors.New("INFERENCE_OWNERSHIP_CONFLICT")
		}
		changed, e = q.Exec(ctx, recoverySQL(q, `UPDATE gpu_leases SET state='released',updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND deployment_id=$4 AND gpu_uuid=$5 AND revision=$6`, `UPDATE gpu_leases SET state='released',updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND deployment_id=? AND gpu_uuid=? AND revision=?`, tenant, workspace, receipt.NodeID, receipt.DeploymentID, gpu, revision))
		if e != nil {
			return e
		}
		if changed != 1 {
			return errors.New("INFERENCE_OWNERSHIP_CONFLICT")
		}
		return operatorAuditWithin(ctx, q, tenant, workspace, "inference.removal_reconciled", "deployment", receipt.DeploymentID, reason, map[string]any{"receipt_sha256": digest, "receipt_id": receipt.ReceiptID, "local_revision": receipt.LocalRevision, "control_revision": revision, "source": "explicit_local_root_operator_attestation", "model_cache_present": receipt.ModelCachePresent})
	})
}
