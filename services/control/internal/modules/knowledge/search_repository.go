package knowledge

import (
	"bytes"
	"container/heap"
	"context"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// The root is the worst retained result. Equal scores use the original scoped
// document/ordinal order; SQL arrival order can never change a tie decision.
type matchHeap []Match

func (h matchHeap) Len() int           { return len(h) }
func (h matchHeap) Less(i, j int) bool { return matchBetter(h[j], h[i]) }
func (h matchHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *matchHeap) Push(value any)    { *h = append(*h, value.(Match)) }
func (h *matchHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	old[len(old)-1] = Match{}
	*h = old[:len(old)-1]
	return last
}
func matchBetter(a, b Match) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.DocumentID != b.DocumentID {
		return a.DocumentID < b.DocumentID
	}
	return a.ChunkOrdinal < b.ChunkOrdinal
}
func vectorNorm(v []float64, tolerance float64) (float64, error) {
	if len(v) != EmbeddingDimension || tolerance <= 0 || math.IsNaN(tolerance) || math.IsInf(tolerance, 0) {
		return 0, failure("KNOWLEDGE_WORKER_RESULT_INVALID")
	}
	var squared float64
	for _, component := range v {
		if math.IsNaN(component) || math.IsInf(component, 0) {
			return 0, failure("KNOWLEDGE_WORKER_RESULT_INVALID")
		}
		squared += component * component
	}
	norm := math.Sqrt(squared)
	if norm == 0 || math.IsNaN(norm) || math.IsInf(norm, 0) || math.Abs(norm-1) > tolerance {
		return 0, failure("KNOWLEDGE_WORKER_RESULT_INVALID")
	}
	return norm, nil
}

// The frozen MySQL schema's JSON storage ceiling bounds decoder allocations.
const maximumStoredEmbeddingBytes = 32768

func decodeVector(raw []byte, tolerance float64) ([]float64, float64, error) {
	var encoded []json.RawMessage
	if len(raw) > maximumStoredEmbeddingBytes || json.Unmarshal(raw, &encoded) != nil || len(encoded) != EmbeddingDimension {
		return nil, 0, failure("KNOWLEDGE_WORKER_RESULT_INVALID")
	}
	v := make([]float64, EmbeddingDimension)
	for i, value := range encoded {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &v[i]) != nil {
			return nil, 0, failure("KNOWLEDGE_WORKER_RESULT_INVALID")
		}
	}
	norm, e := vectorNorm(v, tolerance)
	if e != nil {
		return nil, 0, e
	}
	return v, norm, nil
}

func excerpt(match *Match, text []byte, maximum int) error {
	if match.StartByte < 0 || match.EndByte <= match.StartByte || len(text) != match.EndByte-match.StartByte || !utf8.Valid(text) {
		return failure("KNOWLEDGE_INVALID_SOURCE")
	}
	if len(text) > maximum {
		end := maximum
		for end > 0 && !utf8.Valid(text[:end]) {
			end--
		}
		text = text[:end]
		match.ExcerptTruncated = true
	}
	match.Text = string(text)
	return nil
}

// mysqlSearch streams the full authorized current set. It retains at most TopK
// excerpts and one decoded384-vector; there is no hidden scan cap or ANN claim.
func (s *Service) mysqlSearch(ctx context.Context, q transaction.Executor, w workspaces.Workspace, base, generation string, query []float64, input SearchRequest) ([]Match, error) {
	if input.TopK < 1 || input.TopK > s.policy().MaximumTopK {
		return nil, failure("VALIDATION_FAILED")
	}
	norm, e := vectorNorm(query, s.Processor.contract.VectorNormTolerance)
	if e != nil {
		return nil, e
	}
	rows, e := q.Query(ctx, transaction.SQL(transaction.MySQL, `SELECT d.id,v.id,v.source_version,d.name,v.sha256,c.start_byte,c.end_byte,c.ordinal,SUBSTRING(v.content,c.start_byte+1,c.end_byte-c.start_byte),c.embedding FROM knowledge_chunks c JOIN knowledge_attempts a ON a.id=c.attempt_id AND a.tenant_id=c.tenant_id AND a.workspace_id=c.workspace_id AND a.base_id=c.base_id AND a.document_id=c.document_id AND a.generation_id=c.generation_id AND a.source_id=c.source_id JOIN knowledge_documents d ON d.id=c.document_id AND d.tenant_id=c.tenant_id AND d.workspace_id=c.workspace_id AND d.base_id=c.base_id JOIN knowledge_source_versions v ON v.id=c.source_id AND v.tenant_id=c.tenant_id AND v.workspace_id=c.workspace_id AND v.base_id=c.base_id AND v.document_id=c.document_id JOIN knowledge_bases b ON b.tenant_id=c.tenant_id AND b.workspace_id=c.workspace_id AND b.id=c.base_id WHERE c.tenant_id=? AND c.workspace_id=? AND c.base_id=? AND c.generation_id=? AND b.initialization_state='ready' AND b.active_generation_id=c.generation_id AND b.enabled AND a.state='ready' AND d.deleted_at IS NULL AND d.visible_source_id=c.source_id`, w.TenantID, w.ID, base, generation))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	best := matchHeap{}
	for rows.Next() {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		var match Match
		var text, raw []byte
		if e = rows.Scan(&match.DocumentID, &match.SourceID, &match.SourceVersion, &match.Name, &match.SourceSHA256, &match.StartByte, &match.EndByte, &match.ChunkOrdinal, &text, &raw); e != nil {
			return nil, e
		}
		vector, otherNorm, e := decodeVector(raw, s.Processor.contract.VectorNormTolerance)
		if e != nil {
			return nil, e
		}
		var dot float64
		for i, value := range query {
			dot += value * vector[i]
		}
		match.Score = math.Max(-1, math.Min(1, dot/(norm*otherNorm)))
		if e = excerpt(&match, text, s.policy().ContextBytes); e != nil {
			return nil, e
		}
		if match.Score < input.MinimumScore {
			continue
		}
		if len(best) < input.TopK {
			heap.Push(&best, match)
		} else if matchBetter(match, best[0]) {
			best[0] = match
			heap.Fix(&best, 0)
		}
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	sort.Slice(best, func(i, j int) bool { return matchBetter(best[i], best[j]) })
	out := []Match(best)
	for i := range out {
		out[i].CitationID = "S" + strconv.Itoa(i+1)
	}
	return out, nil
}
