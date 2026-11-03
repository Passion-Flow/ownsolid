package knowledge

import (
	"context"
	"strconv"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
)

const dmKnowledgeInitializeBase = `UPDATE knowledge_bases SET active_generation_id=?,initialization_state='ready' WHERE tenant_id=? AND workspace_id=? AND id=? AND initialization_state='initializing'`
const dmKnowledgeInsertChunk = `INSERT INTO knowledge_chunks(tenant_id,workspace_id,base_id,document_id,generation_id,source_id,attempt_id,ordinal,start_byte,end_byte,token_count,embedding) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`
const dmKnowledgeBasePage = `SELECT id,workspace_id,name,enabled,revision,active_generation_id,building_generation_id,created_by,created_at,updated_at FROM knowledge_bases WHERE initialization_state='ready' AND tenant_id=? AND workspace_id=?`
const dmKnowledgeDocumentPage = `SELECT id,base_id,workspace_id,name,revision,state,visible_source_id,pending_source_id,current_attempt_id,created_by,created_at,updated_at,deleted_at FROM knowledge_documents WHERE tenant_id=? AND workspace_id=? AND base_id=? AND deleted_at IS NULL`

func knowledgeDamengPage(w workspaces.Workspace, base string, cursor pagination.Cursor, limit int) (transaction.Statement, error) {
	if limit < 1 || limit > pagination.MaximumLimit {
		return transaction.Statement{}, failure("INVALID_QUERY")
	}
	sql := dmKnowledgeBasePage
	args := []any{w.TenantID, w.ID}
	if base != "" {
		sql = dmKnowledgeDocumentPage
		args = append(args, base)
	}
	if cursor.ID != "" {
		if !validID(cursor.ID) || cursor.Time.Location() != time.UTC || cursor.Time.Year() < 1970 || cursor.Time.Year() > 9999 || cursor.Time.Nanosecond()%1000 != 0 {
			return transaction.Statement{}, failure("INVALID_CURSOR")
		}
		sql += " AND (created_at<? OR (created_at=? AND id<?))"
		args = append(args, cursor.Time, cursor.Time, cursor.ID)
	}
	sql += " ORDER BY created_at DESC,id DESC LIMIT " + strconv.Itoa(limit+1)
	return transaction.SQL(transaction.Dameng, sql, args...), nil
}

// Keep existing PostgreSQL/MySQL Exec behavior; native SELECT results must be
// consumed and closed before the next statement on its exclusive connection.
func knowledgeLockRows(ctx context.Context, q transaction.Executor, statement transaction.Statement) (count int64, result error) {
	if q.Dialect() != transaction.Dameng {
		return q.Exec(ctx, statement)
	}
	rows, e := q.Query(ctx, statement)
	if e != nil {
		return 0, e
	}
	defer func() {
		rows.Close()
		if e := rows.Err(); result == nil && e != nil {
			result = e
		}
	}()
	for rows.Next() {
		if e := ctx.Err(); e != nil {
			return 0, e
		}
		var id string
		if e := rows.Scan(&id); e != nil {
			return 0, e
		}
		count++
	}
	return count, rows.Err()
}
