package datasets

import (
	"context"
	"errors"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) requestCancellation(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, d Draft, j Job, reason, requestID string) error {
	if j.State != "pending" && j.State != "running" {
		return nil
	}
	if _, e := tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_jobs SET cancel_requested_at=COALESCE(cancel_requested_at,clock_timestamp()),cancel_requested_by=COALESCE(cancel_requested_by,$2),updated_at=clock_timestamp() WHERE id=$1", `UPDATE dataset_jobs SET cancel_requested_at=COALESCE(cancel_requested_at,CURRENT_TIMESTAMP(6)),cancel_requested_by=COALESCE(cancel_requested_by,?),updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{j.ID, a.Me.User.ID}, []any{a.Me.User.ID, j.ID}, transaction.SQL(transaction.Dameng, dmJobRequestCancel, a.Me.User.ID, j.ID))); e != nil {
		return e
	}
	if j.State == "pending" {
		if e := s.finishFailure(ctx, tx, a, w, d, j, "DATASET_JOB_CANCELLED", false, true); e != nil {
			return e
		}
	}
	return auditEvent(ctx, tx, a, w, "dataset.job_cancellation_requested", "dataset_job", j.ID, reason, requestID)
}
func (s *Service) CancelJob(ctx context.Context, a identity.Auth, workspace, dataset, id, reason, requestID string) (Job, error) {
	var out Job
	if !wire.ValidID(id) || !validReason(reason) {
		return out, failure("VALIDATION_FAILED")
	}
	e := s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if _, e := lockedDataset(ctx, tx, w, dataset); e != nil {
			return e
		}
		var draftID string
		if e := tx.QueryRow(ctx, datasetSQL(tx, "SELECT draft_id::text FROM dataset_jobs WHERE tenant_id=$1 AND workspace_id=$2 AND dataset_id=$3 AND id=$4", `SELECT draft_id FROM dataset_jobs WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND id=?`, []any{w.TenantID, w.ID, dataset, id}, []any{w.TenantID, w.ID, dataset, id}, transaction.SQL(transaction.Dameng, dmCancelJobDraft, w.TenantID, w.ID, dataset, id))).Scan(&draftID); e != nil {
			return failure("DATASET_NOT_FOUND")
		}
		d, e := lockedDraft(ctx, tx, w, dataset, draftID)
		if e != nil {
			return e
		}
		if e = draftAccess(a, w, d, true); e != nil {
			return e
		}
		j, e := lockedJob(ctx, tx, w, dataset, id)
		if e != nil {
			return e
		}
		if e = s.requestCancellation(ctx, tx, a, w, d, j, reason, requestID); e != nil {
			return e
		}
		out, e = lockedJob(ctx, tx, w, dataset, id)
		return e
	})
	return committed(out, e)
}
func (s *Service) CancelDraft(ctx context.Context, a identity.Auth, workspace, dataset, id, reason, requestID string) (Draft, error) {
	var out Draft
	if !wire.ValidID(id) || !validReason(reason) {
		return out, failure("VALIDATION_FAILED")
	}
	e := s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if _, e := lockedDataset(ctx, tx, w, dataset); e != nil {
			return e
		}
		d, e := lockedDraft(ctx, tx, w, dataset, id)
		if e != nil {
			return e
		}
		if e = draftAccess(a, w, d, true); e != nil {
			return e
		}
		if d.State == "published" {
			return failure("DATASET_DRAFT_BUSY")
		}
		if d.CurrentJobID != nil {
			j, e := lockedJob(ctx, tx, w, dataset, *d.CurrentJobID)
			if e != nil {
				return e
			}
			if j.State == "running" || j.State == "pending" {
				if e = s.requestCancellation(ctx, tx, a, w, d, j, reason, requestID); e != nil {
					return e
				}
				out, e = lockedDraft(ctx, tx, w, dataset, id)
				return e
			}
		}
		if _, e = tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_drafts SET state='cancelled',upload_attempt_id=NULL,upload_expires_at=NULL,error_code='DATASET_JOB_CANCELLED',updated_at=clock_timestamp() WHERE id=$1", `UPDATE dataset_drafts SET state='cancelled',upload_attempt_id=NULL,upload_expires_at=NULL,error_code='DATASET_JOB_CANCELLED',updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{id}, []any{id}, transaction.SQL(transaction.Dameng, dmDraftCancel, id))); e != nil {
			return e
		}
		if e = auditEvent(ctx, tx, a, w, "dataset.draft_cancelled", "dataset_draft", id, reason, requestID); e != nil {
			return e
		}
		out, e = lockedDraft(ctx, tx, w, dataset, id)
		return e
	})
	return committed(out, e)
}
func (s *Service) RetryJob(ctx context.Context, a identity.Auth, workspace, dataset, id, reason, key, requestID string) (Job, error) {
	var out Job
	if !wire.ValidID(id) || !wire.ValidID(key) || !validReason(reason) {
		return out, failure("VALIDATION_FAILED")
	}
	intent := fingerprint(struct{ JobID, Reason string }{id, reason}, a.Me.User.ID)
	e := s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		parent, e := lockedDataset(ctx, tx, w, dataset)
		if e != nil {
			return e
		}
		var draftID string
		if e = tx.QueryRow(ctx, datasetSQL(tx, "SELECT draft_id::text FROM dataset_jobs WHERE tenant_id=$1 AND workspace_id=$2 AND dataset_id=$3 AND id=$4", `SELECT draft_id FROM dataset_jobs WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND id=?`, []any{w.TenantID, w.ID, dataset, id}, []any{w.TenantID, w.ID, dataset, id}, transaction.SQL(transaction.Dameng, dmRetryJobDraft, w.TenantID, w.ID, dataset, id))).Scan(&draftID); e != nil {
			return failure("DATASET_NOT_FOUND")
		}
		d, e := lockedDraft(ctx, tx, w, dataset, draftID)
		if e != nil {
			return e
		}
		if e = draftAccess(a, w, d, true); e != nil {
			return e
		}
		j, e := lockedJob(ctx, tx, w, dataset, id)
		if e != nil {
			return e
		}
		if j.Kind == "publish" && !isOwner(a, w) {
			return failure("ACCESS_DENIED")
		}
		var previous string
		var original []byte
		e = tx.QueryRow(ctx, datasetSQL(tx, "SELECT id::text,creation_fingerprint FROM dataset_jobs WHERE tenant_id=$1 AND workspace_id=$2 AND dataset_id=$3 AND draft_id=$4 AND requested_by=$5 AND creation_key=$6", `SELECT id,creation_fingerprint FROM dataset_jobs WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND draft_id=? AND requested_by=? AND creation_key=?`, []any{w.TenantID, w.ID, dataset, draftID, a.Me.User.ID, key}, []any{w.TenantID, w.ID, dataset, draftID, a.Me.User.ID, key}, transaction.SQL(transaction.Dameng, dmRetryFindIntent, w.TenantID, w.ID, dataset, draftID, a.Me.User.ID, key))).Scan(&previous, &original)
		if e == nil {
			if !sameFingerprint(intent, original) {
				return failure("IDEMPOTENCY_CONFLICT")
			}
			out, e = lockedJob(ctx, tx, w, dataset, previous)
			return e
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if parent.RetiredAt != nil {
			return failure("DATASET_RETIRED")
		}
		if j.State != "failed" || !j.Retryable || d.CurrentJobID == nil || *d.CurrentJobID != j.ID || d.Revision != j.ExpectedRevision {
			return failure("DATASET_RETRY_NOT_ALLOWED")
		}
		authority, e := delegatedAuthority(ctx, tx, a, w)
		if e != nil {
			return e
		}
		newID := uuid.NewString()
		version := j.VersionID
		if version != nil {
			v := uuid.NewString()
			version = &v
		}
		digest := j.InputSHA256
		var rule string
		if j.Kind == "publish" {
			previous, _, _, e := datasetJobInput(ctx, tx, j.ID)
			if e != nil {
				return e
			}
			rule = previous.Deduplication
			_, _, value, e := snapshot(ctx, tx, d.ID, s.policy().Limits)
			if e != nil {
				return e
			}
			digest = &value
		}
		_, e = tx.Exec(ctx, datasetSQL(tx, `INSERT INTO dataset_jobs(id,tenant_id,workspace_id,dataset_id,draft_id,kind,requested_by,requested_audience,requested_epoch,membership_authority,reason,expected_revision,state,version_id,input_sha256,input_policy,split_seed,validation_basis_points,retry_of,not_before,creation_key,creation_fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'pending',$13,$14,$15,$16,$17,$18,clock_timestamp()+make_interval(secs=>$19),$20,$21)`, `INSERT INTO dataset_jobs(id,tenant_id,workspace_id,dataset_id,draft_id,kind,requested_by,requested_audience,requested_epoch,membership_authority,reason,expected_revision,state,version_id,input_sha256,input_policy,split_seed,validation_basis_points,retry_of,not_before,creation_key,creation_fingerprint) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,'pending',?,?,CAST(? AS JSON),?,?,?,TIMESTAMPADD(SECOND,?,CURRENT_TIMESTAMP(6)),?,?)`, []any{newID, w.TenantID, w.ID, dataset, d.ID, j.Kind, a.Me.User.ID, a.Me.Audience, a.Epoch, authority, reason, d.Revision, version, digest, s.jobPolicyBytes(rule), j.Seed, j.ValidationBasisPoints, j.ID, s.policy().RetryDelaySeconds, key, intent}, []any{newID, w.TenantID, w.ID, dataset, d.ID, j.Kind, a.Me.User.ID, a.Me.Audience, a.Epoch, authority, reason, d.Revision, version, digest, s.jobPolicyBytes(rule), j.Seed, j.ValidationBasisPoints, j.ID, s.policy().RetryDelaySeconds, key, intent}, transaction.SQL(transaction.Dameng, dmRetryInsertJob, newID, w.TenantID, w.ID, dataset, d.ID, j.Kind, a.Me.User.ID, a.Me.Audience, a.Epoch, authority, reason, d.Revision, version, digest, string(s.jobPolicyBytes(rule)), j.Seed, j.ValidationBasisPoints, j.ID, s.policy().RetryDelaySeconds, key, intent)))
		if e != nil {
			return e
		}
		state := "validating"
		if j.Kind == "publish" {
			state = "publishing"
		}
		if _, e = tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_drafts SET state=$2,current_job_id=$3,error_code=NULL,error_line_number=NULL,updated_at=clock_timestamp() WHERE id=$1", `UPDATE dataset_drafts SET state=?,current_job_id=?,error_code=NULL,error_line_number=NULL,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{d.ID, state, newID}, []any{state, newID, d.ID}, transaction.SQL(transaction.Dameng, dmRetryAttachDraft, state, newID, d.ID))); e != nil {
			return e
		}
		if e = auditEvent(ctx, tx, a, w, "dataset.job_retry_requested", "dataset_job", newID, reason, requestID); e != nil {
			return e
		}
		out, e = lockedJob(ctx, tx, w, dataset, newID)
		return e
	})
	return committed(out, e)
}
