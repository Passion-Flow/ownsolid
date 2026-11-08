package training

import (
	"context"
	"encoding/json"
	"errors"

	"database/sql"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

type undeliveredReceipt struct {
	SchemaVersion int        `json:"schema_version"`
	ReceiptID     string     `json:"receipt_id"`
	Proof         string     `json:"proof"`
	Scope         wire.Scope `json:"scope"`
	NodeID        string     `json:"node_id"`
	TaskID        string     `json:"task_id"`
	AttemptID     string     `json:"attempt_id"`
	AttemptNumber int        `json:"attempt_number"`
	CommandSHA256 string     `json:"command_sha256"`
	ClaimAttempt  int        `json:"claim_attempt"`
}

// nodeUnreachableCode is the wait code of a running job whose agent control has not heard
// from within the lease.
const nodeUnreachableCode = "TRAINING_NODE_UNREACHABLE"

// markUnreachable records a change in whether control can hear the job's agent.
func markUnreachable(ctx context.Context, tx transaction.Executor, j Job, unreachable bool) error {
	marked := j.WaitCode != nil && *j.WaitCode == nodeUnreachableCode
	if unreachable == marked || j.State != "preparing" && j.State != "running" {
		return nil
	}
	var code any
	if unreachable {
		code = nodeUnreachableCode
	}
	_, e := tx.Exec(ctx, transferStatement(tx, "UPDATE training_jobs SET wait_code=$2,revision=revision+1,updated_at=clock_timestamp() WHERE id=$1", "UPDATE training_jobs SET wait_code=?,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?", []any{j.ID, code}, []any{code, j.ID}))
	return e
}

// ReconcileNext can release only a transactionally proven never-delivered command. Delivered uncertainty always retains hardware ownership.
func (s *Service) ReconcileNext(ctx context.Context) (bool, error) {
	var c candidate
	missing := false
	e := s.trainingStorageTransaction(ctx, true, func(ctx context.Context, q transaction.Executor) error {
		e := q.QueryRow(ctx, acceptedAdapterSQL(q, `SELECT j.id::text,j.tenant_id::text,j.workspace_id::text,j.current_node_id::text,j.requested_by::text,j.requested_audience,j.requested_epoch,j.membership_authority::text FROM training_jobs j JOIN training_attempts a ON a.id=j.current_attempt_id AND a.job_id=j.id JOIN installation_state i ON i.tenant_id=j.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND j.state IN ('preparing','running','cancelling','recovery_required') AND a.physical_stopped_at IS NULL AND j.next_dispatch_at<=clock_timestamp() ORDER BY j.next_dispatch_at,j.created_at,j.id LIMIT 1`, `SELECT j.id,j.tenant_id,j.workspace_id,j.current_node_id,j.requested_by,j.requested_audience,j.requested_epoch,j.membership_authority FROM training_jobs j JOIN training_attempts a ON a.id=j.current_attempt_id AND a.job_id=j.id JOIN installation_state i ON i.tenant_id=j.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND j.state IN ('preparing','running','cancelling','recovery_required') AND a.physical_stopped_at IS NULL AND j.next_dispatch_at<=UTC_TIMESTAMP(6) ORDER BY j.next_dispatch_at,j.created_at,j.id LIMIT 1`)).Scan(&c.id, &c.tenant, &c.workspace, &c.node, &c.actor, &c.audience, &c.epoch, &c.membership)
		if errors.Is(e, sql.ErrNoRows) {
			missing = true
			return nil
		}
		return e
	})
	if missing && e == nil {
		return false, nil
	}
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	worked := false
	e = s.delegatedRepositoryTransaction(ctx, c, func(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, n nodes.Node, j Job, valid bool) error {
		if terminal(j.State) || j.CurrentAttemptID == nil {
			return nil
		}
		n, e := s.Nodes.TrainingHistoryWithin(ctx, tx, w.TenantID, w.ID, j.CurrentNodeID)
		if e != nil {
			return e
		}
		// An expired lease nobody acknowledged was never delivered (ADR 0011).
		if e = s.Nodes.ReleaseUnacknowledgedWithin(ctx, tx, n, uuid.NewString()); e != nil {
			return e
		}
		var task string
		var stopHash []byte
		var events int64
		var hasOutcome, stopped bool
		if e = tx.QueryRow(ctx, transferStatement(tx, "SELECT task_id::text,stop_token_hash,last_event_sequence,outcome IS NOT NULL,physical_stopped_at IS NOT NULL FROM training_attempts WHERE id=$1 AND job_id=$2 FOR UPDATE", "SELECT task_id,stop_token_hash,last_event_sequence,outcome IS NOT NULL,physical_stopped_at IS NOT NULL FROM training_attempts WHERE id=? AND job_id=? FOR UPDATE", []any{*j.CurrentAttemptID, j.ID}, []any{*j.CurrentAttemptID, j.ID})).Scan(&task, &stopHash, &events, &hasOutcome, &stopped); e != nil {
			return e
		}
		if stopped {
			return nil
		}
		t, command, e := s.Nodes.TrainingStopTaskWithin(ctx, tx, n, task)
		if e != nil {
			return e
		}
		if e = bindFrozenCommand(j, command); e != nil {
			return e
		}
		// Lost contact is shown, not judged: the attempt keeps its GPU and may return (ADR 0032).
		var expired, unreachable bool
		if e = tx.QueryRow(ctx, transferStatement(tx, "SELECT deadline_at<=clock_timestamp() OR state='running' AND lease_expires_at IS NULL,state='running' AND COALESCE(lease_expires_at<=clock_timestamp(),false) FROM node_tasks WHERE id=$1", "SELECT deadline_at<=UTC_TIMESTAMP(6) OR state='running' AND lease_expires_at IS NULL,state='running' AND COALESCE(lease_expires_at<=UTC_TIMESTAMP(6),false) FROM node_tasks WHERE id=?", []any{task}, []any{task})).Scan(&expired, &unreachable); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, transferStatement(tx, "UPDATE training_jobs SET next_dispatch_at=clock_timestamp()+make_interval(secs=>$2::double precision/1000) WHERE id=$1", "UPDATE training_jobs SET next_dispatch_at=TIMESTAMPADD(MICROSECOND,?*1000,UTC_TIMESTAMP(6)) WHERE id=?", []any{j.ID, s.policy().PollMilliseconds}, []any{s.policy().PollMilliseconds, j.ID})); e != nil {
			return e
		}
		if valid && n.Status == "active" && j.CancelRequestedAt == nil && !expired && (t.State == "pending" || t.State == "running") {
			return markUnreachable(ctx, tx, j, unreachable)
		}
		var gpuNode, gpuUUID, gpuState string
		var revision int
		if e = tx.QueryRow(ctx, transferStatement(tx, "SELECT node_id::text,gpu_uuid,revision,state FROM gpu_leases WHERE training_job_id=$1 FOR UPDATE", "SELECT node_id,gpu_uuid,revision,state FROM gpu_leases WHERE training_job_id=? FOR UPDATE", []any{j.ID}, []any{j.ID})).Scan(&gpuNode, &gpuUUID, &revision, &gpuState); e != nil {
			return e
		}
		if gpuNode != n.ID || gpuUUID != j.GPUUUID || revision != j.AttemptNumber || gpuState != "held" && gpuState != "recovery_required" {
			return fail("TRAINING_ATTEMPT_STALE")
		}
		code := "TRAINING_LEASE_EXPIRED"
		if !valid {
			code = "TRAINING_ACTOR_UNAVAILABLE"
		} else if n.Status != "active" {
			code = "TRAINING_NODE_UNAVAILABLE"
		} else if j.CancelRequestedAt != nil {
			code = "TRAINING_CANCEL_PENDING_STOP"
		}
		if nodes.NeverDelivered(t.State, t.ErrorCode, t.Attempt, events, hasOutcome) {
			sha, e := command.Digest()
			if e != nil {
				return e
			}
			receipt := undeliveredReceipt{SchemaVersion: 1, ReceiptID: uuid.NewString(), Proof: "control_never_delivered", Scope: command.Scope, NodeID: n.ID, TaskID: task, AttemptID: command.AttemptID, AttemptNumber: command.AttemptNumber, CommandSHA256: sha, ClaimAttempt: 0}
			raw, _ := json.Marshal(receipt)
			state := "failed"
			if j.CancelRequestedAt != nil || !valid {
				state = "cancelled"
			}
			if _, e = tx.Exec(ctx, transferStatement(tx, "UPDATE training_attempts SET state=$2,error_code=$3,physical_stop_receipt=$4,physical_stop_sha256=$5,physical_stopped_at=clock_timestamp(),finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1", "UPDATE training_attempts SET state=?,error_code=?,physical_stop_receipt=CAST(? AS JSON),physical_stop_sha256=?,physical_stopped_at=UTC_TIMESTAMP(6),finished_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=?", []any{command.AttemptID, state, code, raw, rawDigest(raw)}, []any{state, code, raw, rawDigest(raw), command.AttemptID})); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, transferStatement(tx, "UPDATE node_tasks SET state=$2,error_code=$3,lease_token_hash=NULL,lease_expires_at=NULL,completed_at=COALESCE(completed_at,clock_timestamp()),updated_at=clock_timestamp() WHERE id=$1", "UPDATE node_tasks SET state=?,error_code=?,lease_token_hash=NULL,lease_expires_at=NULL,completed_at=COALESCE(completed_at,UTC_TIMESTAMP(6)),updated_at=UTC_TIMESTAMP(6) WHERE id=?", []any{task, state, code}, []any{state, code, task})); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, transferStatement(tx, "UPDATE gpu_leases SET state='released',updated_at=clock_timestamp() WHERE training_job_id=$1 AND revision=$2", "UPDATE gpu_leases SET state='released',updated_at=UTC_TIMESTAMP(6) WHERE training_job_id=? AND revision=?", []any{j.ID, command.AttemptNumber}, []any{j.ID, command.AttemptNumber})); e != nil {
				return e
			}
			if e = s.releaseSchedulingWithin(ctx, tx, w, j, command); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, transferStatement(tx, "UPDATE training_jobs SET state=$2,error_code=$3,wait_code=NULL,revision=revision+1,finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1", "UPDATE training_jobs SET state=?,error_code=?,wait_code=NULL,revision=revision+1,finished_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=?", []any{j.ID, state, code}, []any{state, code, j.ID})); e != nil {
				return e
			}
			worked = true
			return transferAuditWithin(ctx, tx, a, w, "training.undelivered_stopped", j.ID, "Control transaction proved command never delivered; exact physical reservation released", uuid.NewString())
		}
		if j.State == "recovery_required" && gpuState == "recovery_required" {
			return nil
		}
		if _, e = tx.Exec(ctx, transferStatement(tx, "UPDATE training_jobs SET state='recovery_required',error_code=$2,wait_code=NULL,revision=revision+1,updated_at=clock_timestamp() WHERE id=$1", "UPDATE training_jobs SET state='recovery_required',error_code=?,wait_code=NULL,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?", []any{j.ID, code}, []any{code, j.ID})); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, transferStatement(tx, "UPDATE training_attempts SET state='recovery_required',error_code=$2,updated_at=clock_timestamp() WHERE id=$1", "UPDATE training_attempts SET state='recovery_required',error_code=?,updated_at=UTC_TIMESTAMP(6) WHERE id=?", []any{command.AttemptID, code}, []any{code, command.AttemptID})); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, transferStatement(tx, "UPDATE gpu_leases SET state='recovery_required',updated_at=clock_timestamp() WHERE training_job_id=$1 AND revision=$2", "UPDATE gpu_leases SET state='recovery_required',updated_at=UTC_TIMESTAMP(6) WHERE training_job_id=? AND revision=?", []any{j.ID, command.AttemptNumber}, []any{j.ID, command.AttemptNumber})); e != nil {
			return e
		}
		worked = true
		return transferAuditWithin(ctx, tx, a, w, "training.physical_stop_required", j.ID, "Delivered attempt lost execution authority; exact physical reservation retained", uuid.NewString())
	})
	if e != nil {
		return false, e
	}
	return worked, nil
}
