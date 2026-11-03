package datasets

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func (s *Service) Retire(ctx context.Context, a identity.Auth, workspace, id string, revision int64, reason, requestID string) (Dataset, error) {
	var out Dataset
	if revision < 1 || !validReason(reason) {
		return out, failure("VALIDATION_FAILED")
	}
	e := s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if !isOwner(a, w) {
			return failure("ACCESS_DENIED")
		}
		parent, e := lockedDataset(ctx, tx, w, id)
		if e != nil {
			return e
		}
		if parent.RetiredAt != nil {
			out = parent
			return nil
		}
		if parent.Revision != revision {
			return failure("DATASET_REVISION_CONFLICT")
		}
		if _, e = tx.Exec(ctx, datasetSQL(tx, "UPDATE datasets SET retired_at=clock_timestamp(),retire_reason=$2,revision=revision+1,updated_at=clock_timestamp() WHERE id=$1", `UPDATE datasets SET retired_at=CURRENT_TIMESTAMP(6),retire_reason=?,revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{id, reason}, []any{reason, id}, transaction.SQL(transaction.Dameng, dmDatasetRetire, reason, id))); e != nil {
			return e
		}
		// Resource locks serialize commits. Other users' account locks are not acquired after this Workspace lock.
		if e = lockDatasetRows(ctx, tx, datasetSQL(tx, "SELECT id FROM dataset_drafts WHERE dataset_id=$1 ORDER BY id FOR UPDATE", `SELECT id FROM dataset_drafts WHERE dataset_id=? ORDER BY id FOR UPDATE`, []any{id}, []any{id}, transaction.SQL(transaction.Dameng, dmRetireLockDrafts, id))); e != nil {
			return e
		}
		if e = lockDatasetRows(ctx, tx, datasetSQL(tx, "SELECT id FROM dataset_jobs WHERE dataset_id=$1 AND state IN ('pending','running') ORDER BY id FOR UPDATE", `SELECT id FROM dataset_jobs WHERE dataset_id=? AND state IN ('pending','running') ORDER BY id FOR UPDATE`, []any{id}, []any{id}, transaction.SQL(transaction.Dameng, dmRetireLockJobs, id))); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_jobs SET cancel_requested_at=COALESCE(cancel_requested_at,clock_timestamp()),cancel_requested_by=COALESCE(cancel_requested_by,$2),state=CASE WHEN state='pending' THEN 'cancelled' ELSE state END,completed_at=CASE WHEN state='pending' THEN clock_timestamp() ELSE completed_at END,error_code=CASE WHEN state='pending' THEN 'DATASET_RETIRED' ELSE error_code END,updated_at=clock_timestamp() WHERE dataset_id=$1 AND state IN ('pending','running')", `UPDATE dataset_jobs SET cancel_requested_at=COALESCE(cancel_requested_at,CURRENT_TIMESTAMP(6)),cancel_requested_by=COALESCE(cancel_requested_by,?),completed_at=CASE WHEN state='pending' THEN CURRENT_TIMESTAMP(6) ELSE completed_at END,error_code=CASE WHEN state='pending' THEN 'DATASET_RETIRED' ELSE error_code END,state=CASE WHEN state='pending' THEN 'cancelled' ELSE state END,updated_at=CURRENT_TIMESTAMP(6) WHERE dataset_id=? AND state IN ('pending','running')`, []any{id, a.Me.User.ID}, []any{a.Me.User.ID, id}, transaction.SQL(transaction.Dameng, dmRetireCancelJobs, a.Me.User.ID, id))); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_drafts SET state=CASE WHEN state='publishing' THEN 'editing' ELSE 'cancelled' END,error_code='DATASET_RETIRED',upload_attempt_id=NULL,upload_expires_at=NULL,updated_at=clock_timestamp() WHERE dataset_id=$1 AND state IN ('uploading','validating','publishing') AND NOT EXISTS(SELECT 1 FROM dataset_jobs j WHERE j.id=dataset_drafts.current_job_id AND j.state='running')", `UPDATE dataset_drafts SET state=CASE WHEN state='publishing' THEN 'editing' ELSE 'cancelled' END,error_code='DATASET_RETIRED',upload_attempt_id=NULL,upload_expires_at=NULL,updated_at=CURRENT_TIMESTAMP(6) WHERE dataset_id=? AND state IN ('uploading','validating','publishing') AND NOT EXISTS(SELECT 1 FROM dataset_jobs j WHERE j.id=dataset_drafts.current_job_id AND j.state='running')`, []any{id}, []any{id}, transaction.SQL(transaction.Dameng, dmRetireCancelDrafts, id))); e != nil {
			return e
		}
		if e = auditEvent(ctx, tx, a, w, "dataset.retired", "dataset", id, reason, requestID); e != nil {
			return e
		}
		out, e = lockedDataset(ctx, tx, w, id)
		return e
	})
	return committed(out, e)
}
func (s *Service) RetireVersion(ctx context.Context, a identity.Auth, workspace, dataset, id, reason, requestID string) (Version, error) {
	var out Version
	if !validReason(reason) {
		return out, failure("VALIDATION_FAILED")
	}
	e := s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if !isOwner(a, w) {
			return failure("ACCESS_DENIED")
		}
		if _, e := lockedDataset(ctx, tx, w, dataset); e != nil {
			return e
		}
		v, e := lockedVersion(ctx, tx, w, dataset, id)
		if e != nil {
			return e
		}
		if v.RetiredAt != nil {
			out = v
			return nil
		}
		if _, e = tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_versions SET retired_at=clock_timestamp(),retire_reason=$2 WHERE id=$1", `UPDATE dataset_versions SET retired_at=CURRENT_TIMESTAMP(6),retire_reason=? WHERE id=?`, []any{id, reason}, []any{reason, id}, transaction.SQL(transaction.Dameng, dmVersionRetire, reason, id))); e != nil {
			return e
		}
		if e = auditEvent(ctx, tx, a, w, "dataset.version_retired", "dataset_version", id, reason, requestID); e != nil {
			return e
		}
		out, e = lockedVersion(ctx, tx, w, dataset, id)
		return e
	})
	return committed(out, e)
}

// InvalidateAuthorities participates in the operator's coordinated restore transaction; immutable data/history remains intact.
func InvalidateAuthorities(ctx context.Context, tx pgx.Tx) error {
	return InvalidateAuthoritiesWithin(ctx, transaction.PostgreSQLTransaction(tx))
}

// InvalidateAuthoritiesWithin borrows the operator coordinated restore transaction.
func InvalidateAuthoritiesWithin(ctx context.Context, tx transaction.Executor) error {
	if ctx == nil || tx == nil || tx.Dialect() != transaction.PostgreSQL && tx.Dialect() != transaction.MySQL && tx.Dialect() != transaction.Dameng {
		return failure("DATASET_STORAGE_MISMATCH")
	}
	if _, e := tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_job_attempts SET state='cancelled',finished_at=clock_timestamp(),error_code='DATASET_RESTORE_INVALIDATED' WHERE state='running'", `UPDATE dataset_job_attempts SET state='cancelled',finished_at=CURRENT_TIMESTAMP(6),error_code='DATASET_RESTORE_INVALIDATED' WHERE state='running'`, nil, nil, transaction.SQL(transaction.Dameng, dmRestoreCancelAttempts))); e != nil {
		return e
	}
	if _, e := tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_jobs SET state='cancelled',lease_expires_at=NULL,completed_at=clock_timestamp(),error_code='DATASET_RESTORE_INVALIDATED',retryable=false,updated_at=clock_timestamp() WHERE state IN ('pending','running')", `UPDATE dataset_jobs SET state='cancelled',lease_expires_at=NULL,completed_at=CURRENT_TIMESTAMP(6),error_code='DATASET_RESTORE_INVALIDATED',retryable=false,updated_at=CURRENT_TIMESTAMP(6) WHERE state IN ('pending','running')`, nil, nil, transaction.SQL(transaction.Dameng, dmRestoreCancelJobs))); e != nil {
		return e
	}
	_, e := tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_drafts SET state=CASE WHEN state='publishing' THEN 'editing' ELSE 'cancelled' END,upload_attempt_id=NULL,upload_expires_at=NULL,error_code='DATASET_RESTORE_INVALIDATED',updated_at=clock_timestamp() WHERE state IN ('uploading','validating','publishing')", `UPDATE dataset_drafts SET state=CASE WHEN state='publishing' THEN 'editing' ELSE 'cancelled' END,upload_attempt_id=NULL,upload_expires_at=NULL,error_code='DATASET_RESTORE_INVALIDATED',updated_at=CURRENT_TIMESTAMP(6) WHERE state IN ('uploading','validating','publishing')`, nil, nil, transaction.SQL(transaction.Dameng, dmRestoreCancelDrafts)))
	return e
}

// Consume selected row locks before issuing the next statement on a native
// connection. The caller retains the owning write transaction and lock order.
func lockDatasetRows(ctx context.Context, tx transaction.Executor, statement transaction.Statement) error {
	rows, e := tx.Query(ctx, statement)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if e := rows.Scan(&id); e != nil {
			return e
		}
	}
	return rows.Err()
}
