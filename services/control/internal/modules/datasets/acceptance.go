package datasets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

func (s *Service) acceptInspection(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, d Draft, j Job, result wire.ProcessorResult) error {
	if result.Status != "valid" && result.Status != "invalid" {
		return failure("DATASET_PROCESSOR_RESULT_INVALID")
	}
	var existing int
	if e := tx.QueryRow(ctx, datasetSQL(tx, "SELECT count(*) FROM dataset_rows WHERE draft_id=$1", `SELECT count(*) FROM dataset_rows WHERE draft_id=?`, []any{d.ID}, []any{d.ID}, transaction.SQL(transaction.Dameng, dmInspectionExistingRows, d.ID))).Scan(&existing); e != nil {
		return e
	}
	if existing != 0 || d.ValidationCompletedAt != nil {
		return failure("DATASET_ATTEMPT_STALE")
	}
	parsed := map[int64]wire.ProcessedRow{}
	lineErrors := map[int64]wire.LineError{}
	for _, r := range result.Rows {
		parsed[r.LineNumber] = r
	}
	for _, err := range result.Errors {
		lineErrors[err.LineNumber] = err
	}
	records := make([][]any, 0, result.TotalRows)
	history := make([][]any, 0, result.TotalRows)
	for line := int64(1); line <= result.TotalRows; line++ {
		id := uuid.NewString()
		var source, prompt, completion, errorCode, digest any
		preview := ""
		replaced := false
		if r, ok := parsed[line]; ok {
			source = r.SourceID
			prompt = r.Prompt
			completion = r.Completion
			raw, e := r.Record().Canonical(s.policy().Limits)
			if e != nil {
				return e
			}
			sum := sha256.Sum256(raw)
			digest = hex.EncodeToString(sum[:])
		} else {
			err, ok := lineErrors[line]
			if ok {
				errorCode = err.Code
				preview = err.Preview
				replaced = err.PreviewReplaced
			} else {
				errorCode = "DATASET_ROW_ERROR_UNREPORTED"
			}
		}
		records = append(records, []any{id, w.TenantID, w.ID, j.DatasetID, d.ID, line, source, prompt, completion, errorCode, preview, replaced})
		history = append(history, []any{w.TenantID, w.ID, j.DatasetID, d.ID, id, int64(1), a.Me.User.ID, "imported", digest, "Validate uploaded source"})
	}
	if e := insertInspectedRows(ctx, tx, records, history); e != nil {
		return e
	}

	state := "editing"
	var code any
	if result.InvalidRows > 0 {
		code = "DATASET_VALIDATION_FAILED"
	}
	if result.FatalSourceError {
		state = "failed"
		code = "DATASET_CORRECTED_UPLOAD_REQUIRED"
	}
	raw, _ := json.Marshal(result.Errors)
	if _, e := tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_drafts SET state=$2,total_rows=$3,invalid_rows=$4,validation_completed_at=clock_timestamp(),fatal_source_error=$5,validation_errors=$6,errors_truncated=$7,error_code=$8,error_line_number=$9,updated_at=clock_timestamp() WHERE id=$1", `UPDATE dataset_drafts SET state=?,total_rows=?,invalid_rows=?,validation_completed_at=CURRENT_TIMESTAMP(6),fatal_source_error=?,validation_errors=CAST(? AS JSON),errors_truncated=?,error_code=?,error_line_number=?,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{d.ID, state, result.TotalRows, result.InvalidRows, result.FatalSourceError, raw, result.ErrorsTruncated, code, result.FatalSourceLineNumber}, []any{state, result.TotalRows, result.InvalidRows, result.FatalSourceError, raw, result.ErrorsTruncated, code, result.FatalSourceLineNumber, d.ID}, transaction.SQL(transaction.Dameng, dmInspectionAccept, state, result.TotalRows, result.InvalidRows, result.FatalSourceError, string(raw), result.ErrorsTruncated, code, result.FatalSourceLineNumber, d.ID))); e != nil {
		return e
	}
	return auditEvent(ctx, tx, a, w, "dataset.source_validated", "dataset_draft", d.ID, "Validate uploaded source", j.ID)
}
func (s *Service) acceptPublication(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, parent Dataset, d Draft, j Job, result wire.ProcessorResult, mappingSHA string) error {
	if result.Status != "prepared" || result.Manifest == nil || j.VersionID == nil || j.AttemptID == nil || j.InputSHA256 == nil {
		return failure("DATASET_PROCESSOR_RESULT_INVALID")
	}
	if e := s.Store.VerifyVersionContext(ctx, *j.VersionID, d.ID, j.ID, *j.AttemptID, *j.InputSHA256, mappingSHA, *result.Manifest); e != nil {
		return e
	}
	var count int
	if e := tx.QueryRow(ctx, datasetSQL(tx, "SELECT count(*) FROM dataset_versions WHERE dataset_id=$1", `SELECT count(*) FROM dataset_versions WHERE dataset_id=?`, []any{parent.ID}, []any{parent.ID}, transaction.SQL(transaction.Dameng, dmPublicationVersionCount, parent.ID))).Scan(&count); e != nil {
		return e
	}
	if count >= s.policy().MaxVersionsPerDataset {
		return failure("DATASET_LIMIT_REACHED")
	}
	var ordinal int64
	// The dataset lock is already held; read and update share this transaction.
	if e := tx.QueryRow(ctx, datasetSQL(tx, "SELECT next_version FROM datasets WHERE id=$1 FOR UPDATE", `SELECT next_version FROM datasets WHERE id=? FOR UPDATE`, []any{parent.ID}, []any{parent.ID}, transaction.SQL(transaction.Dameng, dmPublicationNextOrdinal, parent.ID))).Scan(&ordinal); e != nil {
		return e
	}
	if _, e := tx.Exec(ctx, datasetSQL(tx, "UPDATE datasets SET next_version=next_version+1,revision=revision+1,updated_at=clock_timestamp() WHERE id=$1", `UPDATE datasets SET next_version=next_version+1,revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{parent.ID}, []any{parent.ID}, transaction.SQL(transaction.Dameng, dmPublicationAdvanceOrdinal, parent.ID))); e != nil {
		return e
	}
	raw, _ := json.Marshal(result.Manifest)
	_, e := tx.Exec(ctx, datasetSQL(tx, `INSERT INTO dataset_versions(id,tenant_id,workspace_id,dataset_id,draft_id,job_id,attempt_id,ordinal,parent_version_id,published_by,raw_source_sha256,effective_source_sha256,manifest_sha256,manifest,row_mapping_sha256) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, `INSERT INTO dataset_versions(id,tenant_id,workspace_id,dataset_id,draft_id,job_id,attempt_id,ordinal,parent_version_id,published_by,raw_source_sha256,effective_source_sha256,manifest_sha256,manifest,row_mapping_sha256) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,CAST(? AS JSON),?)`, []any{*j.VersionID, w.TenantID, w.ID, parent.ID, d.ID, j.ID, *j.AttemptID, ordinal, d.ParentVersionID, a.Me.User.ID, d.SourceSHA256, *j.InputSHA256, result.Manifest.ManifestFile.SHA256, raw, mappingSHA}, []any{*j.VersionID, w.TenantID, w.ID, parent.ID, d.ID, j.ID, *j.AttemptID, ordinal, d.ParentVersionID, a.Me.User.ID, d.SourceSHA256, *j.InputSHA256, result.Manifest.ManifestFile.SHA256, raw, mappingSHA}, transaction.SQL(transaction.Dameng, dmPublicationInsertVersion, *j.VersionID, w.TenantID, w.ID, parent.ID, d.ID, j.ID, *j.AttemptID, ordinal, d.ParentVersionID, a.Me.User.ID, d.SourceSHA256, *j.InputSHA256, result.Manifest.ManifestFile.SHA256, string(raw), mappingSHA)))
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_drafts SET state='published',published_version_id=$2,error_code=NULL,updated_at=clock_timestamp() WHERE id=$1", `UPDATE dataset_drafts SET state='published',published_version_id=?,error_code=NULL,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{d.ID, *j.VersionID}, []any{*j.VersionID, d.ID}, transaction.SQL(transaction.Dameng, dmPublicationCompleteDraft, *j.VersionID, d.ID))); e != nil {
		return e
	}
	return auditEvent(ctx, tx, a, w, "dataset.version_published", "dataset_version", *j.VersionID, j.Reason, j.ID)
}
