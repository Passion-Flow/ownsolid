package modelpreparations

import (
	"database/sql"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func commandForPreparation(p Preparation, w workspaces.Workspace, attempt string, number int) wire.Command {
	source := p.inputs.TrainingCommand
	return wire.Command{SchemaVersion: 1, Scope: wire.Scope{TenantID: w.TenantID, WorkspaceID: w.ID, PreparationID: p.ID}, AttemptID: attempt, AttemptNumber: number, ModelID: p.ModelID, ModelCatalogSHA256: source.ModelCatalogSHA256, TrainingJobID: p.TrainingJobID, TrainingAttemptID: p.TrainingAttemptID, TrainingConfiguration: source.Configuration, TrainingConfigurationSHA256: source.ConfigurationSHA256, HeldoutID: p.HeldoutID, HeldoutManifestSHA256: p.HeldoutManifestSHA256, GPUUUID: p.GPUUUID, Configuration: p.Configuration, ConfigurationSHA256: p.ConfigurationSHA256, Recipe: p.Recipe, Inputs: p.inputs.inventory()}
}

func (s *Service) resourcePreflight(ctx context.Context, tx transaction.Executor, n nodes.Node, gpu string) error {
	if n.Status != "active" || n.Connectivity != "online" {
		return fail("NODE_UNAVAILABLE")
	}
	if n.Facts.OSID != wire.NodeOSID || n.Facts.OSVersion != wire.NodeOSVersion || n.Facts.Architecture != wire.NodeArchitecture {
		return fail("MODEL_PREPARATION_PLATFORM_UNSUPPORTED")
	}
	p := s.policy()
	if n.Facts.MemoryTotalBytes < p.MinimumHostMemory {
		return fail("MODEL_PREPARATION_HOST_MEMORY_LOW")
	}
	if n.Facts.DiskAvailableBytes < p.MinimumFreeBytes {
		return fail("MODEL_PREPARATION_STORAGE_FULL")
	}
	if s.Inference == nil {
		return fail("MODEL_PREPARATION_PREFLIGHT_REQUIRED")
	}
	observed, e := s.Inference.HardwareWithin(ctx, tx, n)
	if e != nil {
		if e.Error() == "TRAINING_PREFLIGHT_REQUIRED" {
			return fail("MODEL_PREPARATION_PREFLIGHT_REQUIRED")
		}
		return e
	}
	if observed.ProbeErrorCode != "" {
		return fail("MODEL_PREPARATION_PREFLIGHT_REQUIRED")
	}
	for _, card := range observed.GPUs {
		if card.UUID != gpu {
			continue
		}
		major, e := strconv.Atoi(strings.Split(card.DriverVersion, ".")[0])
		if e != nil || major < p.MinimumDriverMajor {
			return fail("MODEL_PREPARATION_DRIVER_UNSUPPORTED")
		}
		if card.ComputeCapability < float64(p.MinimumComputeMajor) {
			return fail("MODEL_PREPARATION_GPU_UNSUPPORTED")
		}
		if card.MIGMode == "Enabled" {
			return fail("MODEL_PREPARATION_MIG_UNSUPPORTED")
		}
		if card.MemoryTotalMiB < p.MinimumGPUMemoryMiB || card.MemoryFreeMiB < p.MinimumGPUMemoryMiB {
			return fail("MODEL_PREPARATION_GPU_MEMORY_LOW")
		}
		return nil
	}
	return fail("MODEL_PREPARATION_GPU_UNAVAILABLE")
}
func (s *Service) wait(ctx context.Context, tx transaction.Executor, p Preparation, code string) error {
	_, e := tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATIONS" SET "REVISION"="REVISION"+CASE WHEN "WAIT_CODE"=? THEN 0 ELSE 1 END,"UPDATED_AT"=CASE WHEN "WAIT_CODE"=? THEN "UPDATED_AT" ELSE SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) END,"WAIT_CODE"=?,"NEXT_DISPATCH_AT"=DATEADD(MICROSECOND,?*1000,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))) WHERE "ID"=?`), `UPDATE model_preparations SET wait_code=$2,next_dispatch_at=clock_timestamp()+make_interval(secs=>$3::double precision/1000),revision=revision+CASE WHEN wait_code IS DISTINCT FROM $2::varchar THEN 1 ELSE 0 END,updated_at=CASE WHEN wait_code IS DISTINCT FROM $2::varchar THEN clock_timestamp() ELSE updated_at END WHERE id=$1`, `UPDATE model_preparations SET revision=revision+CASE WHEN NOT (wait_code <=> ?) THEN 1 ELSE 0 END,updated_at=CASE WHEN NOT (wait_code <=> ?) THEN UTC_TIMESTAMP(6) ELSE updated_at END,wait_code=?,next_dispatch_at=TIMESTAMPADD(MICROSECOND,?*1000,UTC_TIMESTAMP(6)) WHERE id=?`, []any{p.ID, code, s.policy().PollMilliseconds}, []any{code, code, code, s.policy().PollMilliseconds, p.ID}))
	return e
}
func (s *Service) failQueued(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, p Preparation, code string) error {
	if _, e := tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATIONS" SET "STATE"='failed',"ERROR_CODE"=?,"WAIT_CODE"=NULL,"REVISION"="REVISION"+1,"FINISHED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "ID"=?`), `UPDATE model_preparations SET state='failed',error_code=$2,wait_code=NULL,revision=revision+1,finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`, `UPDATE model_preparations SET state='failed',error_code=?,wait_code=NULL,revision=revision+1,finished_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{p.ID, code}, []any{code, p.ID})); e != nil {
		return e
	}
	return preparationAuditWithin(ctx, tx, a, w, "model.preparation_queue_failed", p.ID, code, uuid.NewString())
}

// DispatchNext takes one exact shared physical owner and queues one immutable command atomically.
// It performs no container side effect, preemption, implicit retry or target simulation.
func (s *Service) DispatchNext(ctx context.Context) (bool, error) {
	c, e := s.nextCandidate(ctx)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	worked := false
	e = s.delegatedTransaction(ctx, c, func(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, n nodes.Node, p Preparation, valid bool) error {
		if p.State != "queued" {
			return nil
		}
		if !valid {
			worked = true
			return s.failQueued(ctx, tx, a, w, p, "MODEL_PREPARATION_ACTOR_UNAVAILABLE")
		}
		if n.Status != "active" {
			return s.wait(ctx, tx, p, "NODE_UNAVAILABLE")
		}
		if p.AttemptNumber >= s.policy().MaxAttempts {
			worked = true
			return s.failQueued(ctx, tx, a, w, p, "MODEL_PREPARATION_ATTEMPTS_EXHAUSTED")
		}
		if e = s.pendingRecovery(ctx, tx, p, c); e != nil {
			return e
		}
		installed, e := s.executionRecipe()
		if e != nil {
			return s.wait(ctx, tx, p, "MODEL_PREPARATION_UNAVAILABLE")
		}
		frozen, _ := json.Marshal(p.Recipe)
		current, _ := json.Marshal(installed)
		if !same(frozen, current) {
			return s.wait(ctx, tx, p, "MODEL_PREPARATION_RECIPE_UNAVAILABLE")
		}
		if e = s.resourcePreflight(ctx, tx, n, p.GPUUUID); e != nil {
			switch e.Error() {
			case "NODE_UNAVAILABLE", "MODEL_PREPARATION_PLATFORM_UNSUPPORTED", "MODEL_PREPARATION_HOST_MEMORY_LOW", "MODEL_PREPARATION_STORAGE_FULL", "MODEL_PREPARATION_PREFLIGHT_REQUIRED", "MODEL_PREPARATION_DRIVER_UNSUPPORTED", "MODEL_PREPARATION_GPU_UNSUPPORTED", "MODEL_PREPARATION_MIG_UNSUPPORTED", "MODEL_PREPARATION_GPU_MEMORY_LOW", "MODEL_PREPARATION_GPU_UNAVAILABLE":
				return s.wait(ctx, tx, p, e.Error())
			default:
				return e
			}
		}
		input := CreateRequest{WorkspaceID: w.ID, TrainingJobID: p.TrainingJobID, HeldoutID: p.HeldoutID, NodeID: n.ID, GPUUUID: p.GPUUUID, Configuration: p.Configuration, Reason: p.Reason}
		sources, _, e := s.preparationSourcesWithin(ctx, tx, w, input)
		if e != nil {
			return e
		}
		if sources.digest() != p.InputSHA256 {
			worked = true
			return s.failQueued(ctx, tx, a, w, p, "MODEL_PREPARATION_INPUT_CHANGED")
		}
		attempt := uuid.NewString()
		command := commandForPreparation(p, w, attempt, p.AttemptNumber+1)
		commandSHA, e := command.Digest()
		if e != nil {
			return e
		}
		e = preparationSavepoint(ctx, tx, "begin")
		if e != nil {
			return e
		}
		reserved, e := reservePreparationGPU(ctx, tx, w, n, p, command.AttemptNumber)
		if e != nil {
			if rollbackError := preparationSavepoint(ctx, tx, "rollback"); rollbackError != nil {
				return rollbackError
			}
			var conflict *transaction.Error
			if errors.As(e, &conflict) && conflict.Kind == transaction.UniqueConflict {
				return s.wait(ctx, tx, p, "MODEL_PREPARATION_RESOURCE_BUSY")
			}
			return e
		}
		if !reserved {
			if e = preparationSavepoint(ctx, tx, "rollback"); e != nil {
				return e
			}
			return s.wait(ctx, tx, p, "MODEL_PREPARATION_PHYSICAL_STOP_REQUIRED")
		}
		var deadline time.Time
		if e = preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT DATEADD(SECOND,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))) FROM DUAL`), "SELECT clock_timestamp()+make_interval(secs=>$1)", `SELECT TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6))`, p.Recipe.Resources.WallSeconds)).Scan(&deadline); e != nil {
			preparationSavepoint(ctx, tx, "rollback")
			return e
		}
		task, e := s.Nodes.QueuePreparationWithin(ctx, tx, n, command, c.actor, deadline)
		if e != nil {
			if rollbackError := preparationSavepoint(ctx, tx, "rollback"); rollbackError != nil {
				return rollbackError
			}
			if e.Error() == "NODE_BUSY" {
				return s.wait(ctx, tx, p, "NODE_BUSY")
			}
			return e
		}
		if _, e = tx.Exec(ctx, preparationSelectedSQL(tx, preparationDM(`INSERT INTO "MODEL_PREPARATION_ATTEMPTS"("ID","TENANT_ID","WORKSPACE_ID","PREPARATION_ID","NODE_ID","TASK_ID","ATTEMPT_NUMBER","STATE","REQUESTED_BY","REQUESTED_AUDIENCE","REQUESTED_EPOCH","MEMBERSHIP_AUTHORITY","COMMAND_SHA256") VALUES(?,?,?,?,?,?,?,'pending',?,?,?,?,?)`), `INSERT INTO model_preparation_attempts(id,tenant_id,workspace_id,preparation_id,node_id,task_id,attempt_number,state,requested_by,requested_audience,requested_epoch,membership_authority,command_sha256) VALUES($1,$2,$3,$4,$5,$6,$7,'pending',$8,$9,$10,$11,$12)`, `INSERT INTO model_preparation_attempts(id,tenant_id,workspace_id,preparation_id,node_id,task_id,attempt_number,state,requested_by,requested_audience,requested_epoch,membership_authority,command_sha256) VALUES(?,?,?,?,?,?,?,'pending',?,?,?,?,?)`, attempt, w.TenantID, w.ID, p.ID, n.ID, task.ID, command.AttemptNumber, c.actor, c.audience, c.epoch, c.membership, commandSHA)); e != nil {
			preparationSavepoint(ctx, tx, "rollback")
			return e
		}
		if p.PendingRecoveryID != nil {
			tag, e := tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATION_RECOVERIES" SET "DISPATCHED_ATTEMPT_ID"=?,"APPLIED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "ID"=? AND "APPLIED_AT" IS NULL`), "UPDATE model_preparation_recoveries SET dispatched_attempt_id=$2,applied_at=clock_timestamp() WHERE id=$1 AND applied_at IS NULL", `UPDATE model_preparation_recoveries SET dispatched_attempt_id=?,applied_at=UTC_TIMESTAMP(6) WHERE id=? AND applied_at IS NULL`, []any{*p.PendingRecoveryID, attempt}, []any{attempt, *p.PendingRecoveryID}))
			if e != nil {
				preparationSavepoint(ctx, tx, "rollback")
				return e
			}
			if tag != 1 {
				preparationSavepoint(ctx, tx, "rollback")
				return fail("MODEL_PREPARATION_ATTEMPT_STALE")
			}
		}
		if _, e = tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATIONS" SET "STATE"='preparing',"PENDING_RECOVERY_ID"=NULL,"CURRENT_ATTEMPT_ID"=?,"ATTEMPT_NUMBER"="ATTEMPT_NUMBER"+1,"WAIT_CODE"=NULL,"ERROR_CODE"=NULL,"REVISION"="REVISION"+1,"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "ID"=?`), `UPDATE model_preparations SET state='preparing',pending_recovery_id=NULL,current_attempt_id=$2,attempt_number=attempt_number+1,wait_code=NULL,error_code=NULL,revision=revision+1,updated_at=clock_timestamp() WHERE id=$1`, `UPDATE model_preparations SET state='preparing',pending_recovery_id=NULL,current_attempt_id=?,attempt_number=attempt_number+1,wait_code=NULL,error_code=NULL,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{p.ID, attempt}, []any{attempt, p.ID})); e != nil {
			preparationSavepoint(ctx, tx, "rollback")
			return e
		}
		if e = preparationAuditWithin(ctx, tx, a, w, "model.preparation_dispatched", p.ID, "Current authorized immutable attempt reserved one physical GPU", uuid.NewString()); e != nil {
			preparationSavepoint(ctx, tx, "rollback")
			return e
		}
		if e = preparationSavepoint(ctx, tx, "release"); e != nil {
			return e
		}
		worked = true
		return nil
	})
	if e != nil {
		return false, e
	}
	return worked, nil
}
