package deployments

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

var _ nodes.InferenceObserverWithin = (*Service)(nil)

func (s *Service) ObserveInferenceWithin(ctx context.Context, q transaction.Executor, n nodes.Node, o wire.InferenceObservation) error {
	if e := s.runtimeExecutor(ctx, q); e != nil {
		return e
	}
	if !wire.IsInferenceProfile(n.ExecutionProfile) {
		return fail("AGENT_PROFILE_REJECTED")
	}
	if e := o.Validate(); e != nil {
		return e
	}
	if d := o.Deployment; d != nil {
		var model, gpu, kind, catalog string
		var serving *string
		var revision int64
		e := q.QueryRow(ctx, deploymentNativeSQL(q, `SELECT "MODEL_ID","GPU_UUID","REVISION","MODEL_KIND","CATALOG_DIGEST","SERVING_RECIPE_SHA256" FROM "DEPLOYMENTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=? AND "ID"=? FOR UPDATE`, `SELECT model_id::text,gpu_uuid,revision,model_kind,catalog_digest,serving_recipe_sha256 FROM deployments WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4 FOR UPDATE`, `SELECT model_id,gpu_uuid,revision,model_kind,catalog_digest,serving_recipe_sha256 FROM deployments WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? FOR UPDATE`, n.TenantID, n.WorkspaceID, n.ID, d.DeploymentID)).Scan(&model, &gpu, &revision, &kind, &catalog, &serving)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("INFERENCE_OWNERSHIP_CONFLICT")
		}
		if e != nil {
			return e
		}
		if model != d.ModelID || gpu != d.GPUUUID || !matchesStoredServing(Deployment{ModelKind: kind, CatalogDigest: catalog, ServingRecipeSHA256: serving}, *d) || d.Revision > revision {
			return fail("INFERENCE_REVISION_CONFLICT")
		}
		if kind == wire.DerivedModelKind && (n.ExecutionProfile != wire.DockerModelLifecycleProfile || n.ProtocolVersion != wire.ModelLifecycleVersion) {
			return fail("AGENT_PROFILE_REJECTED")
		}
		// Heartbeats cannot create deployments, release leases or promote an old revision.
		if revision == d.Revision {
			raw, e := json.Marshal(d)
			if e != nil {
				return fail("VALIDATION_FAILED")
			}
			if _, e = q.Exec(ctx, deploymentNativeSQL(q, `UPDATE "DEPLOYMENTS" SET "OBSERVED_STATE"=?,"OBSERVATION"=?,"OBSERVED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=? AND "ID"=? AND "REVISION"=? AND "REMOVED_AT" IS NULL`, `UPDATE deployments SET observed_state=$1,observation=$2,observed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE tenant_id=$3 AND workspace_id=$4 AND node_id=$5 AND id=$6 AND revision=$7 AND removed_at IS NULL`, `UPDATE deployments SET observed_state=?,observation=CAST(? AS JSON),observed_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? AND revision=? AND removed_at IS NULL`, d.State, deploymentJSON(q, raw), n.TenantID, n.WorkspaceID, n.ID, d.DeploymentID, revision)); e != nil {
				return e
			}
		}
	}
	raw, e := json.Marshal(o)
	if e != nil {
		return fail("VALIDATION_FAILED")
	}
	statement := transaction.SQL(transaction.PostgreSQL, `INSERT INTO inference_nodes(tenant_id,workspace_id,node_id,observation) VALUES($1,$2,$3,$4) ON CONFLICT(node_id) DO UPDATE SET observation=EXCLUDED.observation,received_at=clock_timestamp() WHERE inference_nodes.tenant_id=EXCLUDED.tenant_id AND inference_nodes.workspace_id=EXCLUDED.workspace_id`, n.TenantID, n.WorkspaceID, n.ID, raw)
	if q.Dialect() == transaction.MySQL {
		statement = transaction.SQL(transaction.MySQL, `INSERT INTO inference_nodes(tenant_id,workspace_id,node_id,observation) VALUES(?,?,?,CAST(? AS JSON)) ON DUPLICATE KEY UPDATE observation=CAST(? AS JSON),received_at=UTC_TIMESTAMP(6)`, n.TenantID, n.WorkspaceID, n.ID, raw, raw)
	}
	if q.Dialect() == transaction.Dameng {
		return nativeRecordHardware(ctx, q, n, raw)
	}
	_, e = q.Exec(ctx, statement)
	return e
}
func (s *Service) RecordInferenceResultWithin(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task, result wire.Result) error {
	if e := s.runtimeExecutor(ctx, q); e != nil {
		return e
	}
	command, e := s.Nodes.ReadInferenceCommandWithin(ctx, q, n, t)
	if e != nil {
		return e
	}
	if e = result.ValidateInference(command); e != nil {
		return e
	}
	if command.DeploymentID != "" {
		var revision int64
		var task string
		e = q.QueryRow(ctx, deploymentNativeSQL(q, `SELECT "REVISION","LAST_TASK_ID" FROM "DEPLOYMENTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=? AND "ID"=? FOR UPDATE`, `SELECT revision,last_task_id::text FROM deployments WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4 FOR UPDATE`, `SELECT revision,last_task_id FROM deployments WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? FOR UPDATE`, n.TenantID, n.WorkspaceID, n.ID, command.DeploymentID)).Scan(&revision, &task)
		if e != nil {
			return e
		}
		if revision != command.Revision || task != t.ID {
			return fail("AGENT_LEASE_STALE")
		}
	}
	if e = s.ObserveInferenceWithin(ctx, q, n, *result.Inference); e != nil {
		return e
	}
	if result.Outcome == "success" && (command.Action == "stop" || command.Action == "remove") {
		// Successful current fenced tasks alone can confirm physical stop/removal.
		changed, e := q.Exec(ctx, deploymentNativeSQL(q, `UPDATE "GPU_LEASES" SET "STATE"='released',"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=? AND "DEPLOYMENT_ID"=? AND "REVISION"=? AND "GPU_UUID"=?`, `UPDATE gpu_leases SET state='released',updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND deployment_id=$4 AND revision=$5 AND gpu_uuid=$6`, `UPDATE gpu_leases SET state='released',updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND deployment_id=? AND revision=? AND gpu_uuid=?`, n.TenantID, n.WorkspaceID, n.ID, command.DeploymentID, command.Revision, command.GPUUUID))
		if e != nil {
			return e
		}
		if changed != 1 {
			return fail("INFERENCE_OWNERSHIP_CONFLICT")
		}
		if command.Action == "remove" {
			changed, e = q.Exec(ctx, deploymentNativeSQL(q, `UPDATE "DEPLOYMENTS" SET "REMOVED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=? AND "ID"=? AND "REVISION"=? AND "LAST_TASK_ID"=?`, `UPDATE deployments SET removed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4 AND revision=$5 AND last_task_id=$6`, `UPDATE deployments SET removed_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? AND revision=? AND last_task_id=?`, n.TenantID, n.WorkspaceID, n.ID, command.DeploymentID, command.Revision, t.ID))
			if e != nil {
				return e
			}
			if changed != 1 {
				return fail("AGENT_LEASE_STALE")
			}
		}
	}
	return nil
}

// Restore uses the operator's existing real transaction and retains physical ownership.
func InvalidateAuthoritiesWithin(ctx context.Context, q transaction.Executor) error {
	if ctx == nil || q == nil || q.Dialect() != transaction.PostgreSQL && q.Dialect() != transaction.MySQL && q.Dialect() != transaction.Dameng {
		return fail("STORAGE_UNAVAILABLE")
	}
	if q.Dialect() == transaction.Dameng {
		if _, pool := q.(transaction.Store); pool {
			return fail("STORAGE_UNAVAILABLE")
		}
	}
	if _, e := q.Exec(ctx, deploymentNativeSQL(q, `UPDATE "GPU_LEASES" SET "STATE"='recovery_required',"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "STATE"<>'released'`, `UPDATE gpu_leases SET state='recovery_required',updated_at=clock_timestamp() WHERE state<>'released'`, `UPDATE gpu_leases SET state='recovery_required',updated_at=UTC_TIMESTAMP(6) WHERE state<>'released'`)); e != nil {
		return e
	}
	_, e := q.Exec(ctx, deploymentNativeSQL(q, `UPDATE "DEPLOYMENTS" SET "OBSERVED_STATE"='unknown',"OBSERVATION"=NULL,"OBSERVED_AT"=NULL,"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "REMOVED_AT" IS NULL`, `UPDATE deployments SET observed_state='unknown',observation=NULL,observed_at=NULL,updated_at=clock_timestamp() WHERE removed_at IS NULL`, `UPDATE deployments SET observed_state='unknown',observation=NULL,observed_at=NULL,updated_at=UTC_TIMESTAMP(6) WHERE removed_at IS NULL`))
	return e
}
