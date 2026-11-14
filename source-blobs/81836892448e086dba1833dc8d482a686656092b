package models

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// The stopped installation's owning restore transaction retains commit authority.
// Immutable model files/history remain retained; no restored import is adopted.
func InvalidateAuthoritiesWithin(ctx context.Context, q transaction.Executor) error {
	if ctx == nil || q == nil || q.Dialect() != transaction.PostgreSQL && q.Dialect() != transaction.MySQL {
		return fail("STORAGE_UNAVAILABLE")
	}
	_, e := q.Exec(ctx, modelNativeSQL(q, `UPDATE MODEL_IMPORTS SET STATE='failed',ERROR_CODE='MODEL_ACTOR_UNAVAILABLE',UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE STATE IN ('queued','verifying')`, `UPDATE model_imports SET state='failed',error_code='MODEL_ACTOR_UNAVAILABLE',updated_at=clock_timestamp() WHERE state IN ('queued','verifying')`, `UPDATE model_imports SET state='failed',error_code='MODEL_ACTOR_UNAVAILABLE',updated_at=UTC_TIMESTAMP(6) WHERE state IN ('queued','verifying')`))
	return e
}
