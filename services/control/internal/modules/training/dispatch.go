package training

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) resourcePreflight(ctx context.Context, tx pgx.Tx, n nodes.Node, gpu string) error {
	return s.resourcePreflightWithin(ctx, transaction.PostgreSQLTransaction(tx), n, gpu)
}
func (s *Service) resourcePreflightWithin(ctx context.Context, q transaction.Executor, n nodes.Node, gpu string) error {
	if n.Status != "active" || n.Connectivity != "online" {
		return fail("NODE_UNAVAILABLE")
	}
	if n.Facts.OSID != "ubuntu" || n.Facts.OSVersion != "24.04" || n.Facts.Architecture != "amd64" {
		return fail("TRAINING_PLATFORM_UNSUPPORTED")
	}
	p := s.policy()
	if n.Facts.MemoryTotalBytes < p.MinimumHostMemory {
		return fail("TRAINING_HOST_MEMORY_LOW")
	}
	if s.Inference == nil {
		return fail("TRAINING_PREFLIGHT_REQUIRED")
	}
	if s.Inference.Nodes != s.Nodes || s.Nodes == nil || s.Nodes.Identity != s.Identity {
		return fail("TRAINING_STORAGE_MISMATCH")
	}
	o, e := s.Inference.HardwareWithin(ctx, q, n)
	if e != nil {
		return e
	}
	if o.ProbeErrorCode != "" {
		return fail("TRAINING_PREFLIGHT_REQUIRED")
	}
	for _, observed := range o.GPUs {
		if observed.UUID != gpu {
			continue
		}
		major, e := strconv.Atoi(strings.Split(observed.DriverVersion, ".")[0])
		if e != nil || major < p.MinimumDriverMajor {
			return fail("TRAINING_DRIVER_UNSUPPORTED")
		}
		if int(observed.ComputeCapability) < p.MinimumComputeMajor {
			return fail("TRAINING_GPU_UNSUPPORTED")
		}
		if observed.MIGMode == "Enabled" {
			return fail("TRAINING_MIG_UNSUPPORTED")
		}
		if observed.MemoryTotalMiB < p.MinimumGPUMemoryMiB {
			return fail("TRAINING_GPU_MEMORY_LOW")
		}
		return nil
	}
	return fail("TRAINING_GPU_UNAVAILABLE")
}
func (s *Service) wait(ctx context.Context, tx pgx.Tx, j Job, code string) error {
	return s.waitWithin(ctx, transaction.PostgreSQLTransaction(tx), j, code)
}
func (s *Service) waitWithin(ctx context.Context, q transaction.Executor, j Job, code string) error {
	args := []any{j.ID, code, s.policy().PollMilliseconds, j.WorkspaceID}
	if q.Dialect() == transaction.MySQL {
		args = []any{code, code, code, s.policy().PollMilliseconds, j.WorkspaceID, j.ID}
	}
	n, e := q.Exec(ctx, acceptedAdapterSQL(q,
		`UPDATE training_jobs SET wait_code=$2,next_dispatch_at=clock_timestamp()+make_interval(secs=>$3::double precision/1000),revision=revision+CASE WHEN wait_code IS DISTINCT FROM $2::varchar THEN 1 ELSE 0 END,updated_at=CASE WHEN wait_code IS DISTINCT FROM $2::varchar THEN clock_timestamp() ELSE updated_at END WHERE id=$1 AND workspace_id=$4 AND tenant_id=(SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete)`,
		`UPDATE training_jobs SET revision=revision+CASE WHEN NOT(wait_code <=> ?) THEN 1 ELSE 0 END,updated_at=CASE WHEN NOT(wait_code <=> ?) THEN UTC_TIMESTAMP(6) ELSE updated_at END,wait_code=?,next_dispatch_at=TIMESTAMPADD(MICROSECOND,?*1000,UTC_TIMESTAMP(6)) WHERE workspace_id=? AND id=? AND tenant_id=(SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete)`, args...))
	if e == nil && n != 1 {
		return fail("STORAGE_UNAVAILABLE")
	}
	return e
}
func (s *Service) failQueued(ctx context.Context, tx pgx.Tx, a identity.Auth, w workspaces.Workspace, j Job, code string) error {
	return s.failQueuedWithin(ctx, transaction.PostgreSQLTransaction(tx), a, w, j, code)
}
func (s *Service) failQueuedWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, j Job, code string) error {
	n, e := q.Exec(ctx, acceptedAdapterSQL(q,
		`UPDATE training_jobs SET state='failed',error_code=$2,wait_code=NULL,pending_recovery_id=NULL,revision=revision+1,finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1 AND tenant_id=$3 AND workspace_id=$4`,
		`UPDATE training_jobs SET state='failed',error_code=?,wait_code=NULL,pending_recovery_id=NULL,revision=revision+1,finished_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=? AND tenant_id=? AND workspace_id=?`, dispatchFailureArgs(q, w, j, code)...))
	if e != nil {
		return e
	}
	if n != 1 {
		return fail("STORAGE_UNAVAILABLE")
	}
	return delegatedAuditWithin(ctx, q, a, w, "training.queue_failed", j.ID, code)
}
func dispatchFailureArgs(q transaction.Executor, w workspaces.Workspace, j Job, code string) []any {
	if q.Dialect() == transaction.MySQL {
		return []any{code, j.ID, w.TenantID, w.ID}
	}
	return []any{j.ID, code, w.TenantID, w.ID}
}
func delegatedAuditWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, action, id, reason string) error {
	n, e := q.Exec(ctx, acceptedAdapterSQL(q,
		`INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,$5,$6,'success','training_job',$7,$8,$9)`,
		`INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,?,?,'success','training_job',?,?,?)`, uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, a.Me.Audience, action, id, reason, uuid.NewString()))
	if e == nil && n != 1 {
		return fail("STORAGE_UNAVAILABLE")
	}
	return e
}

type dispatchSnapshot struct {
	revision int64
	input    datasets.TrainingInputs
	command  wire.Command
	resumeID *string
}

func trainingInputUnavailable(e error) bool {
	if e == nil {
		return false
	}
	switch e.Error() {
	case "DATASET_RETIRED", "DATASET_NOT_FOUND", "DATASET_DATA_INVALID", "DATASET_FILE_INVALID", "DATASET_FILE_CHANGED", "TRAINING_DATASET_EMPTY_PARTITION":
		return true
	}
	return false
}

// dispatchSnapshotWithin either records an observable non-executing outcome, or
// freezes metadata. It performs no filesystem work and grants no physical owner.
func (s *Service) dispatchSnapshotWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, n nodes.Node, j Job, valid bool, attempt string) (*dispatchSnapshot, bool, error) {
	if j.CurrentNodeID != n.ID {
		return nil, false, fail("TRAINING_ATTEMPT_STALE")
	}
	if j.State != "queued" {
		return nil, false, nil
	}
	if !valid {
		return nil, true, s.failQueuedWithin(ctx, q, a, w, j, "TRAINING_ACTOR_UNAVAILABLE")
	}
	if n.Status != "active" {
		return nil, false, s.waitWithin(ctx, q, j, "NODE_UNAVAILABLE")
	}
	if j.AttemptNumber >= s.policy().MaxAttempts {
		return nil, true, s.failQueuedWithin(ctx, q, a, w, j, "TRAINING_ATTEMPTS_EXHAUSTED")
	}
	current, e := s.recipe()
	if e != nil {
		return nil, false, s.waitWithin(ctx, q, j, "TRAINING_UNAVAILABLE")
	}
	if !reflect.DeepEqual(j.Recipe, current) {
		return nil, false, s.waitWithin(ctx, q, j, "TRAINING_RECIPE_UNAVAILABLE")
	}
	if e = s.resourcePreflightWithin(ctx, q, n, j.GPUUUID); e != nil {
		switch e.Error() {
		case "NODE_UNAVAILABLE", "TRAINING_PREFLIGHT_REQUIRED", "TRAINING_PLATFORM_UNSUPPORTED", "TRAINING_HOST_MEMORY_LOW", "TRAINING_DRIVER_UNSUPPORTED", "TRAINING_GPU_UNSUPPORTED", "TRAINING_MIG_UNSUPPORTED", "TRAINING_GPU_MEMORY_LOW", "TRAINING_GPU_UNAVAILABLE":
			return nil, false, s.waitWithin(ctx, q, j, e.Error())
		}
		return nil, false, e
	}
	if s.Models.Identity != s.Identity || s.Datasets.Identity != s.Identity {
		return nil, false, fail("TRAINING_STORAGE_MISMATCH")
	}
	if e = s.Models.ReadyWithin(ctx, q, w.TenantID, w.ID, j.ModelID); e != nil {
		if e.Error() == "RESOURCE_NOT_FOUND" {
			return nil, true, s.failQueuedWithin(ctx, q, a, w, j, "TRAINING_MODEL_UNAVAILABLE")
		}
		return nil, false, e
	}
	input, e := s.Datasets.TrainingInputSnapshotWithin(ctx, q, w.TenantID, w.ID, j.DatasetID, j.DatasetVersionID, j.AttemptNumber > 0)
	if e != nil {
		if trainingInputUnavailable(e) {
			return nil, true, s.failQueuedWithin(ctx, q, a, w, j, "TRAINING_INPUT_UNAVAILABLE")
		}
		return nil, false, e
	}
	if input.Version.ManifestSHA256 != j.DatasetManifestSHA256 {
		return nil, true, s.failQueuedWithin(ctx, q, a, w, j, "TRAINING_INPUT_UNAVAILABLE")
	}
	command := commandForJob(j, w, attempt, j.AttemptNumber+1, wire.InputInventory{Train: input.Train, Validation: input.Validation})
	var resumeID *string
	if j.PendingRecoveryID != nil {
		var mode, target string
		var previous, next, checkpoint *string
		e = q.QueryRow(ctx, acceptedAdapterSQL(q,
			"SELECT mode,next_node_id::text,previous_attempt_id::text,next_attempt_id::text,checkpoint_id::text FROM training_recoveries WHERE tenant_id=$1 AND workspace_id=$2 AND job_id=$3 AND id=$4 FOR UPDATE",
			"SELECT mode,next_node_id,previous_attempt_id,next_attempt_id,checkpoint_id FROM training_recoveries WHERE tenant_id=? AND workspace_id=? AND job_id=? AND id=? FOR UPDATE", w.TenantID, w.ID, j.ID, *j.PendingRecoveryID)).Scan(&mode, &target, &previous, &next, &checkpoint)
		if e != nil {
			return nil, false, e
		}
		if target != n.ID || next != nil || previous == nil || j.CurrentAttemptID == nil || *previous != *j.CurrentAttemptID || mode != "checkpoint" && mode != "restart" {
			return nil, false, fail("TRAINING_DATA_INVALID")
		}
		if mode == "checkpoint" {
			if checkpoint == nil {
				return nil, false, fail("TRAINING_CHECKPOINT_INVALID")
			}
			accepted, e := s.acceptedCheckpointWithin(ctx, q, w, j, command, *checkpoint)
			if e != nil {
				return nil, false, e
			}
			resume := accepted.resume()
			command.Resume = &resume
			resumeID = checkpoint
		} else if checkpoint != nil {
			return nil, false, fail("TRAINING_CHECKPOINT_INVALID")
		}
	} else if j.AttemptNumber > 0 {
		return nil, false, fail("TRAINING_RECOVERY_NOT_ALLOWED")
	}
	if e = command.Validate(); e != nil {
		return nil, false, fail("TRAINING_COMMAND_INVALID")
	}
	return &dispatchSnapshot{j.Revision, input, command, resumeID}, false, nil
}

const dispatchSavepoint = "training_dispatch_reservation"

func dispatchSavepointSQL(ctx context.Context, q transaction.Executor, action string) error {
	var statement string
	switch action {
	case "begin":
		statement = "SAVEPOINT " + dispatchSavepoint
	case "rollback":
		statement = "ROLLBACK TO SAVEPOINT " + dispatchSavepoint
	case "release":
		statement = "RELEASE SAVEPOINT " + dispatchSavepoint
	default:
		return fail("TRAINING_DATA_INVALID")
	}
	_, e := q.Exec(ctx, transaction.SQL(q.Dialect(), statement))
	return e
}
func dispatchRollbackWait(ctx context.Context, s *Service, q transaction.Executor, j Job, code string) error {
	if e := dispatchSavepointSQL(ctx, q, "rollback"); e != nil {
		return e
	}
	if e := dispatchSavepointSQL(ctx, q, "release"); e != nil {
		return e
	}
	return s.waitWithin(ctx, q, j, code)
}
func reserveTrainingGPUWithin(ctx context.Context, q transaction.Executor, w workspaces.Workspace, n nodes.Node, j Job, revision int) error {
	var id, state, gpu string
	e := q.QueryRow(ctx, acceptedAdapterSQL(q,
		"SELECT id::text,state,gpu_uuid FROM gpu_leases WHERE tenant_id=$1 AND workspace_id=$2 AND training_job_id=$3 FOR UPDATE",
		"SELECT id,state,gpu_uuid FROM gpu_leases WHERE tenant_id=? AND workspace_id=? AND training_job_id=? FOR UPDATE", w.TenantID, w.ID, j.ID)).Scan(&id, &state, &gpu)
	if e == nil {
		if state != "released" || gpu != j.GPUUUID {
			return fail("TRAINING_PHYSICAL_STOP_REQUIRED")
		}
		count, e := q.Exec(ctx, acceptedAdapterSQL(q,
			"UPDATE gpu_leases SET node_id=$1,revision=$2,state='held',updated_at=clock_timestamp() WHERE tenant_id=$3 AND workspace_id=$4 AND training_job_id=$5 AND id=$6 AND state='released'",
			"UPDATE gpu_leases SET node_id=?,revision=?,state='held',updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND training_job_id=? AND id=? AND state='released'", n.ID, revision, w.TenantID, w.ID, j.ID, id))
		if e != nil {
			return e
		}
		if count != 1 {
			return fail("TRAINING_PHYSICAL_STOP_REQUIRED")
		}
		return nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	count, e := q.Exec(ctx, acceptedAdapterSQL(q,
		"INSERT INTO gpu_leases(id,tenant_id,workspace_id,node_id,gpu_uuid,training_job_id,revision,state) VALUES($1,$2,$3,$4,$5,$6,$7,'held')",
		"INSERT INTO gpu_leases(id,tenant_id,workspace_id,node_id,gpu_uuid,training_job_id,revision,state) VALUES(?,?,?,?,?,?,?,'held')", uuid.NewString(), w.TenantID, w.ID, n.ID, j.GPUUUID, j.ID, revision))
	if e == nil && count != 1 {
		return fail("STORAGE_UNAVAILABLE")
	}
	return e
}
func (s *Service) reserveDispatchWithin(ctx context.Context, q transaction.Executor, c candidate, a identity.Auth, w workspaces.Workspace, n nodes.Node, j Job, snapshot dispatchSnapshot) (bool, error) {
	if e := dispatchSavepointSQL(ctx, q, "begin"); e != nil {
		return false, e
	}
	if e := reserveTrainingGPUWithin(ctx, q, w, n, j, snapshot.command.AttemptNumber); e != nil {
		if e.Error() == "TRAINING_PHYSICAL_STOP_REQUIRED" {
			return false, dispatchRollbackWait(ctx, s, q, j, e.Error())
		}
		var conflict *transaction.Error
		if errors.As(e, &conflict) && conflict.Kind == transaction.UniqueConflict {
			return false, dispatchRollbackWait(ctx, s, q, j, "TRAINING_RESOURCE_BUSY")
		}
		return false, e
	}
	waitCode, e := s.reserveSchedulingWithin(ctx, q, w, j, snapshot.command)
	if e != nil {
		return false, e
	}
	if waitCode != "" {
		return false, dispatchRollbackWait(ctx, s, q, j, waitCode)
	}
	var deadline time.Time
	if e := q.QueryRow(ctx, acceptedAdapterSQL(q, "SELECT clock_timestamp()+make_interval(secs=>$1)", "SELECT TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6))", j.Recipe.Resources.WallSeconds)).Scan(&deadline); e != nil {
		return false, e
	}
	task, e := s.Nodes.QueueTrainingWithin(ctx, q, n, snapshot.command, c.actor, deadline)
	if e != nil {
		if e.Error() == "NODE_BUSY" {
			return false, dispatchRollbackWait(ctx, s, q, j, "NODE_BUSY")
		}
		return false, e
	}
	inventory, e := json.Marshal(snapshot.command.Inputs)
	if e != nil {
		return false, e
	}
	count, e := q.Exec(ctx, acceptedAdapterSQL(q,
		`INSERT INTO training_attempts(id,tenant_id,workspace_id,job_id,node_id,task_id,attempt_number,state,requested_by,requested_audience,requested_epoch,membership_authority,input_inventory,resume_checkpoint_id) VALUES($1,$2,$3,$4,$5,$6,$7,'pending',$8,$9,$10,$11,$12,$13)`,
		`INSERT INTO training_attempts(id,tenant_id,workspace_id,job_id,node_id,task_id,attempt_number,state,requested_by,requested_audience,requested_epoch,membership_authority,input_inventory,resume_checkpoint_id) VALUES(?,?,?,?,?,?,?,'pending',?,?,?,?,?,?)`, snapshot.command.AttemptID, w.TenantID, w.ID, j.ID, n.ID, task.ID, snapshot.command.AttemptNumber, c.actor, c.audience, c.epoch, c.membership, inventory, snapshot.resumeID))
	if e != nil {
		return false, e
	}
	if count != 1 {
		return false, fail("STORAGE_UNAVAILABLE")
	}
	if j.PendingRecoveryID != nil {
		count, e = q.Exec(ctx, acceptedAdapterSQL(q, "UPDATE training_recoveries SET next_attempt_id=$1 WHERE tenant_id=$2 AND workspace_id=$3 AND job_id=$4 AND id=$5 AND next_attempt_id IS NULL", "UPDATE training_recoveries SET next_attempt_id=? WHERE tenant_id=? AND workspace_id=? AND job_id=? AND id=? AND next_attempt_id IS NULL", snapshot.command.AttemptID, w.TenantID, w.ID, j.ID, *j.PendingRecoveryID))
		if e != nil {
			return false, e
		}
		if count != 1 {
			return false, fail("TRAINING_DATA_INVALID")
		}
	}
	count, e = q.Exec(ctx, acceptedAdapterSQL(q,
		`UPDATE training_jobs SET state='preparing',current_attempt_id=$1,pending_recovery_id=NULL,attempt_number=attempt_number+1,wait_code=NULL,error_code=NULL,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$2 AND workspace_id=$3 AND id=$4`,
		`UPDATE training_jobs SET state='preparing',current_attempt_id=?,pending_recovery_id=NULL,attempt_number=attempt_number+1,wait_code=NULL,error_code=NULL,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?`, snapshot.command.AttemptID, w.TenantID, w.ID, j.ID))
	if e != nil {
		return false, e
	}
	if count != 1 {
		return false, fail("STORAGE_UNAVAILABLE")
	}
	if e = delegatedAuditWithin(ctx, q, a, w, "training.dispatched", j.ID, "Current authority and shared physical GPU reserved"); e != nil {
		return false, e
	}
	if e = dispatchSavepointSQL(ctx, q, "release"); e != nil {
		return false, e
	}
	return true, nil
}

// DispatchNext verifies immutable files outside locks and reserves exactly one
// typed attempt under repeated current authority. Unknown outcomes publish false.
func (s *Service) DispatchNext(ctx context.Context) (bool, error) {
	if ctx == nil {
		return false, fail("STORAGE_UNAVAILABLE")
	}
	c, e := s.nextCandidate(ctx)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, trainingRepositoryError(e)
	}
	attempt := uuid.NewString()
	var frozen *dispatchSnapshot
	worked := false
	e = s.delegatedRepositoryTransaction(ctx, c, func(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, n nodes.Node, j Job, valid bool) error {
		var e error
		frozen, worked, e = s.dispatchSnapshotWithin(ctx, q, a, w, n, j, valid, attempt)
		return e
	})
	if e != nil {
		return false, e
	}
	if frozen == nil {
		return worked, nil
	}
	verification := s.Datasets.VerifyTrainingInputSnapshot(ctx, frozen.input)
	if verification != nil && !trainingInputUnavailable(verification) {
		return false, trainingRepositoryError(verification)
	}
	worked = false
	e = s.delegatedRepositoryTransaction(ctx, c, func(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, n nodes.Node, j Job, valid bool) error {
		current, done, e := s.dispatchSnapshotWithin(ctx, q, a, w, n, j, valid, attempt)
		if e != nil {
			return e
		}
		if current == nil {
			worked = done
			return nil
		}
		if !reflect.DeepEqual(frozen, current) {
			return fail("TRAINING_ATTEMPT_STALE")
		}
		if verification != nil {
			worked = true
			return s.failQueuedWithin(ctx, q, a, w, j, "TRAINING_INPUT_UNAVAILABLE")
		}
		worked, e = s.reserveDispatchWithin(ctx, q, c, a, w, n, j, *current)
		return e
	})
	if e != nil {
		return false, e
	}
	return worked, nil
}
