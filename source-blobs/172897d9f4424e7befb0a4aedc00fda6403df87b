package knowledge

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
)

type documentRecord struct {
	Document
	visibleID, pendingID, attemptID *string
}

const documentFields = `id::text,base_id::text,workspace_id::text,name,revision,state,visible_source_id::text,pending_source_id::text,current_attempt_id::text,created_by::text,created_at,updated_at,deleted_at`

func scanDocument(row transaction.Row) (documentRecord, error) {
	var d documentRecord
	e := row.Scan(&d.ID, &d.BaseID, &d.WorkspaceID, &d.Name, &d.Revision, &d.State, &d.visibleID, &d.pendingID, &d.attemptID, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt, &d.DeletedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return d, failure("KNOWLEDGE_DOCUMENT_NOT_FOUND")
	}
	if e != nil {
		return documentRecord{}, e
	}
	d.CreatedAt = d.CreatedAt.UTC()
	d.UpdatedAt = d.UpdatedAt.UTC()
	d.DeletedAt = utcKnowledge(d.DeletedAt)
	return d, nil
}
func lockedDocument(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, base, id string, deleted bool) (documentRecord, error) {
	if !validID(id) {
		return documentRecord{}, failure("KNOWLEDGE_DOCUMENT_NOT_FOUND")
	}
	d, e := scanDocument(tx.QueryRow(ctx, knowledgeSQL(tx, "SELECT "+documentFields+" FROM knowledge_documents WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3 AND id=$4 FOR UPDATE", `SELECT id,base_id,workspace_id,name,revision,state,visible_source_id,pending_source_id,current_attempt_id,created_by,created_at,updated_at,deleted_at FROM knowledge_documents WHERE tenant_id=? AND workspace_id=? AND base_id=? AND id=? FOR UPDATE`, []any{w.TenantID, w.ID, base, id}, []any{w.TenantID, w.ID, base, id})))
	if e == nil && !deleted && d.DeletedAt != nil {
		return d, failure("KNOWLEDGE_DOCUMENT_NOT_FOUND")
	}
	return knowledgeCommitted(d, e)
}
func hydrateDocument(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, d documentRecord) (Document, error) {
	for _, item := range []struct {
		id     *string
		target **SourceMetadata
	}{{d.visibleID, &d.VisibleSource}, {d.pendingID, &d.PendingSource}} {
		if item.id == nil {
			continue
		}
		var source SourceMetadata
		if e := tx.QueryRow(ctx, knowledgeSQL(tx, `SELECT id::text,source_version,sha256,size_bytes,created_at FROM knowledge_source_versions WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3 AND document_id=$4 AND id=$5`, `SELECT id,source_version,sha256,size_bytes,created_at FROM knowledge_source_versions WHERE tenant_id=? AND workspace_id=? AND base_id=? AND document_id=? AND id=?`, []any{w.TenantID, w.ID, d.BaseID, d.ID, *item.id}, []any{w.TenantID, w.ID, d.BaseID, d.ID, *item.id})).Scan(&source.ID, &source.SourceVersion, &source.SHA256, &source.SizeBytes, &source.CreatedAt); e != nil {
			return knowledgeCommitted(d.Document, e)
		}
		source.CreatedAt = source.CreatedAt.UTC()
		*item.target = &source
	}
	if d.attemptID != nil {
		var attempt Attempt
		e := tx.QueryRow(ctx, knowledgeSQL(tx, `SELECT id::text,kind,state,attempt_number,chunk_count,error_code,requested_by::text,created_at,started_at,finished_at FROM knowledge_attempts WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3 AND document_id=$4 AND id=$5`, `SELECT id,kind,state,attempt_number,chunk_count,error_code,requested_by,created_at,started_at,finished_at FROM knowledge_attempts WHERE tenant_id=? AND workspace_id=? AND base_id=? AND document_id=? AND id=?`, []any{w.TenantID, w.ID, d.BaseID, d.ID, *d.attemptID}, []any{w.TenantID, w.ID, d.BaseID, d.ID, *d.attemptID})).Scan(&attempt.ID, &attempt.Kind, &attempt.State, &attempt.AttemptNumber, &attempt.ChunkCount, &attempt.ErrorCode, &attempt.RequestedBy, &attempt.CreatedAt, &attempt.StartedAt, &attempt.FinishedAt)
		if e != nil {
			return knowledgeCommitted(d.Document, e)
		}
		attempt.CreatedAt = attempt.CreatedAt.UTC()
		attempt.StartedAt = utcKnowledge(attempt.StartedAt)
		attempt.FinishedAt = utcKnowledge(attempt.FinishedAt)
		d.CurrentAttempt = &attempt
	}
	return d.Document, nil
}
func (s *Service) writableBase(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, base string) (baseRecord, error) {
	b, e := lockedBase(ctx, tx, w, base)
	if e != nil {
		return knowledgeCommitted(b, e)
	}
	if !b.Enabled {
		return b, failure("KNOWLEDGE_BASE_DISABLED")
	}
	if b.buildingID != nil {
		return b, failure("KNOWLEDGE_BUSY")
	}
	generation, e := loadGeneration(ctx, tx, w, base, b.activeID)
	if e != nil {
		return knowledgeCommitted(b, e)
	}
	if generation.CatalogSHA256 != CatalogDigest() || generation.Dimension != EmbeddingDimension {
		return b, failure("KNOWLEDGE_INDEX_INCOMPATIBLE")
	}
	return b, nil
}
func (s *Service) validSource(text, digest string) error {
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || strings.ContainsRune(text, 0) || len(text) > s.policy().SourceBytes || hashBytes([]byte(text)) != digest {
		return failure("KNOWLEDGE_INVALID_SOURCE")
	}
	for _, r := range text {
		if r < 32 && r != '\t' && r != '\r' && r != '\n' {
			return failure("KNOWLEDGE_INVALID_SOURCE")
		}
	}
	return nil
}
func (s *Service) insertAttempt(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, base, document, source, generation, kind string, revision int64) (string, error) {
	var count int
	if e := tx.QueryRow(ctx, knowledgeSQL(tx, `SELECT coalesce(max(attempt_number),0) FROM knowledge_attempts WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3 AND document_id=$4 AND source_id=$5 AND generation_id=$6`, `SELECT coalesce(max(attempt_number),0) FROM knowledge_attempts WHERE tenant_id=? AND workspace_id=? AND base_id=? AND document_id=? AND source_id=? AND generation_id=?`, []any{w.TenantID, w.ID, base, document, source, generation}, []any{w.TenantID, w.ID, base, document, source, generation})).Scan(&count); e != nil {
		return "", e
	}
	if count >= s.policy().MaximumAttempts {
		return "", failure("KNOWLEDGE_RETRY_NOT_ALLOWED")
	}
	member, e := delegation(ctx, tx, a, w)
	if e != nil {
		return "", e
	}
	id := uuid.NewString()
	_, e = tx.Exec(ctx, knowledgeSQL(tx, `INSERT INTO knowledge_attempts(id,tenant_id,workspace_id,base_id,document_id,source_id,generation_id,kind,attempt_number,expected_document_revision,requested_by,requested_audience,requested_epoch,membership_authority,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'pending')`, `INSERT INTO knowledge_attempts(id,tenant_id,workspace_id,base_id,document_id,source_id,generation_id,kind,attempt_number,expected_document_revision,requested_by,requested_audience,requested_epoch,membership_authority,state) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'pending')`, []any{id, w.TenantID, w.ID, base, document, source, generation, kind, count + 1, revision, a.Me.User.ID, a.Me.Audience, a.Epoch, member}, []any{id, w.TenantID, w.ID, base, document, source, generation, kind, count + 1, revision, a.Me.User.ID, a.Me.Audience, a.Epoch, member}))
	return id, e
}
func (s *Service) GetDocument(ctx context.Context, a identity.Auth, workspace, base, id string) (Document, error) {
	var out Document
	e := s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if _, e := lockedBase(ctx, tx, w, base); e != nil {
			return e
		}
		d, e := lockedDocument(ctx, tx, w, base, id, false)
		if e != nil {
			return e
		}
		out, e = hydrateDocument(ctx, tx, w, d)
		return e
	})
	return knowledgeCommitted(out, e)
}
func (s *Service) DocumentPage(ctx context.Context, a identity.Auth, workspace, base string, values url.Values) (Page[Document], error) {
	out := Page[Document]{Items: []Document{}}
	limit, cursor, scope, e := pagination.Parse(s.pageValues(values), "knowledge:documents:"+a.Me.Audience+":"+a.Me.User.ID+":"+workspace+":"+base)
	if e != nil {
		return knowledgeCommitted(out, e)
	}
	e = s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if _, e := lockedBase(ctx, tx, w, base); e != nil {
			return e
		}
		query := "SELECT " + documentFields + " FROM knowledge_documents WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3 AND deleted_at IS NULL"
		myQuery := "SELECT " + mysqlDocumentFields + " FROM knowledge_documents WHERE tenant_id=? AND workspace_id=? AND base_id=? AND deleted_at IS NULL"
		args := []any{w.TenantID, w.ID, base}
		if cursor.ID != "" {
			args = append(args, cursor.Time, cursor.ID)
			query += " AND (created_at,id)<($4,$5::uuid)"
			myQuery += " AND (created_at,id)<(?,?)"
		}
		args = append(args, limit+1)
		query += " ORDER BY created_at DESC,id DESC LIMIT $" + strconv.Itoa(len(args))
		myQuery += " ORDER BY created_at DESC,id DESC LIMIT ?"
		rows, e := tx.Query(ctx, knowledgeSQL(tx, query, myQuery, args, args))
		if e != nil {
			return e
		}
		var records []documentRecord
		for rows.Next() {
			d, e := scanDocument(rows)
			if e != nil {
				rows.Close()
				return e
			}
			records = append(records, d)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		if len(records) > limit {
			records = records[:limit]
			last := records[limit-1]
			out.NextCursor = pagination.Encode(last.CreatedAt, last.ID, scope)
		}
		for _, record := range records {
			d, e := hydrateDocument(ctx, tx, w, record)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, d)
		}
		return nil
	})
	return knowledgeCommitted(out, e)
}
func (s *Service) Upload(ctx context.Context, a identity.Auth, workspace, base string, input UploadDocument, key, request string) (Document, error) {
	var out Document
	if e := s.enabled(); e != nil {
		return knowledgeCommitted(out, e)
	}
	if !validReason(input.Reason) {
		return out, failure("VALIDATION_FAILED")
	}
	if e := s.validSource(input.Content, input.SHA256); e != nil {
		return knowledgeCommitted(out, e)
	}
	name, nameKey, e := normalizedName(input.Name, 255)
	if e != nil || !strings.EqualFold(filepath.Ext(name), ".txt") || strings.ContainsAny(name, "/\\") {
		return out, failure("KNOWLEDGE_INVALID_SOURCE")
	}
	input.Name = name
	e = s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		b, e := s.writableBase(ctx, tx, w, base)
		if e != nil {
			return e
		}
		binding := struct {
			Base  string
			Input UploadDocument
		}{base, input}
		previous, digest, e := s.intent(ctx, tx, a, w, "upload_source", key, binding)
		if e != nil {
			return e
		}
		if previous != "" {
			d, e := lockedDocument(ctx, tx, w, base, previous, true)
			if e != nil {
				return e
			}
			out, e = hydrateDocument(ctx, tx, w, d)
			return e
		}
		var count int
		if e = tx.QueryRow(ctx, knowledgeSQL(tx, "SELECT count(*) FROM knowledge_documents WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3 AND deleted_at IS NULL", `SELECT count(*) FROM knowledge_documents WHERE tenant_id=? AND workspace_id=? AND base_id=? AND deleted_at IS NULL`, []any{w.TenantID, w.ID, base}, []any{w.TenantID, w.ID, base})).Scan(&count); e != nil {
			return e
		}
		if count >= s.policy().MaximumDocuments {
			return failure("KNOWLEDGE_LIMIT_REACHED")
		}
		id, source := uuid.NewString(), uuid.NewString()
		if _, e = tx.Exec(ctx, knowledgeSQL(tx, `INSERT INTO knowledge_documents(id,tenant_id,workspace_id,base_id,name,name_key,state,pending_source_id,created_by) VALUES($1,$2,$3,$4,$5,$6,'pending',$7,$8)`, `INSERT INTO knowledge_documents(id,tenant_id,workspace_id,base_id,name,name_key,state,pending_source_id,created_by) VALUES(?,?,?,?,?,?,'pending',NULL,?)`, []any{id, w.TenantID, w.ID, base, name, nameKey, source, a.Me.User.ID}, []any{id, w.TenantID, w.ID, base, name, nameKey, a.Me.User.ID})); e != nil {
			return knowledgeUnique(e, "KNOWLEDGE_DOCUMENT_EXISTS")
		}
		if _, e = tx.Exec(ctx, knowledgeSQL(tx, `INSERT INTO knowledge_source_versions(id,tenant_id,workspace_id,base_id,document_id,source_version,content,sha256,created_by) VALUES($1,$2,$3,$4,$5,1,$6,$7,$8)`, `INSERT INTO knowledge_source_versions(id,tenant_id,workspace_id,base_id,document_id,source_version,content,sha256,created_by) VALUES(?,?,?,?,?,1,?,?,?)`, []any{source, w.TenantID, w.ID, base, id, []byte(input.Content), input.SHA256, a.Me.User.ID}, []any{source, w.TenantID, w.ID, base, id, []byte(input.Content), input.SHA256, a.Me.User.ID})); e != nil {
			return e
		}
		attempt, e := s.insertAttempt(ctx, tx, a, w, base, id, source, b.activeID, "ingest", 1)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, knowledgeSQL(tx, "UPDATE knowledge_documents SET current_attempt_id=$2 WHERE id=$1", `UPDATE knowledge_documents SET current_attempt_id=?,pending_source_id=? WHERE id=?`, []any{id, attempt}, []any{attempt, source, id})); e != nil {
			return e
		}
		if e = saveIntent(ctx, tx, a, w, "upload_source", key, id, digest); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "knowledge.document_uploaded", "knowledge_document", id, input.Reason, request); e != nil {
			return e
		}
		d, e := lockedDocument(ctx, tx, w, base, id, false)
		if e != nil {
			return e
		}
		out, e = hydrateDocument(ctx, tx, w, d)
		return e
	})
	return knowledgeCommitted(out, e)
}
func (s *Service) Replace(ctx context.Context, a identity.Auth, workspace, base, id string, input ReplaceSource, key, request string) (Document, error) {
	var out Document
	if e := s.enabled(); e != nil {
		return knowledgeCommitted(out, e)
	}
	if !validReason(input.Reason) || input.ExpectedRevision < 1 {
		return out, failure("VALIDATION_FAILED")
	}
	if e := s.validSource(input.Content, input.SHA256); e != nil {
		return knowledgeCommitted(out, e)
	}
	e := s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		b, e := s.writableBase(ctx, tx, w, base)
		if e != nil {
			return e
		}
		d, e := lockedDocument(ctx, tx, w, base, id, false)
		if e != nil {
			return e
		}
		previous, digest, e := s.intent(ctx, tx, a, w, "replace_source", key, struct {
			Base, Document string
			Input          ReplaceSource
		}{base, id, input})
		if e != nil {
			return e
		}
		if previous != "" {
			if previous != id {
				return failure("IDEMPOTENCY_CONFLICT")
			}
			out, e = hydrateDocument(ctx, tx, w, d)
			return e
		}
		if d.Revision != input.ExpectedRevision {
			return failure("KNOWLEDGE_REVISION_CONFLICT")
		}
		if d.State == "pending" || d.State == "running" {
			return failure("KNOWLEDGE_BUSY")
		}
		if d.pendingID != nil && (d.visibleID == nil || *d.pendingID != *d.visibleID) {
			if _, e = tx.Exec(ctx, knowledgeSQL(tx, "UPDATE knowledge_source_versions SET superseded_at=clock_timestamp() WHERE id=$1 AND superseded_at IS NULL", `UPDATE knowledge_source_versions SET superseded_at=CURRENT_TIMESTAMP(6) WHERE id=? AND superseded_at IS NULL`, []any{*d.pendingID}, []any{*d.pendingID})); e != nil {
				return e
			}
		}
		var version int64
		if e = tx.QueryRow(ctx, knowledgeSQL(tx, "SELECT coalesce(max(source_version),0)+1 FROM knowledge_source_versions WHERE document_id=$1", `SELECT coalesce(max(source_version),0)+1 FROM knowledge_source_versions WHERE document_id=?`, []any{id}, []any{id})).Scan(&version); e != nil {
			return e
		}
		source := uuid.NewString()
		if _, e = tx.Exec(ctx, knowledgeSQL(tx, `INSERT INTO knowledge_source_versions(id,tenant_id,workspace_id,base_id,document_id,source_version,content,sha256,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, `INSERT INTO knowledge_source_versions(id,tenant_id,workspace_id,base_id,document_id,source_version,content,sha256,created_by) VALUES(?,?,?,?,?,?,?,?,?)`, []any{source, w.TenantID, w.ID, base, id, version, []byte(input.Content), input.SHA256, a.Me.User.ID}, []any{source, w.TenantID, w.ID, base, id, version, []byte(input.Content), input.SHA256, a.Me.User.ID})); e != nil {
			return e
		}
		attempt, e := s.insertAttempt(ctx, tx, a, w, base, id, source, b.activeID, "ingest", d.Revision+1)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_documents SET pending_source_id=$2,current_attempt_id=$3,state='pending',revision=revision+1,updated_at=clock_timestamp() WHERE id=$1`, `UPDATE knowledge_documents SET pending_source_id=?,current_attempt_id=?,state='pending',revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{id, source, attempt}, []any{source, attempt, id})); e != nil {
			return e
		}
		if e = saveIntent(ctx, tx, a, w, "replace_source", key, id, digest); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "knowledge.source_replaced", "knowledge_document", id, input.Reason, request); e != nil {
			return e
		}
		d, e = lockedDocument(ctx, tx, w, base, id, false)
		if e != nil {
			return e
		}
		out, e = hydrateDocument(ctx, tx, w, d)
		return e
	})
	return knowledgeCommitted(out, e)
}
func (s *Service) Retry(ctx context.Context, a identity.Auth, workspace, base, id string, input RevisionReason, key, request string) (Document, error) {
	var out Document
	if e := s.enabled(); e != nil {
		return knowledgeCommitted(out, e)
	}
	if !validReason(input.Reason) || input.ExpectedRevision < 1 {
		return out, failure("VALIDATION_FAILED")
	}
	e := s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		b, e := s.writableBase(ctx, tx, w, base)
		if e != nil {
			return e
		}
		d, e := lockedDocument(ctx, tx, w, base, id, false)
		if e != nil {
			return e
		}
		previous, digest, e := s.intent(ctx, tx, a, w, "retry_document", key, struct {
			Base, Document string
			Input          RevisionReason
		}{base, id, input})
		if e != nil {
			return e
		}
		if previous != "" {
			if previous != id {
				return failure("IDEMPOTENCY_CONFLICT")
			}
			out, e = hydrateDocument(ctx, tx, w, d)
			return e
		}
		if d.Revision != input.ExpectedRevision {
			return failure("KNOWLEDGE_REVISION_CONFLICT")
		}
		if d.State != "failed" || d.pendingID == nil || d.attemptID == nil {
			return failure("KNOWLEDGE_RETRY_NOT_ALLOWED")
		}
		attempt, e := s.insertAttempt(ctx, tx, a, w, base, id, *d.pendingID, b.activeID, "ingest", d.Revision+1)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_documents SET current_attempt_id=$2,state='pending',revision=revision+1,updated_at=clock_timestamp() WHERE id=$1`, `UPDATE knowledge_documents SET current_attempt_id=?,state='pending',revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{id, attempt}, []any{attempt, id})); e != nil {
			return e
		}
		if e = saveIntent(ctx, tx, a, w, "retry_document", key, id, digest); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "knowledge.document_retried", "knowledge_document", id, input.Reason, request); e != nil {
			return e
		}
		d, e = lockedDocument(ctx, tx, w, base, id, false)
		if e != nil {
			return e
		}
		out, e = hydrateDocument(ctx, tx, w, d)
		return e
	})
	return knowledgeCommitted(out, e)
}
func (s *Service) Delete(ctx context.Context, a identity.Auth, workspace, base, id string, input RevisionReason, request string) (Document, error) {
	var out Document
	if !validReason(input.Reason) || input.ExpectedRevision < 1 {
		return out, failure("VALIDATION_FAILED")
	}
	e := s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if _, e := lockedBase(ctx, tx, w, base); e != nil {
			return e
		}
		d, e := lockedDocument(ctx, tx, w, base, id, true)
		if e != nil {
			return e
		}
		if d.DeletedAt != nil {
			out, e = hydrateDocument(ctx, tx, w, d)
			return e
		}
		if d.Revision != input.ExpectedRevision {
			return failure("KNOWLEDGE_REVISION_CONFLICT")
		}
		if _, e = tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_attempts SET state='cancelled',lease_hash=NULL,lease_expires_at=NULL,error_code='KNOWLEDGE_CANCELLED',finished_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3 AND document_id=$4 AND state IN ('pending','running')`, `UPDATE knowledge_attempts SET state='cancelled',lease_hash=NULL,lease_expires_at=NULL,error_code='KNOWLEDGE_CANCELLED',finished_at=CURRENT_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND base_id=? AND document_id=? AND state IN ('pending','running')`, []any{w.TenantID, w.ID, base, id}, []any{w.TenantID, w.ID, base, id})); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, knowledgeSQL(tx, "UPDATE knowledge_source_versions SET superseded_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3 AND document_id=$4 AND superseded_at IS NULL", `UPDATE knowledge_source_versions SET superseded_at=CURRENT_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND base_id=? AND document_id=? AND superseded_at IS NULL`, []any{w.TenantID, w.ID, base, id}, []any{w.TenantID, w.ID, base, id})); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_documents SET state='deleted',visible_source_id=NULL,pending_source_id=NULL,revision=revision+1,deleted_at=clock_timestamp(),delete_reason=$2,updated_at=clock_timestamp() WHERE id=$1`, `UPDATE knowledge_documents SET state='deleted',visible_source_id=NULL,pending_source_id=NULL,revision=revision+1,deleted_at=CURRENT_TIMESTAMP(6),delete_reason=?,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{id, input.Reason}, []any{input.Reason, id})); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "knowledge.document_deleted", "knowledge_document", id, input.Reason, request); e != nil {
			return e
		}
		d, e = lockedDocument(ctx, tx, w, base, id, true)
		if e != nil {
			return e
		}
		out, e = hydrateDocument(ctx, tx, w, d)
		return e
	})
	return knowledgeCommitted(out, e)
}
func (s *Service) GetSource(ctx context.Context, a identity.Auth, workspace, base, id, source string) (Source, error) {
	var out Source
	if !validID(source) {
		return out, failure("KNOWLEDGE_SOURCE_NOT_FOUND")
	}
	e := s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		b, e := lockedBase(ctx, tx, w, base)
		if e != nil {
			return e
		}
		if !b.Enabled {
			return failure("KNOWLEDGE_BASE_DISABLED")
		}
		d, e := lockedDocument(ctx, tx, w, base, id, false)
		if e != nil {
			return e
		}
		if (d.visibleID == nil || *d.visibleID != source) && (d.pendingID == nil || *d.pendingID != source) {
			return failure("KNOWLEDGE_SOURCE_NOT_FOUND")
		}
		var content []byte
		e = tx.QueryRow(ctx, knowledgeSQL(tx, `SELECT id::text,source_version,content,sha256,size_bytes,created_at FROM knowledge_source_versions WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3 AND document_id=$4 AND id=$5`, `SELECT id,source_version,content,sha256,size_bytes,created_at FROM knowledge_source_versions WHERE tenant_id=? AND workspace_id=? AND base_id=? AND document_id=? AND id=?`, []any{w.TenantID, w.ID, base, id, source}, []any{w.TenantID, w.ID, base, id, source})).Scan(&out.SourceID, &out.SourceVersion, &content, &out.SHA256, &out.SizeBytes, &out.CreatedAt)
		if e != nil {
			return e
		}
		if !utf8.Valid(content) || len(content) != out.SizeBytes || hashBytes(content) != out.SHA256 {
			return failure("KNOWLEDGE_INVALID_SOURCE")
		}
		out.CreatedAt = out.CreatedAt.UTC()
		out.DocumentID = id
		out.Name = d.Name
		out.Content = string(content)
		return nil
	})
	return knowledgeCommitted(out, e)
}
