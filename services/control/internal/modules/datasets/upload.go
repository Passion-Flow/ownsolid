package datasets

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) Upload(ctx context.Context, a identity.Auth, workspace, dataset, draft, digest string, size int64, source io.Reader, requestID string) (Draft, error) {
	var out Draft
	if s.Store == nil || s.Processor == nil {
		return out, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	var object, upload string
	var uploadAuthority *string
	var replay bool
	e := s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if e := s.mongoAttachmentFence(ctx, tx, a, w, dataset); e != nil {
			return e
		}
		parent, e := lockedDataset(ctx, tx, w, dataset)
		if e != nil {
			return e
		}
		d, e := lockedDraft(ctx, tx, w, dataset, draft)
		if e != nil {
			return e
		}
		if e = draftAccess(a, w, d, true); e != nil {
			return e
		}
		if size != d.ExpectedBytes || digest != d.SourceSHA256 {
			return failure("DATASET_SOURCE_CHANGED")
		}
		if e = tx.QueryRow(ctx, datasetSQL(tx, "SELECT source_object_id::text FROM dataset_drafts WHERE id=$1", `SELECT source_object_id FROM dataset_drafts WHERE id=?`, []any{draft}, []any{draft}, transaction.SQL(transaction.Dameng, dmUploadObject, draft))).Scan(&object); e != nil {
			return e
		}
		if d.SourceReceivedAt != nil {
			if _, e = s.Store.Source(object, digest, size); e != nil {
				return e
			}
			out = d
			replay = true
			return nil
		}
		if parent.RetiredAt != nil {
			return failure("DATASET_RETIRED")
		}
		if d.State != "uploading" && d.State != "failed" {
			return failure("DATASET_DRAFT_BUSY")
		}
		var busy bool
		if e = tx.QueryRow(ctx, datasetSQL(tx, "SELECT upload_expires_at>clock_timestamp() FROM dataset_drafts WHERE id=$1 AND upload_attempt_id IS NOT NULL", `SELECT upload_expires_at>CURRENT_TIMESTAMP(6) FROM dataset_drafts WHERE id=? AND upload_attempt_id IS NOT NULL`, []any{draft}, []any{draft}, transaction.SQL(transaction.Dameng, dmUploadBusy, draft))).Scan(&busy); e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if busy {
			return failure("DATASET_UPLOAD_BUSY")
		}
		// The file copy runs outside SQL. A later membership lifetime must not
		// adopt this upload merely because the caller has been granted access again.
		uploadAuthority, e = delegatedAuthority(ctx, tx, a, w)
		if e != nil {
			return e
		}
		upload = uuid.NewString()
		_, e = tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_drafts SET state='uploading',upload_attempt_id=$2,upload_expires_at=clock_timestamp()+make_interval(secs=>$3),error_code=NULL,error_line_number=NULL,updated_at=clock_timestamp() WHERE id=$1", `UPDATE dataset_drafts SET state='uploading',upload_attempt_id=?,upload_expires_at=TIMESTAMPADD(SECOND,?,CURRENT_TIMESTAMP(6)),error_code=NULL,error_line_number=NULL,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{draft, upload, s.policy().UploadSeconds}, []any{upload, s.policy().UploadSeconds, draft}, transaction.SQL(transaction.Dameng, dmUploadClaim, upload, s.policy().UploadSeconds, draft)))
		return e
	})
	if e != nil || replay {
		return committed(out, e)
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(s.policy().UploadSeconds)*time.Second)
	defer cancel()
	_, copyError := s.Store.PutSource(bounded, object, upload, digest, size, source)
	if copyError != nil {
		cleanup, stop := context.WithTimeout(context.Background(), time.Duration(s.policy().CommitSeconds)*time.Second)
		defer stop()
		// This failure update can only clear its own lease; it neither authorizes data nor publishes a job.
		repo, lookupError := s.repository()
		if lookupError == nil {
			_, _ = repo.Exec(cleanup, datasetSQL(repo, "UPDATE dataset_drafts SET state='failed',upload_attempt_id=NULL,upload_expires_at=NULL,error_code=$3,updated_at=clock_timestamp() WHERE id=$1 AND upload_attempt_id=$2 AND source_received_at IS NULL", `UPDATE dataset_drafts SET state='failed',upload_attempt_id=NULL,upload_expires_at=NULL,error_code=?,updated_at=CURRENT_TIMESTAMP(6) WHERE id=? AND upload_attempt_id=? AND source_received_at IS NULL`, []any{draft, upload, safeDatasetError(copyError)}, []any{safeDatasetError(copyError), draft, upload}, transaction.SQL(transaction.Dameng, dmUploadFailure, safeDatasetError(copyError), draft, upload)))
		}
		return out, copyError
	}
	e = s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if e := s.mongoAttachmentFence(ctx, tx, a, w, dataset); e != nil {
			return e
		}
		parent, e := lockedDataset(ctx, tx, w, dataset)
		if e != nil {
			return e
		}
		d, e := lockedDraft(ctx, tx, w, dataset, draft)
		if e != nil {
			return e
		}
		if e = draftAccess(a, w, d, true); e != nil {
			return e
		}
		if parent.RetiredAt != nil {
			return failure("DATASET_RETIRED")
		}
		if d.State != "uploading" || d.SourceReceivedAt != nil {
			return failure("DATASET_ATTEMPT_STALE")
		}
		var current bool
		if e = tx.QueryRow(ctx, datasetSQL(tx, "SELECT COALESCE(upload_attempt_id=$2 AND upload_expires_at>clock_timestamp(),false) FROM dataset_drafts WHERE id=$1", `SELECT COALESCE(upload_attempt_id=? AND upload_expires_at>CURRENT_TIMESTAMP(6),false) FROM dataset_drafts WHERE id=?`, []any{draft, upload}, []any{upload, draft}, transaction.SQL(transaction.Dameng, dmUploadCurrentLease, upload, draft))).Scan(&current); e != nil {
			return e
		}
		if !current {
			return failure("DATASET_ATTEMPT_STALE")
		}
		if _, e = s.Store.Source(object, digest, size); e != nil {
			return e
		}
		authority, e := delegatedAuthority(ctx, tx, a, w)
		if e != nil {
			return e
		}
		if (uploadAuthority == nil) != (authority == nil) || uploadAuthority != nil && *uploadAuthority != *authority {
			return failure("DATASET_ATTEMPT_STALE")
		}
		job := uuid.NewString()
		intent := fingerprint(struct{ Draft, Upload string }{draft, upload}, a.Me.User.ID)
		_, e = tx.Exec(ctx, datasetSQL(tx, `INSERT INTO dataset_jobs(id,tenant_id,workspace_id,dataset_id,draft_id,kind,requested_by,requested_audience,requested_epoch,membership_authority,reason,expected_revision,state,input_sha256,input_policy,creation_key,creation_fingerprint) VALUES($1,$2,$3,$4,$5,'import',$6,$7,$8,$9,'Validate uploaded source',$10,'pending',$11,$12,$13,$14)`, `INSERT INTO dataset_jobs(id,tenant_id,workspace_id,dataset_id,draft_id,kind,requested_by,requested_audience,requested_epoch,membership_authority,reason,expected_revision,state,input_sha256,input_policy,creation_key,creation_fingerprint) VALUES(?,?,?,?,?,'import',?,?,?,?,'Validate uploaded source',?,'pending',?,CAST(? AS JSON),?,?)`, []any{job, w.TenantID, w.ID, dataset, draft, a.Me.User.ID, a.Me.Audience, a.Epoch, authority, d.Revision, digest, s.jobPolicyBytes(""), uuid.NewString(), intent}, []any{job, w.TenantID, w.ID, dataset, draft, a.Me.User.ID, a.Me.Audience, a.Epoch, authority, d.Revision, digest, s.jobPolicyBytes(""), uuid.NewString(), intent}, transaction.SQL(transaction.Dameng, dmUploadQueueInspection, job, w.TenantID, w.ID, dataset, draft, a.Me.User.ID, a.Me.Audience, a.Epoch, authority, d.Revision, digest, string(s.jobPolicyBytes("")), uuid.NewString(), intent)))
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_drafts SET source_received_at=clock_timestamp(),upload_attempt_id=NULL,upload_expires_at=NULL,state='validating',current_job_id=$2,error_code=NULL,error_line_number=NULL,updated_at=clock_timestamp() WHERE id=$1", `UPDATE dataset_drafts SET source_received_at=CURRENT_TIMESTAMP(6),upload_attempt_id=NULL,upload_expires_at=NULL,state='validating',current_job_id=?,error_code=NULL,error_line_number=NULL,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{draft, job}, []any{job, draft}, transaction.SQL(transaction.Dameng, dmUploadComplete, job, draft)))
		if e != nil {
			return e
		}
		if e = auditEvent(ctx, tx, a, w, "dataset.source_received", "dataset_draft", draft, "Validate uploaded source", requestID); e != nil {
			return e
		}
		out, e = lockedDraft(ctx, tx, w, dataset, draft)
		return e
	})
	return committed(out, e)
}
func safeDatasetError(e error) string {
	if e == nil {
		return ""
	}
	switch e.Error() {
	case "DATASET_ATTEMPT_MONITOR_FAILED", "DATASET_UPLOAD_INCOMPLETE", "DATASET_UPLOAD_BUSY", "DATASET_SOURCE_CHANGED", "DATASET_STORAGE_FULL", "DATASET_FILE_INVALID", "DATASET_ATTEMPT_STALE", "DATASET_STORAGE_UNAVAILABLE", "DATASET_PROCESSOR_TIMEOUT", "DATASET_PROCESSOR_MEMORY_EXCEEDED", "DATASET_PROCESSOR_RESULT_TOO_LARGE", "DATASET_PROCESSOR_RESULT_INVALID", "DATASET_PROCESSOR_MEMORY_MONITOR_FAILED", "DATASET_JOB_CANCELLED", "DATASET_PROCESSOR_PROGRAM_INVALID", "DATASET_PROCESSOR_START_FAILED", "DATASET_OUTPUT_CONFLICT":
		return e.Error()
	}
	return "DATASET_PROCESSOR_FAILED"
}
