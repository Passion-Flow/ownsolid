package knowledge

import (
	"context"
	"encoding/json"
	"strconv"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const dmKnowledgeStorageShape = `SELECT CASE WHEN EXISTS(SELECT 1 FROM ALL_TAB_COLUMNS WHERE OWNER=SYS_CONTEXT('USERENV','CURRENT_SCHEMA') AND TABLE_NAME='KNOWLEDGE_CHUNKS' AND COLUMN_NAME='EMBEDDING' AND DATA_TYPE='CLOB') AND EXISTS(SELECT 1 FROM ALL_TAB_COLUMNS WHERE OWNER=SYS_CONTEXT('USERENV','CURRENT_SCHEMA') AND TABLE_NAME='KNOWLEDGE_SOURCE_VERSIONS' AND COLUMN_NAME='CONTENT' AND DATA_TYPE='BLOB') AND EXISTS(SELECT 1 FROM ALL_TAB_COLUMNS WHERE OWNER=SYS_CONTEXT('USERENV','CURRENT_SCHEMA') AND TABLE_NAME='KNOWLEDGE_BASES' AND COLUMN_NAME='INITIALIZATION_STATE') AND NOT EXISTS(SELECT 1 FROM knowledge_bases b JOIN installation_state i ON i.tenant_id=b.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND (b.initialization_state<>'ready' OR b.active_generation_id IS NULL)) THEN 1 ELSE 0 END FROM DUAL`
const dmKnowledgeStoredSources = `SELECT v.id,v.content,v.sha256,v.size_bytes FROM knowledge_source_versions v JOIN installation_state i ON i.tenant_id=v.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1`
const dmKnowledgeStoredVectors = `SELECT c.attempt_id,c.ordinal,c.embedding FROM knowledge_chunks c JOIN installation_state i ON i.tenant_id=c.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1`

// Startup integrity reads historical/retired bytes too. It never imports data,
// repairs schema, or claims native migration readiness from the shallow shape
// check; the migration owner must still verify the exact contribution metadata.
func (s *Service) verifyDamengDatabase(ctx context.Context, q transaction.Executor) error {
	var valid bool
	if e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, dmKnowledgeStorageShape)).Scan(&valid); e != nil {
		return e
	}
	if !valid {
		return failure("KNOWLEDGE_CONFIGURATION_INVALID")
	}
	after := ""
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		sql := dmKnowledgeStoredSources
		args := []any{}
		if after != "" {
			sql += " AND v.id>?"
			args = append(args, after)
		}
		sql += " ORDER BY v.id LIMIT 1"
		next, count, e := knowledgeStoredSourcePage(ctx, q, transaction.SQL(transaction.Dameng, sql, args...), after)
		if e != nil {
			return e
		}
		if count == 0 {
			break
		}
		after = next
	}
	var contract workerContract
	if json.Unmarshal([]byte(WorkerContractJSON), &contract) != nil || contract.Dimension != EmbeddingDimension {
		return failure("KNOWLEDGE_CONFIGURATION_INVALID")
	}
	cursor := knowledgeVectorCursor{}
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		sql := dmKnowledgeStoredVectors
		args := []any{}
		if cursor.document != "" {
			sql += " AND (c.attempt_id>? OR (c.attempt_id=? AND c.ordinal>?))"
			args = append(args, cursor.document, cursor.document, cursor.ordinal)
		}
		sql += " ORDER BY c.attempt_id,c.ordinal LIMIT " + strconv.Itoa(knowledgeDamengVectorPage)
		next, count, e := knowledgeStoredVectorPage(ctx, q, transaction.SQL(transaction.Dameng, sql, args...), cursor, contract.VectorNormTolerance)
		if e != nil {
			return e
		}
		if count < knowledgeDamengVectorPage {
			return nil
		}
		cursor = next
	}
}
func knowledgeStoredSourcePage(ctx context.Context, q transaction.Executor, statement transaction.Statement, after string) (next string, count int, result error) {
	rows, e := q.Query(ctx, statement)
	if e != nil {
		return after, 0, e
	}
	defer func() {
		rows.Close()
		if e := rows.Err(); result == nil && e != nil {
			result = e
		}
	}()
	next = after
	for rows.Next() {
		if e := ctx.Err(); e != nil {
			return next, count, e
		}
		var id, digest string
		var content []byte
		var size int
		if e := rows.Scan(&id, &content, &digest, &size); e != nil {
			return next, count, e
		}
		if count != 0 || !validID(id) || id <= next || size < 1 || size > config.MaximumKnowledgeSourceBytes || size != len(content) || !utf8.Valid(content) || hashBytes(content) != digest {
			return next, count, failure("KNOWLEDGE_INVALID_SOURCE")
		}
		next = id
		count++
	}
	return next, count, rows.Err()
}
func knowledgeStoredVectorPage(ctx context.Context, q transaction.Executor, statement transaction.Statement, after knowledgeVectorCursor, tolerance float64) (next knowledgeVectorCursor, count int, result error) {
	rows, e := q.Query(ctx, statement)
	if e != nil {
		return after, 0, e
	}
	defer func() {
		rows.Close()
		if e := rows.Err(); result == nil && e != nil {
			result = e
		}
	}()
	next = after
	for rows.Next() {
		if e := ctx.Err(); e != nil {
			return next, count, e
		}
		var id string
		var ordinal int
		var raw []byte
		if e := rows.Scan(&id, &ordinal, &raw); e != nil {
			return next, count, e
		}
		if count >= knowledgeDamengVectorPage || !validID(id) || ordinal < 0 || ordinal >= knowledgeMaximumStoredChunks || id < next.document || id == next.document && ordinal <= next.ordinal {
			return next, count, failure("KNOWLEDGE_WORKER_RESULT_INVALID")
		}
		if _, _, e := decodeVector(raw, tolerance); e != nil {
			return next, count, e
		}
		next = knowledgeVectorCursor{id, ordinal}
		count++
	}
	return next, count, rows.Err()
}
