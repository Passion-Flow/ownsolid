package datasets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"strconv"
)

const rowFields = `id::text,line_number,revision,source_id,prompt,completion,error_code,raw_preview,preview_replaced`

func scanRow(row transaction.Row) (Row, error) {
	var r Row
	e := row.Scan(&r.ID, &r.LineNumber, &r.Revision, &r.SourceID, &r.Prompt, &r.Completion, &r.ErrorCode, &r.RawPreview, &r.PreviewReplaced)
	if errors.Is(e, pgx.ErrNoRows) {
		return r, failure("DATASET_NOT_FOUND")
	}
	return r, e
}
func editable(a identity.Auth, w workspaces.Workspace, d Draft) error {
	if e := draftAccess(a, w, d, true); e != nil {
		return e
	}
	if d.FatalSourceError {
		return failure("DATASET_CORRECTED_UPLOAD_REQUIRED")
	}
	if d.State != "editing" {
		return failure("DATASET_DRAFT_BUSY")
	}
	return nil
}
func (s *Service) AppendRow(ctx context.Context, a identity.Auth, workspace, dataset, draft string, input RowRequest, requestID string) (Row, error) {
	if input.RowRevision != 0 {
		return Row{}, failure("VALIDATION_FAILED")
	}
	return s.mutateRow(ctx, a, workspace, dataset, draft, "", "appended", input, requestID)
}
func (s *Service) ReplaceRow(ctx context.Context, a identity.Auth, workspace, dataset, draft, row string, input RowRequest, requestID string) (Row, error) {
	return s.mutateRow(ctx, a, workspace, dataset, draft, row, "replaced", input, requestID)
}
func (s *Service) DeleteRow(ctx context.Context, a identity.Auth, workspace, dataset, draft, row string, expected, rowRevision int64, reason, requestID string) error {
	_, e := s.mutateRow(ctx, a, workspace, dataset, draft, row, "deleted", RowRequest{ExpectedRevision: expected, RowRevision: rowRevision, Reason: reason}, requestID)
	return e
}
func (s *Service) mutateRow(ctx context.Context, a identity.Auth, workspace, dataset, draft, row, action string, input RowRequest, requestID string) (Row, error) {
	var out Row
	if input.ExpectedRevision < 1 || !validReason(input.Reason) || action != "appended" && (!wire.ValidID(row) || input.RowRevision < 1) {
		return out, failure("VALIDATION_FAILED")
	}
	if action != "deleted" {
		if e := input.Record().Validate(s.policy().Limits); e != nil {
			return committed(out, e)
		}
	}
	e := s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		parent, e := lockedDataset(ctx, tx, w, dataset)
		if e != nil {
			return e
		}
		if parent.RetiredAt != nil {
			return failure("DATASET_RETIRED")
		}
		d, e := lockedDraft(ctx, tx, w, dataset, draft)
		if e != nil {
			return e
		}
		if e = editable(a, w, d); e != nil {
			return e
		}
		if d.Revision != input.ExpectedRevision {
			return failure("DATASET_REVISION_CONFLICT")
		}
		if action == "appended" {
			var next int64
			if e = tx.QueryRow(ctx, datasetSQL(tx, "SELECT COALESCE(max(line_number),0)+1 FROM dataset_rows WHERE draft_id=$1", `SELECT COALESCE(max(line_number),0)+1 FROM dataset_rows WHERE draft_id=?`, []any{draft}, []any{draft}, transaction.SQL(transaction.Dameng, dmRowNextLine, draft))).Scan(&next); e != nil {
				return e
			}
			if next > s.policy().Limits.MaximumRows {
				return failure("DATASET_LIMIT_REACHED")
			}
			row = uuid.NewString()
			_, e = tx.Exec(ctx, datasetSQL(tx, `INSERT INTO dataset_rows(id,tenant_id,workspace_id,dataset_id,draft_id,line_number,source_id,prompt,completion) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, `INSERT INTO dataset_rows(id,tenant_id,workspace_id,dataset_id,draft_id,line_number,source_id,prompt,completion) VALUES(?,?,?,?,?,?,?,?,?)`, []any{row, w.TenantID, w.ID, dataset, draft, next, input.SourceID, input.Prompt, input.Completion}, []any{row, w.TenantID, w.ID, dataset, draft, next, input.SourceID, input.Prompt, input.Completion}, transaction.SQL(transaction.Dameng, dmRowInsert, row, w.TenantID, w.ID, dataset, draft, next, input.SourceID, input.Prompt, input.Completion)))
		} else {
			previous, e := scanRow(tx.QueryRow(ctx, datasetSQL(tx, "SELECT "+rowFields+" FROM dataset_rows WHERE tenant_id=$1 AND workspace_id=$2 AND dataset_id=$3 AND draft_id=$4 AND id=$5 AND NOT deleted FOR UPDATE", `SELECT id,line_number,revision,source_id,prompt,completion,error_code,raw_preview,preview_replaced FROM dataset_rows WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND draft_id=? AND id=? AND NOT deleted FOR UPDATE`, []any{w.TenantID, w.ID, dataset, draft, row}, []any{w.TenantID, w.ID, dataset, draft, row}, transaction.SQL(transaction.Dameng, dmRowLock, w.TenantID, w.ID, dataset, draft, row))))
			if e != nil {
				return e
			}
			if previous.Revision != input.RowRevision {
				return failure("DATASET_REVISION_CONFLICT")
			}
			if action == "deleted" {
				_, e = tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_rows SET deleted=true,revision=revision+1,updated_at=clock_timestamp() WHERE id=$1", `UPDATE dataset_rows SET deleted=true,revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{row}, []any{row}, transaction.SQL(transaction.Dameng, dmRowDelete, row)))
			} else {
				_, e = tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_rows SET source_id=$2,prompt=$3,completion=$4,error_code=NULL,raw_preview='',preview_replaced=false,revision=revision+1,updated_at=clock_timestamp() WHERE id=$1", `UPDATE dataset_rows SET source_id=?,prompt=?,completion=?,error_code=NULL,raw_preview='',preview_replaced=false,revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{row, input.SourceID, input.Prompt, input.Completion}, []any{input.SourceID, input.Prompt, input.Completion, row}, transaction.SQL(transaction.Dameng, dmRowReplace, input.SourceID, input.Prompt, input.Completion, row)))
			}
		}
		if e != nil {
			return e
		}
		out, e = scanRow(tx.QueryRow(ctx, datasetSQL(tx, "SELECT "+rowFields+" FROM dataset_rows WHERE id=$1", `SELECT id,line_number,revision,source_id,prompt,completion,error_code,raw_preview,preview_replaced FROM dataset_rows WHERE id=?`, []any{row}, []any{row}, transaction.SQL(transaction.Dameng, dmRowRead, row))))
		if e != nil {
			return e
		}
		var digest *string
		if out.ErrorCode == nil {
			b, e := (wire.Record{SourceID: *out.SourceID, Prompt: *out.Prompt, Completion: *out.Completion}).Canonical(s.policy().Limits)
			if e != nil {
				return e
			}
			h := sha256.Sum256(b)
			value := hex.EncodeToString(h[:])
			digest = &value
		}
		_, e = tx.Exec(ctx, datasetSQL(tx, `INSERT INTO dataset_row_history(tenant_id,workspace_id,dataset_id,draft_id,row_id,revision,actor_user_id,action,content_sha256,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, `INSERT INTO dataset_row_history(tenant_id,workspace_id,dataset_id,draft_id,row_id,revision,actor_user_id,action,content_sha256,reason) VALUES(?,?,?,?,?,?,?,?,?,?)`, []any{w.TenantID, w.ID, dataset, draft, row, out.Revision, a.Me.User.ID, action, digest, input.Reason}, []any{w.TenantID, w.ID, dataset, draft, row, out.Revision, a.Me.User.ID, action, digest, input.Reason}, transaction.SQL(transaction.Dameng, dmRowHistoryInsert, w.TenantID, w.ID, dataset, draft, row, out.Revision, a.Me.User.ID, action, digest, input.Reason)))
		if e != nil {
			return e
		}
		if e = s.refreshDraftCounts(ctx, tx, draft); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_drafts SET revision=revision+1,updated_at=clock_timestamp() WHERE id=$1", `UPDATE dataset_drafts SET revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{draft}, []any{draft}, transaction.SQL(transaction.Dameng, dmDraftAdvanceRevision, draft))); e != nil {
			return e
		}
		return auditEvent(ctx, tx, a, w, "dataset.row_"+action, "dataset_row", row, input.Reason, requestID)
	})
	return committed(out, e)
}
func (s *Service) refreshDraftCounts(ctx context.Context, tx transaction.Executor, draft string) error {
	if tx.Dialect() == transaction.Dameng && (s.policy().Limits.MaximumErrors < 1 || s.policy().Limits.MaximumErrors > wire.MaximumErrorsCeiling) {
		return failure("VALIDATION_FAILED")
	}
	var total, invalid int64
	if e := tx.QueryRow(ctx, datasetSQL(tx, "SELECT count(*),count(*) FILTER(WHERE error_code IS NOT NULL) FROM dataset_rows WHERE draft_id=$1 AND NOT deleted", `SELECT count(*),COALESCE(SUM(error_code IS NOT NULL),0) FROM dataset_rows WHERE draft_id=? AND NOT deleted`, []any{draft}, []any{draft}, transaction.SQL(transaction.Dameng, dmDraftRowCounts, draft))).Scan(&total, &invalid); e != nil {
		return e
	}
	rows, e := tx.Query(ctx, datasetSQL(tx, "SELECT line_number,error_code,raw_preview,preview_replaced FROM dataset_rows WHERE draft_id=$1 AND NOT deleted AND error_code IS NOT NULL ORDER BY line_number LIMIT $2", `SELECT line_number,error_code,raw_preview,preview_replaced FROM dataset_rows WHERE draft_id=? AND NOT deleted AND error_code IS NOT NULL ORDER BY line_number LIMIT ?`, []any{draft, s.policy().Limits.MaximumErrors}, []any{draft, s.policy().Limits.MaximumErrors}, transaction.SQL(transaction.Dameng, dmDraftErrorsPage+strconv.FormatInt(s.policy().Limits.MaximumErrors, 10), draft)))
	if e != nil {
		return e
	}
	lineErrors := []wire.LineError{}
	for rows.Next() {
		var item wire.LineError
		if e = rows.Scan(&item.LineNumber, &item.Code, &item.Preview, &item.PreviewReplaced); e != nil {
			rows.Close()
			return e
		}
		lineErrors = append(lineErrors, item)
	}
	rows.Close()
	if rows.Err() != nil {
		return rows.Err()
	}
	raw, _ := json.Marshal(lineErrors)
	_, e = tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_drafts SET total_rows=$2,invalid_rows=$3,validation_errors=$4,errors_truncated=$5,error_code=CASE WHEN $3>0 THEN 'DATASET_VALIDATION_FAILED' END,error_line_number=NULL,updated_at=clock_timestamp() WHERE id=$1", `UPDATE dataset_drafts SET total_rows=?,invalid_rows=?,validation_errors=CAST(? AS JSON),errors_truncated=?,error_code=CASE WHEN ?>0 THEN 'DATASET_VALIDATION_FAILED' END,error_line_number=NULL,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{draft, total, invalid, raw, invalid > int64(len(lineErrors))}, []any{total, invalid, raw, invalid > int64(len(lineErrors)), invalid, draft}, transaction.SQL(transaction.Dameng, dmDraftRefreshCounts, total, invalid, string(raw), invalid > int64(len(lineErrors)), invalid, draft)))
	return e
}
