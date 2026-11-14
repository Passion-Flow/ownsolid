package training

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/jackc/pgx/v5"
)

const mysqlAcceptedJobFields = `id,workspace_id,node_id,current_node_id,model_id,dataset_id,dataset_version_id,gpu_uuid,configuration,configuration_sha256,execution_recipe,dataset_manifest_sha256,model_catalog_sha256,created_by,reason,state,wait_code,revision,attempt_number,current_attempt_id,accepted_artifact_id,cancel_requested_at,error_code,created_at,updated_at,finished_at,pending_recovery_id`

func acceptedAdapterSQL(q transaction.Executor, pg, my string, args ...any) transaction.Statement {
	if q.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, my, args...)
	}
	return transaction.SQL(transaction.PostgreSQL, pg, args...)
}
func acceptedAdapterError(e error) error {
	if e == nil {
		return nil
	}
	if errors.Is(e, context.Canceled) {
		return fail(string(transaction.Cancelled))
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return fail(string(transaction.Timeout))
	}
	switch e.Error() {
	case "TRAINING_NOT_FOUND", "TRAINING_DATA_INVALID", "TRAINING_RESULT_UNAVAILABLE", "TRAINING_STORAGE_UNAVAILABLE", "TRAINING_IDENTITY_REQUIRED", "TRAINING_STORAGE_MISMATCH", string(transaction.Cancelled), string(transaction.Timeout), string(transaction.LockTimeout), string(transaction.Deadlock):
		return fail(e.Error())
	default:
		return fail("STORAGE_UNAVAILABLE")
	}
}
func (s *Service) acceptedAdapterExecutor(ctx context.Context, q transaction.Executor, tenant, workspace, job string) error {
	if s == nil || ctx == nil || q == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	if e := ctx.Err(); e != nil {
		return acceptedAdapterError(e)
	}
	for _, id := range []string{tenant, workspace, job} {
		if !datawire.ValidID(id) {
			return fail("TRAINING_NOT_FOUND")
		}
	}
	if s.Identity == nil || s.Workspaces == nil || s.Workspaces.Identity != s.Identity {
		return fail("TRAINING_IDENTITY_REQUIRED")
	}
	if s.DB != nil && s.DB != s.Identity.DB || s.Workspaces.DB != nil && s.Workspaces.DB != s.Identity.DB {
		return fail("TRAINING_STORAGE_MISMATCH")
	}
	store, e := s.Identity.Repository()
	if e != nil {
		return acceptedAdapterError(e)
	}
	if store.Dialect() != q.Dialect() || q.Dialect() != transaction.MySQL && q.Dialect() != transaction.PostgreSQL {
		return fail("TRAINING_STORAGE_MISMATCH")
	}
	if s.Store == nil {
		return fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	return nil
}

// AcceptedAdapterWithin borrows current actor/Workspace fences and never opens
// files or commits. Exact stopped-result metadata is not physical/GPU evidence.
func (s *Service) AcceptedAdapterWithin(ctx context.Context, q transaction.Executor, tenant, workspace, job string) (AcceptedAdapter, error) {
	var out AcceptedAdapter
	if e := s.acceptedAdapterExecutor(ctx, q, tenant, workspace, job); e != nil {
		return out, acceptedAdapterError(e)
	}
	j, e := scanJob(q.QueryRow(ctx, acceptedAdapterSQL(q,
		"SELECT "+jobFields+" FROM training_jobs WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE",
		"SELECT "+mysqlAcceptedJobFields+" FROM training_jobs WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE", tenant, workspace, job)))
	if e != nil {
		return out, acceptedAdapterError(e)
	}
	record, e := readAcceptedAdapterWithin(ctx, q, tenant, j)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, fail("TRAINING_RESULT_UNAVAILABLE")
	}
	return record.AcceptedAdapter, acceptedAdapterError(e)
}

type acceptedAdapterRecord struct {
	AcceptedAdapter
	result     wire.Result
	stop       wire.StopReceipt
	stopSHA256 string
	createdAt  time.Time
}

// readAcceptedAdapterWithin reconciles an already scoped, locked job with its
// exact stopped attempt, result and inventory. It does not open artifact files.
func readAcceptedAdapterWithin(ctx context.Context, q transaction.Executor, tenant string, j Job) (acceptedAdapterRecord, error) {
	var out acceptedAdapterRecord
	if j.State != "succeeded" || j.AcceptedArtifactID == nil || j.CurrentAttemptID == nil {
		return out, fail("TRAINING_RESULT_UNAVAILABLE")
	}
	var inventory, metadata, payload, stopRaw, attemptInputs, attemptResult []byte
	var manifest, storage, task, node, stopSHA, attemptState string
	var stopped *time.Time
	var attemptOutcome *string
	e := q.QueryRow(ctx, acceptedAdapterSQL(q, `SELECT r.file_inventory,u.manifest_bytes,r.manifest_sha256,r.storage_key,r.created_at,t.payload,a.physical_stop_receipt,a.physical_stop_sha256,a.physical_stopped_at,a.state,a.task_id::text,a.node_id::text,a.outcome_sha256,a.input_inventory,a.outcome
 FROM training_artifacts r
 JOIN training_uploads u ON u.id=r.id AND u.tenant_id=r.tenant_id AND u.workspace_id=r.workspace_id AND u.job_id=r.job_id AND u.attempt_id=r.attempt_id AND u.state='accepted' AND u.kind='artifact' AND u.manifest_sha256=r.manifest_sha256
 JOIN training_attempts a ON a.id=r.attempt_id AND a.job_id=r.job_id AND a.tenant_id=r.tenant_id AND a.workspace_id=r.workspace_id
 JOIN node_tasks t ON t.id=a.task_id AND t.tenant_id=a.tenant_id AND t.workspace_id=a.workspace_id AND t.node_id=a.node_id AND t.operation='training.run.v1'
 WHERE r.tenant_id=$1 AND r.workspace_id=$2 AND r.job_id=$3 AND r.id=$4 AND r.attempt_id=$5
 AND a.physical_stopped_at IS NOT NULL FOR SHARE OF r,u,a,t`, `SELECT r.file_inventory,u.manifest_bytes,r.manifest_sha256,r.storage_key,r.created_at,t.payload,a.physical_stop_receipt,a.physical_stop_sha256,a.physical_stopped_at,a.state,a.task_id,a.node_id,a.outcome_sha256,a.input_inventory,a.outcome
 FROM training_artifacts r
 JOIN training_uploads u ON u.id=r.id AND u.tenant_id=r.tenant_id AND u.workspace_id=r.workspace_id AND u.job_id=r.job_id AND u.attempt_id=r.attempt_id AND u.state='accepted' AND u.kind='artifact' AND u.manifest_sha256=r.manifest_sha256
 JOIN training_attempts a ON a.id=r.attempt_id AND a.job_id=r.job_id AND a.tenant_id=r.tenant_id AND a.workspace_id=r.workspace_id
 JOIN node_tasks t ON t.id=a.task_id AND t.tenant_id=a.tenant_id AND t.workspace_id=a.workspace_id AND t.node_id=a.node_id AND t.operation='training.run.v1'
 WHERE r.tenant_id=? AND r.workspace_id=? AND r.job_id=? AND r.id=? AND r.attempt_id=?
 AND a.physical_stopped_at IS NOT NULL FOR SHARE`, tenant, j.WorkspaceID, j.ID, *j.AcceptedArtifactID, *j.CurrentAttemptID)).Scan(&inventory, &metadata, &manifest, &storage, &out.createdAt, &payload, &stopRaw, &stopSHA, &stopped, &attemptState, &task, &node, &attemptOutcome, &attemptInputs, &attemptResult)
	if e != nil {
		return acceptedAdapterRecord{}, e
	}
	var command wire.Command
	var descriptor UploadDescriptor
	var stop wire.StopReceipt
	if wire.DecodeStrict(payload, &command) != nil || command.Validate() != nil || command.Scope != (wire.Scope{TenantID: tenant, WorkspaceID: j.WorkspaceID, JobID: j.ID}) || command.AttemptID != *j.CurrentAttemptID || command.AttemptNumber != j.AttemptNumber || command.ModelID != j.ModelID || command.ModelCatalogSHA256 != j.ModelCatalogSHA256 || command.DatasetID != j.DatasetID || command.DatasetVersionID != j.DatasetVersionID || command.DatasetManifestSHA256 != j.DatasetManifestSHA256 || command.GPUUUID != j.GPUUUID || command.Configuration != j.Configuration || command.ConfigurationSHA256 != j.ConfigurationSHA256 || !reflect.DeepEqual(command.Recipe, j.Recipe) || node != j.CurrentNodeID || attemptState != "succeeded" || stopped == nil {
		return out, fail("TRAINING_DATA_INVALID")
	}
	var inputs wire.InputInventory
	if wire.DecodeStrict(attemptInputs, &inputs) != nil || inputs != command.Inputs {
		return out, fail("TRAINING_DATA_INVALID")
	}
	if wire.DecodeStrict(inventory, &descriptor) != nil || descriptor.Validate(j.Recipe.Resources.MaximumOutputBytes, j.Recipe.Resources.MaximumOutputFiles) != nil || descriptor.Kind != "artifact" || descriptor.UploadID != *j.AcceptedArtifactID || descriptor.AttemptID != command.AttemptID || descriptor.Scope != command.Scope || descriptor.ManifestSHA256 != manifest || storage != publishedDirectory(descriptor) || len(descriptor.Files) != 3 {
		return out, fail("TRAINING_DATA_INVALID")
	}
	if wire.DecodeStrict(stopRaw, &stop) != nil || stop.ValidateFor(command, node, task) != nil || stop.Proof != "owned_container_removed" || stop.AgentErrorCode != "" || stop.OutcomeSHA256 == nil || *stop.OutcomeSHA256 != manifest || attemptOutcome == nil || *attemptOutcome != manifest {
		return out, fail("TRAINING_DATA_INVALID")
	}
	canonicalStop, _ := json.Marshal(stop)
	if rawDigest(canonicalStop) != stopSHA {
		return out, fail("TRAINING_DATA_INVALID")
	}
	result, e := decodeAcceptedArtifactResult(command, descriptor, metadata, attemptResult)
	if e != nil {
		return out, e
	}
	j.CreatedAt, j.UpdatedAt = j.CreatedAt.UTC(), j.UpdatedAt.UTC()
	if j.FinishedAt != nil {
		stamp := j.FinishedAt.UTC()
		j.FinishedAt = &stamp
	}
	if j.CancelRequestedAt != nil {
		stamp := j.CancelRequestedAt.UTC()
		j.CancelRequestedAt = &stamp
	}
	out.AcceptedAdapter = AcceptedAdapter{Job: j, Command: command, Descriptor: descriptor}
	out.result = result
	out.stop = stop
	out.stopSHA256 = stopSHA
	return out, nil
}
