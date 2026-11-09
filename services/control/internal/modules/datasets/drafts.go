package datasets

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const draftFields = `error_line_number,id::text,dataset_id::text,workspace_id::text,created_by::text,parent_version_id::text,source_name,source_sha256,expected_bytes,source_received_at,state,revision,CASE WHEN validation_completed_at IS NOT NULL THEN total_rows END,CASE WHEN validation_completed_at IS NOT NULL THEN invalid_rows END,validation_completed_at,fatal_source_error,validation_errors,errors_truncated,error_code,current_job_id::text,published_version_id::text,created_at,updated_at`

func scanDraft(row transaction.Row) (Draft, error) {
	var d Draft
	var raw []byte
	e := row.Scan(&d.ErrorLineNumber, &d.ID, &d.DatasetID, &d.WorkspaceID, &d.CreatedBy, &d.ParentVersionID, &d.SourceName, &d.SourceSHA256, &d.ExpectedBytes, &d.SourceReceivedAt, &d.State, &d.Revision, &d.TotalRows, &d.InvalidRows, &d.ValidationCompletedAt, &d.FatalSourceError, &raw, &d.ErrorsTruncated, &d.ErrorCode, &d.CurrentJobID, &d.PublishedVersionID, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return Draft{}, failure("DATASET_NOT_FOUND")
	}
	if e != nil {
		return committed(d, e)
	}
	if json.Unmarshal(raw, &d.ValidationErrors) != nil {
		return Draft{}, failure("DATASET_DATA_INVALID")
	}
	if e != nil {
		return Draft{}, e
	}
	d.CreatedAt = d.CreatedAt.UTC()
	d.UpdatedAt = d.UpdatedAt.UTC()
	if d.SourceReceivedAt != nil {
		stamp := d.SourceReceivedAt.UTC()
		d.SourceReceivedAt = &stamp
	}
	if d.ValidationCompletedAt != nil {
		stamp := d.ValidationCompletedAt.UTC()
		d.ValidationCompletedAt = &stamp
	}

	return d, nil
}
func lockedDraft(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, dataset, id string) (Draft, error) {
	if !wire.ValidID(id) {
		return Draft{}, failure("DATASET_NOT_FOUND")
	}
	return scanDraft(tx.QueryRow(ctx, datasetSQL(tx, "SELECT "+draftFields+" FROM dataset_drafts WHERE tenant_id=$1 AND workspace_id=$2 AND dataset_id=$3 AND id=$4 FOR UPDATE", `SELECT error_line_number,id,dataset_id,workspace_id,created_by,parent_version_id,source_name,source_sha256,expected_bytes,source_received_at,state,revision,CASE WHEN validation_completed_at IS NOT NULL THEN total_rows END,CASE WHEN validation_completed_at IS NOT NULL THEN invalid_rows END,validation_completed_at,fatal_source_error,validation_errors,errors_truncated,error_code,current_job_id,published_version_id,created_at,updated_at FROM dataset_drafts WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND id=? FOR UPDATE`, []any{w.TenantID, w.ID, dataset, id}, []any{w.TenantID, w.ID, dataset, id}, transaction.SQL(transaction.Dameng, dmDraftLock, w.TenantID, w.ID, dataset, id))))
}
func draftAccess(a identity.Auth, w workspaces.Workspace, d Draft, write bool) error {
	if !canWrite(a, w) {
		return failure("ACCESS_DENIED")
	}
	if write && !isOwner(a, w) && d.CreatedBy != a.Me.User.ID {
		return failure("ACCESS_DENIED")
	}
	return nil
}
func (s *Service) CreateDraft(ctx context.Context, a identity.Auth, workspace, dataset string, input DraftRequest, key, requestID string) (Draft, error) {
	return s.createDraft(ctx, a, workspace, dataset, input, key, requestID, "")
}
func (s *Service) createDraft(ctx context.Context, a identity.Auth, workspace, dataset string, input DraftRequest, key, requestID, parentVersion string) (Draft, error) {
	var out Draft
	if !wire.ValidID(key) || !validDraftSourceName(input.SourceName) || !wire.ValidDigest(input.SourceSHA256) || input.ExpectedBytes < 1 || input.ExpectedBytes > s.policy().Limits.MaximumBytes || !validReason(input.Reason) {
		return out, failure("VALIDATION_FAILED")
	}
	intent := fingerprint(struct {
		Input         DraftRequest
		ParentVersion string
	}{input, parentVersion}, a.Me.User.ID)
	e := s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if e := s.mongoAttachmentFence(ctx, tx, a, w, dataset); e != nil {
			return e
		}
		d, e := lockedDataset(ctx, tx, w, dataset)
		if e != nil {
			return e
		}
		var id string
		var original []byte
		e = tx.QueryRow(ctx, datasetSQL(tx, "SELECT id::text,creation_fingerprint FROM dataset_drafts WHERE tenant_id=$1 AND workspace_id=$2 AND dataset_id=$3 AND created_by=$4 AND creation_key=$5", `SELECT id,creation_fingerprint FROM dataset_drafts WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND created_by=? AND creation_key=?`, []any{w.TenantID, w.ID, dataset, a.Me.User.ID, key}, []any{w.TenantID, w.ID, dataset, a.Me.User.ID, key}, transaction.SQL(transaction.Dameng, dmDraftFindIntent, w.TenantID, w.ID, dataset, a.Me.User.ID, key))).Scan(&id, &original)
		if e == nil {
			if !sameFingerprint(original, intent) {
				return failure("IDEMPOTENCY_CONFLICT")
			}
			out, e = lockedDraft(ctx, tx, w, dataset, id)
			return e
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if d.RetiredAt != nil {
			return failure("DATASET_RETIRED")
		}
		var parentID *string
		if parentVersion != "" {
			parent, e := lockedVersion(ctx, tx, w, dataset, parentVersion)
			if e != nil {
				return e
			}
			if parent.RetiredAt != nil {
				return failure("DATASET_VERSION_RETIRED")
			}
			if s.Store == nil || parent.EffectiveSourceSHA256 != input.SourceSHA256 {
				return failure("DATASET_FILE_INVALID")
			}
			file, size, e := s.Store.OpenEffective(ctx, parent.ID, parent.AttemptID, parent.EffectiveSourceSHA256, s.policy().Limits.MaximumBytes)
			if e != nil {
				return e
			}
			file.Close()
			if size != input.ExpectedBytes {
				return failure("DATASET_FILE_INVALID")
			}

			parentID = &parentVersion
		}

		if e = s.checkDraftCapacity(ctx, tx, w); e != nil {
			return e
		}
		authority, e := delegatedAuthority(ctx, tx, a, w)
		if e != nil {
			return e
		}
		id = uuid.NewString()
		e = insertDraft(ctx, tx, a, w, dataset, id, uuid.NewString(), input, key, intent, parentID, authority)
		if e != nil {
			return e
		}
		if e = auditEvent(ctx, tx, a, w, "dataset.draft_created", "dataset_draft", id, input.Reason, requestID); e != nil {
			return e
		}
		out, e = lockedDraft(ctx, tx, w, dataset, id)
		return e
	})
	return committed(out, e)
}
func validDraftSourceName(name string) bool {
	return validText(name, 255) && !strings.ContainsAny(name, "/\\") && filepath.Base(name) == name && name != "." && name != ".."
}

func (s *Service) checkDraftCapacity(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
	var count int
	if e := tx.QueryRow(ctx, datasetSQL(tx, "SELECT count(*) FROM dataset_drafts WHERE tenant_id=$1 AND workspace_id=$2 AND state NOT IN ('published','cancelled')", `SELECT count(*) FROM dataset_drafts WHERE tenant_id=? AND workspace_id=? AND state NOT IN ('published','cancelled')`, []any{w.TenantID, w.ID}, []any{w.TenantID, w.ID}, transaction.SQL(transaction.Dameng, dmDraftActiveCount, w.TenantID, w.ID))).Scan(&count); e != nil {
		return e
	}
	if count >= s.policy().MaxActiveDrafts {
		return failure("DATASET_LIMIT_REACHED")
	}
	return nil
}

func insertDraft(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, dataset, id, object string, input DraftRequest, key string, intent []byte, parentID, authority *string) error {
	_, e := tx.Exec(ctx, datasetSQL(tx, `INSERT INTO dataset_drafts(id,tenant_id,workspace_id,dataset_id,created_by,requested_audience,requested_epoch,membership_authority,source_object_id,source_name,source_sha256,expected_bytes,state,creation_key,creation_fingerprint,parent_version_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'uploading',$13,$14,$15)`, `INSERT INTO dataset_drafts(id,tenant_id,workspace_id,dataset_id,created_by,requested_audience,requested_epoch,membership_authority,source_object_id,source_name,source_sha256,expected_bytes,state,creation_key,creation_fingerprint,parent_version_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,'uploading',?,?,?)`, []any{id, w.TenantID, w.ID, dataset, a.Me.User.ID, a.Me.Audience, a.Epoch, authority, object, input.SourceName, input.SourceSHA256, input.ExpectedBytes, key, intent, parentID}, []any{id, w.TenantID, w.ID, dataset, a.Me.User.ID, a.Me.Audience, a.Epoch, authority, object, input.SourceName, input.SourceSHA256, input.ExpectedBytes, key, intent, parentID}, transaction.SQL(transaction.Dameng, dmDraftInsert, id, w.TenantID, w.ID, dataset, a.Me.User.ID, a.Me.Audience, a.Epoch, authority, object, input.SourceName, input.SourceSHA256, input.ExpectedBytes, key, intent, parentID)))
	return e
}

func (s *Service) GetDraft(ctx context.Context, a identity.Auth, workspace, dataset, id string) (Draft, error) {
	var out Draft
	e := s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if _, e := lockedDataset(ctx, tx, w, dataset); e != nil {
			return e
		}
		var e error
		out, e = lockedDraft(ctx, tx, w, dataset, id)
		if e != nil {
			return e
		}
		return draftAccess(a, w, out, false)
	})
	return committed(out, e)
}
