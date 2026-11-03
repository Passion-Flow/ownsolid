package datasets

import (
	"bytes"
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	"os"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"strings"
	"time"
)

type claimedAttempt struct {
	request        wire.ProcessorRequest
	job            Job
	policy         jobPolicy
	sourceObject   string
	sourceBytes    int64
	input, mapping []byte
	claimed        bool
}

// ProcessNext executes one actual bounded CPU attempt. No Python PID is stored or signaled after a restart.
func (s *Service) ProcessNext(ctx context.Context) (bool, error) {
	if s.Store == nil || s.Processor == nil {
		return false, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	c, e := s.nextCandidate(ctx)
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	var work claimedAttempt
	e = s.workerTransaction(ctx, c, func(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, parent Dataset, d Draft, j Job, authorized bool) error {
		if j.State != "pending" && j.State != "running" {
			return nil
		}
		var eligible bool
		if e := tx.QueryRow(ctx, datasetSQL(tx, "SELECT (state='pending' AND not_before<=clock_timestamp()) OR (state='running' AND lease_expires_at<=clock_timestamp()) FROM dataset_jobs WHERE id=$1", `SELECT (state='pending' AND not_before<=CURRENT_TIMESTAMP(6)) OR (state='running' AND lease_expires_at<=CURRENT_TIMESTAMP(6)) FROM dataset_jobs WHERE id=?`, []any{j.ID}, []any{j.ID}, transaction.SQL(transaction.Dameng, dmWorkerEligible, j.ID))).Scan(&eligible); e != nil {
			return e
		}
		if !eligible {
			return nil
		}
		if j.State == "running" && j.AttemptID != nil {
			if _, e := tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_job_attempts SET state='expired',finished_at=clock_timestamp(),error_code='DATASET_ATTEMPT_EXPIRED' WHERE job_id=$1 AND id=$2 AND state='running'", `UPDATE dataset_job_attempts SET state='expired',finished_at=CURRENT_TIMESTAMP(6),error_code='DATASET_ATTEMPT_EXPIRED' WHERE job_id=? AND id=? AND state='running'`, []any{j.ID, *j.AttemptID}, []any{j.ID, *j.AttemptID}, transaction.SQL(transaction.Dameng, dmWorkerExpireAttempt, j.ID, *j.AttemptID))); e != nil {
				return e
			}
		}
		if !authorized {
			return s.finishFailure(ctx, tx, a, w, d, j, "DATASET_ACTOR_UNAVAILABLE", false, false)
		}
		if j.CancelRequestedAt != nil {
			return s.finishFailure(ctx, tx, a, w, d, j, "DATASET_JOB_CANCELLED", false, true)
		}
		if j.AttemptCount >= s.policy().MaxAttempts {
			return s.finishFailure(ctx, tx, a, w, d, j, "DATASET_ATTEMPTS_EXHAUSTED", false, false)
		}
		var policyRaw []byte
		if e := tx.QueryRow(ctx, datasetSQL(tx, "SELECT input_policy,source_object_id::text,expected_bytes FROM dataset_jobs JOIN dataset_drafts ON dataset_drafts.id=dataset_jobs.draft_id WHERE dataset_jobs.id=$1", `SELECT input_policy,source_object_id,expected_bytes FROM dataset_jobs JOIN dataset_drafts ON dataset_drafts.id=dataset_jobs.draft_id WHERE dataset_jobs.id=?`, []any{j.ID}, []any{j.ID}, transaction.SQL(transaction.Dameng, dmWorkerInput, j.ID))).Scan(&policyRaw, &work.sourceObject, &work.sourceBytes); e != nil {
			return e
		}
		if wire.DecodeStrict(policyRaw, &work.policy) != nil || work.policy.Limits.Validate() != nil || work.policy.Resources.Validate() != nil {
			return s.finishFailure(ctx, tx, a, w, d, j, "DATASET_POLICY_INVALID", false, false)
		}
		if work.policy.Limits != s.policy().Limits || work.policy.Resources != s.policy().Resources {
			return s.finishFailure(ctx, tx, a, w, d, j, "DATASET_POLICY_CHANGED", false, false)
		}
		if j.InputSHA256 == nil {
			return s.finishFailure(ctx, tx, a, w, d, j, "DATASET_SOURCE_CHANGED", false, false)
		}
		if j.Kind == "publish" {
			var digest string
			var e error
			work.input, work.mapping, digest, e = snapshot(ctx, tx, d.ID, work.policy.Limits)
			if e != nil {
				// A native statement failure poisons its connection; PostgreSQL
				// statement errors can also abort the transaction. Preserve the
				// original failure and roll back instead of issuing failure SQL.
				if _, validation := e.(*snapshotValidationError); !validation {
					return e
				}
				if contextError := ctx.Err(); contextError != nil {
					return contextError
				}
				return s.finishFailure(ctx, tx, a, w, d, j, safeDatasetError(e), false, false)
			}
			if digest != *j.InputSHA256 {
				return s.finishFailure(ctx, tx, a, w, d, j, "DATASET_SOURCE_CHANGED", false, false)
			}
		}
		attemptID := uuid.NewString()
		work.request = wire.ProcessorRequest{SchemaVersion: 1, Operation: wire.InspectOperation, JobID: j.ID, AttemptID: attemptID, InputSHA256: *j.InputSHA256, Limits: work.policy.Limits, Resources: work.policy.Resources}
		if j.Kind == "publish" {
			work.request.Operation = wire.PublishOperation
			work.request.Seed = j.Seed
			work.request.ValidationBasisPoints = j.ValidationBasisPoints
		}
		if _, e := tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_jobs SET state='running',attempt_id=$2,attempt_count=attempt_count+1,lease_expires_at=clock_timestamp()+make_interval(secs=>$3),error_code=NULL,error_line_number=NULL,retryable=false,updated_at=clock_timestamp() WHERE id=$1", `UPDATE dataset_jobs SET state='running',attempt_id=?,attempt_count=attempt_count+1,lease_expires_at=TIMESTAMPADD(SECOND,?,CURRENT_TIMESTAMP(6)),error_code=NULL,error_line_number=NULL,retryable=false,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{j.ID, attemptID, s.policy().LeaseSeconds}, []any{attemptID, s.policy().LeaseSeconds, j.ID}, transaction.SQL(transaction.Dameng, dmWorkerClaim, attemptID, s.policy().LeaseSeconds, j.ID))); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, datasetSQL(tx, "INSERT INTO dataset_job_attempts(tenant_id,workspace_id,dataset_id,draft_id,job_id,id,attempt_number,state) VALUES($1,$2,$3,$4,$5,$6,$7,'running')", `INSERT INTO dataset_job_attempts(tenant_id,workspace_id,dataset_id,draft_id,job_id,id,attempt_number,state) VALUES(?,?,?,?,?,?,?,'running')`, []any{w.TenantID, w.ID, j.DatasetID, j.DraftID, j.ID, attemptID, j.AttemptCount + 1}, []any{w.TenantID, w.ID, j.DatasetID, j.DraftID, j.ID, attemptID, j.AttemptCount + 1}, transaction.SQL(transaction.Dameng, dmWorkerInsertAttempt, w.TenantID, w.ID, j.DatasetID, j.DraftID, j.ID, attemptID, j.AttemptCount+1))); e != nil {
			return e
		}
		work.job = j
		work.claimed = true
		return nil
	})
	if e != nil || !work.claimed {
		return true, e
	}
	executeCtx, cancelCause := context.WithCancelCause(ctx)
	cancel := func() { cancelCause(context.Canceled) }
	defer cancel()
	monitorDone := make(chan struct{})
	go s.watchAttempt(executeCtx, c, work.request.AttemptID, cancelCause, monitorDone)
	defer func() { cancel(); <-monitorDone }()
	var source io.Reader
	var sourceFile *os.File
	size := int64(len(work.input))
	if work.job.Kind == "import" {
		path, check := s.Store.Source(work.sourceObject, work.request.InputSHA256, work.sourceBytes)
		if check != nil {
			return true, s.recordAttemptFailure(ctx, c, work, check)
		}
		sourceFile, e = os.Open(path)
		if e != nil {
			return true, s.recordAttemptFailure(ctx, c, work, failure("DATASET_FILE_INVALID"))
		}
		defer sourceFile.Close()
		source = sourceFile
		size = work.sourceBytes
	} else {
		source = bytes.NewReader(work.input)
	}
	scope, input, output, e := s.Store.CreateAttempt(work.job.ID, work.request.AttemptID, source, work.request.InputSHA256, size, work.policy.Resources.MaximumResultBytes)
	if e != nil {
		return true, s.recordAttemptFailure(ctx, c, work, e)
	}
	work.request.ScopeDirectory = scope
	work.request.InputFile = input
	work.request.OutputDirectory = output
	result, e := s.Processor.Execute(executeCtx, work.request)
	if cause := context.Cause(executeCtx); cause != nil && cause.Error() == "DATASET_ATTEMPT_MONITOR_FAILED" {
		e = cause
	}
	if e != nil {
		return true, s.recordAttemptFailure(ctx, c, work, e)
	}
	if result.Status == "failed" {
		return true, s.recordAttemptFailureAt(ctx, c, work, failure(result.ErrorCode), result.ErrorLineNumber)
	}
	var mappingSHA string
	if result.Status == "prepared" {
		mappingSHA, e = s.Store.PrepareVersionFiles(work.request, result, *work.job.VersionID, work.job.DraftID, work.mapping)
		if e != nil {
			return true, s.recordAttemptFailure(ctx, c, work, e)
		}
	}
	commitCtx, stop := context.WithTimeout(ctx, time.Duration(s.policy().CommitSeconds)*time.Second)
	defer stop()
	e = s.workerTransaction(commitCtx, c, func(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, parent Dataset, d Draft, j Job, authorized bool) error {
		if j.State != "running" || j.AttemptID == nil || *j.AttemptID != work.request.AttemptID {
			return failure("DATASET_ATTEMPT_STALE")
		}
		var live bool
		if e := tx.QueryRow(commitCtx, datasetSQL(tx, "SELECT lease_expires_at>clock_timestamp() FROM dataset_jobs WHERE id=$1", `SELECT lease_expires_at>CURRENT_TIMESTAMP(6) FROM dataset_jobs WHERE id=?`, []any{j.ID}, []any{j.ID}, transaction.SQL(transaction.Dameng, dmWorkerAcceptLease, j.ID))).Scan(&live); e != nil {
			return e
		}
		if !live {
			return failure("DATASET_ATTEMPT_STALE")
		}
		if !authorized {
			return s.finishFailure(commitCtx, tx, a, w, d, j, "DATASET_ACTOR_UNAVAILABLE", false, false)
		}
		if j.CancelRequestedAt != nil {
			return s.finishFailure(commitCtx, tx, a, w, d, j, "DATASET_JOB_CANCELLED", false, true)
		}
		if work.job.Kind == "import" {
			if e := s.acceptInspection(commitCtx, tx, a, w, d, j, result); e != nil {
				return e
			}
		} else {
			if e := s.acceptPublication(commitCtx, tx, a, w, parent, d, j, result, mappingSHA); e != nil {
				return e
			}
		}
		if _, e := tx.Exec(commitCtx, datasetSQL(tx, "UPDATE dataset_job_attempts SET state='succeeded',finished_at=clock_timestamp(),memory_enforcement=$3 WHERE job_id=$1 AND id=$2 AND state='running'", `UPDATE dataset_job_attempts SET state='succeeded',finished_at=CURRENT_TIMESTAMP(6),memory_enforcement=? WHERE job_id=? AND id=? AND state='running'`, []any{j.ID, work.request.AttemptID, result.MemoryEnforcement}, []any{result.MemoryEnforcement, j.ID, work.request.AttemptID}, transaction.SQL(transaction.Dameng, dmWorkerCompleteAttempt, result.MemoryEnforcement, j.ID, work.request.AttemptID))); e != nil {
			return e
		}
		_, e := tx.Exec(commitCtx, datasetSQL(tx, "UPDATE dataset_jobs SET state='succeeded',lease_expires_at=NULL,error_code=NULL,error_line_number=NULL,retryable=false,completed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1", `UPDATE dataset_jobs SET state='succeeded',lease_expires_at=NULL,error_code=NULL,error_line_number=NULL,retryable=false,completed_at=CURRENT_TIMESTAMP(6),updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{j.ID}, []any{j.ID}, transaction.SQL(transaction.Dameng, dmWorkerCompleteJob, j.ID)))
		return e
	})
	return true, e
}
func (s *Service) watchAttempt(ctx context.Context, c candidate, attempt string, cancel context.CancelCauseFunc, done chan<- struct{}) {
	defer close(done)
	repo, e := s.repository()
	if e != nil {
		cancel(failure("DATASET_ATTEMPT_MONITOR_FAILED"))
		return
	}
	ticker := time.NewTicker(time.Duration(s.policy().PollMilliseconds) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			bounded, stop := context.WithTimeout(ctx, time.Duration(s.policy().CommitSeconds)*time.Second)
			var live bool
			e := repo.QueryRow(bounded, datasetSQL(repo, "SELECT state='running' AND attempt_id=$2 AND cancel_requested_at IS NULL AND lease_expires_at>clock_timestamp() FROM dataset_jobs WHERE id=$1", `SELECT state='running' AND attempt_id=? AND cancel_requested_at IS NULL AND lease_expires_at>CURRENT_TIMESTAMP(6) FROM dataset_jobs WHERE id=?`, []any{c.id, attempt}, []any{attempt, c.id}, transaction.SQL(transaction.Dameng, dmWorkerWatchLease, attempt, c.id))).Scan(&live)
			stop()
			if e != nil {
				cancel(failure("DATASET_ATTEMPT_MONITOR_FAILED"))
				return
			}
			if !live {
				cancel(failure("DATASET_JOB_CANCELLED"))
				return
			}
		}
	}
}
func (s *Service) recordAttemptFailure(ctx context.Context, c candidate, work claimedAttempt, problem error) error {
	return s.recordAttemptFailureAt(ctx, c, work, problem, nil)
}
func (s *Service) recordAttemptFailureAt(ctx context.Context, c candidate, work claimedAttempt, problem error, line *int64) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	bounded, stop := context.WithTimeout(ctx, time.Duration(s.policy().CommitSeconds)*time.Second)
	defer stop()
	code := safeDatasetError(problem)
	if wire.KnownError(problem.Error()) {
		code = problem.Error()
	}
	retryable := strings.HasPrefix(code, "DATASET_PROCESSOR_") && code != "DATASET_PROCESSOR_PROGRAM_INVALID" || code == "DATASET_STORAGE_UNAVAILABLE" || code == "DATASET_STORAGE_FULL" || code == "DATASET_ATTEMPT_MONITOR_FAILED"
	return s.workerTransaction(bounded, c, func(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, _ Dataset, d Draft, j Job, authorized bool) error {
		if j.State != "running" || j.AttemptID == nil || *j.AttemptID != work.request.AttemptID {
			return failure("DATASET_ATTEMPT_STALE")
		}
		var live bool
		if e := tx.QueryRow(bounded, datasetSQL(tx, "SELECT lease_expires_at>clock_timestamp() FROM dataset_jobs WHERE id=$1", `SELECT lease_expires_at>CURRENT_TIMESTAMP(6) FROM dataset_jobs WHERE id=?`, []any{j.ID}, []any{j.ID}, transaction.SQL(transaction.Dameng, dmWorkerFailureLease, j.ID))).Scan(&live); e != nil {
			return e
		}
		if !live {
			return failure("DATASET_ATTEMPT_STALE")
		}

		if !authorized {
			code = "DATASET_ACTOR_UNAVAILABLE"
			retryable = false
		}
		cancelled := j.CancelRequestedAt != nil
		if cancelled {
			code = "DATASET_JOB_CANCELLED"
			retryable = false
		}
		if e := s.finishFailure(bounded, tx, a, w, d, j, code, retryable, cancelled); e != nil {
			return e
		}
		if line != nil {
			if *line < 0 || *line > work.policy.Limits.MaximumRows+1 {
				return failure("DATASET_PROCESSOR_RESULT_INVALID")
			}
			if _, e := tx.Exec(bounded, datasetSQL(tx, "UPDATE dataset_jobs SET error_line_number=$2 WHERE id=$1", `UPDATE dataset_jobs SET error_line_number=? WHERE id=?`, []any{j.ID, *line}, []any{*line, j.ID}, transaction.SQL(transaction.Dameng, dmWorkerJobErrorLine, *line, j.ID))); e != nil {
				return e
			}
			if _, e := tx.Exec(bounded, datasetSQL(tx, "UPDATE dataset_drafts SET error_line_number=$2 WHERE id=$1 AND current_job_id=$3", `UPDATE dataset_drafts SET error_line_number=? WHERE id=? AND current_job_id=?`, []any{d.ID, *line, j.ID}, []any{*line, d.ID, j.ID}, transaction.SQL(transaction.Dameng, dmWorkerDraftErrorLine, *line, d.ID, j.ID))); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s *Service) finishFailure(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, d Draft, j Job, code string, retryable, cancelled bool) error {
	state := "failed"
	attemptState := "failed"
	if cancelled {
		state = "cancelled"
		attemptState = "cancelled"
	}
	if j.AttemptID != nil {
		if _, e := tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_job_attempts SET state=$3,finished_at=clock_timestamp(),error_code=$4 WHERE job_id=$1 AND id=$2 AND state='running'", `UPDATE dataset_job_attempts SET state=?,finished_at=CURRENT_TIMESTAMP(6),error_code=? WHERE job_id=? AND id=? AND state='running'`, []any{j.ID, *j.AttemptID, attemptState, code}, []any{attemptState, code, j.ID, *j.AttemptID}, transaction.SQL(transaction.Dameng, dmWorkerFinishAttempt, attemptState, code, j.ID, *j.AttemptID))); e != nil {
			return e
		}
	}
	if _, e := tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_jobs SET state=$2,lease_expires_at=NULL,error_code=$3,error_line_number=NULL,retryable=$4,completed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1", `UPDATE dataset_jobs SET state=?,lease_expires_at=NULL,error_code=?,error_line_number=NULL,retryable=?,completed_at=CURRENT_TIMESTAMP(6),updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{j.ID, state, code, retryable}, []any{state, code, retryable, j.ID}, transaction.SQL(transaction.Dameng, dmWorkerFinishJob, state, code, retryable, j.ID))); e != nil {
		return e
	}
	draftState := "failed"
	if cancelled {
		draftState = "cancelled"
	}
	if j.Kind == "publish" {
		draftState = "editing"
	}
	fatal := j.Kind == "import" && (code == "DATASET_ROW_LIMIT" || code == "DATASET_FILE_TOO_LARGE" || code == "DATASET_ENCODING_INVALID" || code == "DATASET_BOM_FORBIDDEN")
	if _, e := tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_drafts SET state=$2,error_code=$3,error_line_number=NULL,fatal_source_error=fatal_source_error OR $4,updated_at=clock_timestamp() WHERE id=$1 AND current_job_id=$5", `UPDATE dataset_drafts SET state=?,error_code=?,error_line_number=NULL,fatal_source_error=fatal_source_error OR ?,updated_at=CURRENT_TIMESTAMP(6) WHERE id=? AND current_job_id=?`, []any{d.ID, draftState, code, fatal, j.ID}, []any{draftState, code, fatal, d.ID, j.ID}, transaction.SQL(transaction.Dameng, dmWorkerFinishDraft, draftState, code, fatal, d.ID, j.ID))); e != nil {
		return e
	}
	return auditEvent(ctx, tx, a, w, "dataset.job_"+state, "dataset_job", j.ID, code, j.ID)
}
