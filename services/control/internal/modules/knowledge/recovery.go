package knowledge

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// InvalidateAuthorities fences restored live work in the common offline operator
// transaction. Ready immutable data remains usable after fresh authentication.
func InvalidateAuthorities(ctx context.Context, tx pgx.Tx) error {
	return InvalidateAuthoritiesWithin(ctx, transaction.PostgreSQLTransaction(tx))
}

func InvalidateAuthoritiesWithin(ctx context.Context, tx transaction.Executor) error {
	if ctx == nil || tx == nil || tx.Dialect() != transaction.PostgreSQL && tx.Dialect() != transaction.MySQL {
		return failure("KNOWLEDGE_STORAGE_MISMATCH")
	}
	if _, e := tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_attempts SET state='failed',lease_hash=NULL,lease_expires_at=NULL,error_code='KNOWLEDGE_WORKER_INTERRUPTED',finished_at=clock_timestamp() WHERE state IN ('pending','running')`, `UPDATE knowledge_attempts SET state='failed',lease_hash=NULL,lease_expires_at=NULL,error_code='KNOWLEDGE_WORKER_INTERRUPTED',finished_at=CURRENT_TIMESTAMP(6) WHERE state IN ('pending','running')`, nil, nil)); e != nil {
		return e
	}
	if _, e := tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_documents SET state='failed',revision=revision+1,updated_at=clock_timestamp() WHERE deleted_at IS NULL AND pending_source_id IS NOT NULL AND state IN ('pending','running')`, `UPDATE knowledge_documents SET state='failed',revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE deleted_at IS NULL AND pending_source_id IS NOT NULL AND state IN ('pending','running')`, nil, nil)); e != nil {
		return e
	}
	if _, e := tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_generations SET state='failed',error_code='KNOWLEDGE_WORKER_INTERRUPTED',finished_at=clock_timestamp() WHERE state='building'`, `UPDATE knowledge_generations SET state='failed',error_code='KNOWLEDGE_WORKER_INTERRUPTED',finished_at=CURRENT_TIMESTAMP(6) WHERE state='building'`, nil, nil)); e != nil {
		return e
	}
	_, e := tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_bases SET building_generation_id=NULL,revision=revision+1,updated_at=clock_timestamp() WHERE building_generation_id IS NOT NULL`, `UPDATE knowledge_bases SET building_generation_id=NULL,revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE building_generation_id IS NOT NULL`, nil, nil))
	return e
}
