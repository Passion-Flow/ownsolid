package knowledge

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// A structural parameter/packet batch, below both drivers' parameter ceilings;
// adjustable document/chunk/resource limits remain the owning typed policy.
const knowledgeChunkInsertBatch = 64

func insertKnowledgeChunks(ctx context.Context, q transaction.Executor, w workspaces.Workspace, c *claimed, chunks []Chunk) error {
	for offset := 0; offset < len(chunks); offset += knowledgeChunkInsertBatch {
		end := min(offset+knowledgeChunkInsertBatch, len(chunks))
		args := []any{}
		pgRows, myRows := []string{}, []string{}
		for _, chunk := range chunks[offset:end] {
			vector, e := json.Marshal(chunk.Embedding)
			if e != nil {
				return failure("KNOWLEDGE_WORKER_RESULT_INVALID")
			}
			values := []any{w.TenantID, w.ID, c.base, c.document, c.generation, c.source, c.id, chunk.Ordinal, chunk.StartByte, chunk.EndByte, chunk.TokenCount, string(vector)}
			pg, my := []string{}, []string{}
			for i, value := range values {
				args = append(args, value)
				mark := "$" + strconv.Itoa(len(args))
				other := "?"
				if i == len(values)-1 {
					mark += "::public.vector(384)"
					other = "CAST(? AS JSON)"
				}
				pg = append(pg, mark)
				my = append(my, other)
			}
			pgRows = append(pgRows, "("+strings.Join(pg, ",")+")")
			myRows = append(myRows, "("+strings.Join(my, ",")+")")
		}
		prefix := "INSERT INTO knowledge_chunks(tenant_id,workspace_id,base_id,document_id,generation_id,source_id,attempt_id,ordinal,start_byte,end_byte,token_count,embedding) VALUES"
		if _, e := q.Exec(ctx, knowledgeSQL(q, prefix+strings.Join(pgRows, ","), prefix+strings.Join(myRows, ","), args, args)); e != nil {
			return e
		}
	}
	return nil
}
