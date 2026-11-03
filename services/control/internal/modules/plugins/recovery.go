package plugins

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// InvalidateAuthorities belongs to the common offline restore transaction.
func InvalidateAuthorities(ctx context.Context, tx pgx.Tx) error {
	return InvalidateAuthoritiesWithin(ctx, transaction.PostgreSQLTransaction(tx))
}

// InvalidateAuthoritiesWithin reduces restored grants only and never commits.
func InvalidateAuthoritiesWithin(ctx context.Context, q transaction.Executor) error {
	if ctx == nil || q == nil {
		return failure("STORAGE_UNAVAILABLE")
	}
	if q.Dialect() == transaction.Dameng {
		return pluginError(invalidateNativeGrants(ctx, q, "", ""))
	}
	_, e := q.Exec(ctx, pluginSQL(q, "UPDATE plugin_grants SET enabled=false,authority_id=gen_random_uuid(),revision=revision+1,updated_at=clock_timestamp()", "UPDATE plugin_grants SET enabled=false,authority_id=LOWER(UUID()),revision=revision+1,updated_at=UTC_TIMESTAMP(6)", pluginNativeQuery{}))
	return pluginError(e)
}
