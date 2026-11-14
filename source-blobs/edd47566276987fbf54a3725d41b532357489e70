package knowledge

import (
	"context"
	"encoding/json"
	"math"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func (s *Service) Search(ctx context.Context, a identity.Auth, workspace, base string, input SearchRequest, request string) (SearchResult, error) {
	out := SearchResult{BaseID: base, Matches: []Match{}}
	if e := s.enabled(); e != nil {
		return knowledgeCommitted(out, e)
	}
	p := s.policy()
	if !utf8.ValidString(input.Query) || strings.TrimSpace(input.Query) == "" || len(input.Query) > p.QueryBytes || input.TopK < 1 || input.TopK > p.MaximumTopK || math.IsNaN(input.MinimumScore) || math.IsInf(input.MinimumScore, 0) || input.MinimumScore < -1 || input.MinimumScore > 1 {
		return SearchResult{}, failure("VALIDATION_FAILED")
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(p.QuerySeconds))
	defer cancel()
	var generation Generation
	e := s.queryTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		b, e := lockedBase(ctx, tx, w, base)
		if e != nil {
			return e
		}
		if !b.Enabled {
			return failure("KNOWLEDGE_BASE_DISABLED")
		}
		generation, e = loadGeneration(ctx, tx, w, base, b.activeID)
		if e != nil {
			return e
		}
		if generation.CatalogSHA256 != CatalogDigest() || generation.Dimension != EmbeddingDimension || generation.State != "active" {
			return failure("KNOWLEDGE_INDEX_INCOMPATIBLE")
		}
		_, e = s.Identity.AdmitWithin(ctx, tx, identity.Limit{Purpose: "knowledge.query", Identifier: a.Me.User.ID + ":" + w.ID, Count: p.QueryCount, Window: config.Seconds(p.QueryWindowSeconds)})
		return e
	})
	if e != nil {
		return knowledgeCommitted(out, e)
	}
	encoded, e := s.Processor.Execute(ctx, input.Query, false, generation.ChunkTokens, generation.OverlapTokens)
	if e != nil {
		return knowledgeCommitted(out, e)
	}
	vector, e := json.Marshal(encoded.Chunks[0].Embedding)
	if e != nil {
		return SearchResult{}, failure("KNOWLEDGE_WORKER_RESULT_INVALID")
	}
	e = s.queryTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		b, e := lockedBase(ctx, tx, w, base)
		if e != nil {
			return e
		}
		if !b.Enabled {
			return failure("KNOWLEDGE_BASE_DISABLED")
		}
		if b.activeID != generation.ID {
			return failure("KNOWLEDGE_ANSWER_SOURCE_CHANGED")
		}
		if tx.Dialect() == transaction.MySQL {
			out.Matches, e = s.mysqlSearch(ctx, tx, w, base, generation.ID, encoded.Chunks[0].Embedding, input)
			if e != nil {
				return e
			}
			out.GenerationID = generation.ID
			out.EncoderCatalogSHA256 = generation.CatalogSHA256
			return s.audit(ctx, tx, a, w, "knowledge.searched", "knowledge_base", base, "Scoped exact embedding retrieval", request)
		}
		rows, e := tx.Query(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT d.id::text,v.id::text,v.source_version,d.name,v.sha256,c.start_byte,c.end_byte,c.ordinal,substring(v.content FROM c.start_byte+1 FOR c.end_byte-c.start_byte),1-(c.embedding OPERATOR(public.<=>) $5::public.vector) FROM knowledge_chunks c JOIN knowledge_attempts a ON a.id=c.attempt_id AND a.tenant_id=c.tenant_id AND a.workspace_id=c.workspace_id AND a.base_id=c.base_id AND a.document_id=c.document_id AND a.generation_id=c.generation_id AND a.source_id=c.source_id JOIN knowledge_documents d ON d.id=c.document_id AND d.tenant_id=c.tenant_id AND d.workspace_id=c.workspace_id AND d.base_id=c.base_id JOIN knowledge_source_versions v ON v.id=c.source_id AND v.tenant_id=c.tenant_id AND v.workspace_id=c.workspace_id AND v.base_id=c.base_id AND v.document_id=c.document_id WHERE c.tenant_id=$1 AND c.workspace_id=$2 AND c.base_id=$3 AND c.generation_id=$4 AND a.state='ready' AND d.deleted_at IS NULL AND d.visible_source_id=c.source_id AND 1-(c.embedding OPERATOR(public.<=>) $5::public.vector)>=$6 ORDER BY c.embedding OPERATOR(public.<=>) $5::public.vector,d.id,c.ordinal LIMIT $7`, w.TenantID, w.ID, base, generation.ID, string(vector), input.MinimumScore, input.TopK))
		if e != nil {
			return e
		}
		for rows.Next() {
			var match Match
			var text []byte
			if e = rows.Scan(&match.DocumentID, &match.SourceID, &match.SourceVersion, &match.Name, &match.SourceSHA256, &match.StartByte, &match.EndByte, &match.ChunkOrdinal, &text, &match.Score); e != nil {
				rows.Close()
				return e
			}
			if len(text) != match.EndByte-match.StartByte || !utf8.Valid(text) || math.IsNaN(match.Score) || math.IsInf(match.Score, 0) {
				rows.Close()
				return failure("KNOWLEDGE_INVALID_SOURCE")
			}
			match.Score = math.Max(-1, math.Min(1, match.Score))
			if len(text) > p.ContextBytes {
				end := p.ContextBytes
				for end > 0 && !utf8.Valid(text[:end]) {
					end--
				}
				text = text[:end]
				match.ExcerptTruncated = true
			}
			match.Text = string(text)
			match.CitationID = "S" + strconv.Itoa(len(out.Matches)+1)
			out.Matches = append(out.Matches, match)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		out.GenerationID = generation.ID
		out.EncoderCatalogSHA256 = generation.CatalogSHA256
		return s.audit(ctx, tx, a, w, "knowledge.searched", "knowledge_base", base, "Scoped exact embedding retrieval", request)
	})
	if e != nil {
		return SearchResult{}, knowledgeError(e)
	}
	return out, nil
}
