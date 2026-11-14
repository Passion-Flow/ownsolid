package knowledge

import (
	"container/heap"
	"context"
	"math"
	"sort"
	"strconv"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Structural transport batch, not an adjustable candidate/quality cap. A page
// has at most128 embeddings of32768 bytes plus bounded metadata (<4.4MiB).
// All pages run under the caller's current user/Workspace/base locks and deadline.
const knowledgeDamengVectorPage = 128
const knowledgeMaximumStoredChunks = 4096
const knowledgeMaximumDocumentNameBytes = 255 * utf8.UTFMax
const dmKnowledgeVectorCandidates = `SELECT d.id,v.id,v.source_version,d.name,v.sha256,c.start_byte,c.end_byte,c.ordinal,c.embedding FROM knowledge_chunks c JOIN knowledge_attempts a ON a.id=c.attempt_id AND a.tenant_id=c.tenant_id AND a.workspace_id=c.workspace_id AND a.base_id=c.base_id AND a.document_id=c.document_id AND a.generation_id=c.generation_id AND a.source_id=c.source_id JOIN knowledge_documents d ON d.id=c.document_id AND d.tenant_id=c.tenant_id AND d.workspace_id=c.workspace_id AND d.base_id=c.base_id JOIN knowledge_source_versions v ON v.id=c.source_id AND v.tenant_id=c.tenant_id AND v.workspace_id=c.workspace_id AND v.base_id=c.base_id AND v.document_id=c.document_id JOIN knowledge_bases b ON b.tenant_id=c.tenant_id AND b.workspace_id=c.workspace_id AND b.id=c.base_id WHERE c.tenant_id=? AND c.workspace_id=? AND c.base_id=? AND c.generation_id=? AND b.initialization_state='ready' AND b.active_generation_id=c.generation_id AND b.enabled=1 AND a.state='ready' AND d.deleted_at IS NULL AND d.visible_source_id=c.source_id`
const dmKnowledgeMatchedSource = `SELECT v.content FROM knowledge_documents d JOIN knowledge_source_versions v ON v.id=d.visible_source_id AND v.document_id=d.id AND v.tenant_id=d.tenant_id AND v.workspace_id=d.workspace_id AND v.base_id=d.base_id WHERE d.tenant_id=? AND d.workspace_id=? AND d.base_id=? AND d.id=? AND d.deleted_at IS NULL AND v.id=? AND v.sha256=? AND v.source_version=?`

type knowledgeVectorCursor struct {
	document string
	ordinal  int
}

func knowledgeVectorPageStatement(w workspaces.Workspace, base, generation string, after knowledgeVectorCursor) transaction.Statement {
	sql := dmKnowledgeVectorCandidates
	args := []any{w.TenantID, w.ID, base, generation}
	if after.document != "" {
		sql += " AND (d.id>? OR (d.id=? AND c.ordinal>?))"
		args = append(args, after.document, after.document, after.ordinal)
	}
	sql += " ORDER BY d.id,c.ordinal LIMIT " + strconv.Itoa(knowledgeDamengVectorPage)
	return transaction.SQL(transaction.Dameng, sql, args...)
}

func (s *Service) damengScorePage(ctx context.Context, q transaction.Executor, w workspaces.Workspace, base, generation string, after knowledgeVectorCursor, query []float64, norm float64, input SearchRequest, best *matchHeap) (next knowledgeVectorCursor, count int, result error) {
	rows, e := q.Query(ctx, knowledgeVectorPageStatement(w, base, generation, after))
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
		var match Match
		var raw []byte
		if e := rows.Scan(&match.DocumentID, &match.SourceID, &match.SourceVersion, &match.Name, &match.SourceSHA256, &match.StartByte, &match.EndByte, &match.ChunkOrdinal, &raw); e != nil {
			return next, count, e
		}
		if !validID(match.DocumentID) || !validID(match.SourceID) || match.SourceVersion < 1 || match.ChunkOrdinal < 0 || match.ChunkOrdinal >= knowledgeMaximumStoredChunks || match.StartByte < 0 || match.EndByte <= match.StartByte || match.EndByte > config.MaximumKnowledgeSourceBytes || match.EndByte-match.StartByte > config.MaximumKnowledgeChunkBytes || len(match.Name) > knowledgeMaximumDocumentNameBytes || !utf8.ValidString(match.Name) || (next.document != "" && (match.DocumentID < next.document || match.DocumentID == next.document && match.ChunkOrdinal <= next.ordinal)) {
			return next, count, failure("KNOWLEDGE_INVALID_SOURCE")
		}
		count++
		if count > knowledgeDamengVectorPage {
			return next, count, failure("KNOWLEDGE_WORKER_RESULT_INVALID")
		}
		next = knowledgeVectorCursor{match.DocumentID, match.ChunkOrdinal}
		vector, otherNorm, e := decodeVector(raw, s.Processor.contract.VectorNormTolerance)
		if e != nil {
			return next, count, e
		}
		var dot float64
		for i, value := range query {
			dot += value * vector[i]
		}
		match.Score = math.Max(-1, math.Min(1, dot/(norm*otherNorm)))
		if match.Score < input.MinimumScore {
			continue
		}
		if len(*best) < input.TopK {
			heap.Push(best, match)
		} else if matchBetter(match, (*best)[0]) {
			(*best)[0] = match
			heap.Fix(best, 0)
		}
	}
	return next, count, rows.Err()
}

// Exact full-set cosine scoring with bounded native materialization and TopK
// memory. A deadline/failure returns no partial result or fallback. Original TXT
// bytes are fetched only for winners; no DB substring/encoding truncation occurs.
func (s *Service) damengSearch(ctx context.Context, q transaction.Executor, w workspaces.Workspace, base, generation string, query []float64, input SearchRequest) ([]Match, error) {
	if q.Dialect() != transaction.Dameng || input.TopK < 1 || input.TopK > s.policy().MaximumTopK || math.IsNaN(input.MinimumScore) || math.IsInf(input.MinimumScore, 0) || input.MinimumScore < -1 || input.MinimumScore > 1 {
		return nil, failure("VALIDATION_FAILED")
	}
	norm, e := vectorNorm(query, s.Processor.contract.VectorNormTolerance)
	if e != nil {
		return nil, e
	}
	best := matchHeap{}
	after := knowledgeVectorCursor{}
	for {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		next, count, e := s.damengScorePage(ctx, q, w, base, generation, after, query, norm, input, &best)
		if e != nil {
			return nil, e
		}
		after = next
		if count < knowledgeDamengVectorPage {
			break
		}
	}
	sort.Slice(best, func(i, j int) bool { return matchBetter(best[i], best[j]) })
	for i := range best {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		m := &best[i]
		var source []byte
		if e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, dmKnowledgeMatchedSource, w.TenantID, w.ID, base, m.DocumentID, m.SourceID, m.SourceSHA256, m.SourceVersion)).Scan(&source); e != nil {
			return nil, e
		}
		if len(source) > config.MaximumKnowledgeSourceBytes || len(source) < m.EndByte || !utf8.Valid(source) || hashBytes(source) != m.SourceSHA256 {
			return nil, failure("KNOWLEDGE_INVALID_SOURCE")
		}
		if e := excerpt(m, source[m.StartByte:m.EndByte], s.policy().ContextBytes); e != nil {
			return nil, e
		}
		m.CitationID = "S" + strconv.Itoa(i+1)
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	return []Match(best), nil
}
