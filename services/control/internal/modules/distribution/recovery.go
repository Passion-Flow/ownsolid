package distribution

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func InvalidateAuthorities(ctx context.Context, tx pgx.Tx) error {
	return InvalidateAuthoritiesWithin(ctx, transaction.PostgreSQLTransaction(tx))
}

// InvalidateAuthoritiesWithin preserves unresolved capacity and immutable history.
func InvalidateAuthoritiesWithin(ctx context.Context, q transaction.Executor) error {
	if ctx == nil || q == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	if _, e := q.Exec(ctx, distributionNativeSQL(q, `UPDATE GATEWAY_INSTANCES SET CREDENTIAL_HASH=NULL,AUTHORITY_INVALIDATED_AT=COALESCE(AUTHORITY_INVALIDATED_AT,CURRENT_TIMESTAMP(6)),REVOKED_AT=COALESCE(REVOKED_AT,CURRENT_TIMESTAMP(6)),REVOKE_REASON=COALESCE(REVOKE_REASON,'restored_state_invalidated')`, "UPDATE gateway_instances SET credential_hash=NULL,authority_invalidated_at=COALESCE(authority_invalidated_at,clock_timestamp()),revoked_at=COALESCE(revoked_at,clock_timestamp()),revoke_reason=COALESCE(revoke_reason,'restored_state_invalidated')", "UPDATE gateway_instances SET credential_hash=NULL,authority_invalidated_at=COALESCE(authority_invalidated_at,UTC_TIMESTAMP(6)),revoked_at=COALESCE(revoked_at,UTC_TIMESTAMP(6)),revoke_reason=COALESCE(revoke_reason,'restored_state_invalidated')")); e != nil {
		return e
	}
	if _, e := q.Exec(ctx, distributionNativeSQL(q, `UPDATE GATEWAY_RECOVERIES SET INVALIDATED_AT=COALESCE(INVALIDATED_AT,CURRENT_TIMESTAMP(6)) WHERE CONFIRMED_AT IS NULL`, "UPDATE gateway_recoveries SET invalidated_at=COALESCE(invalidated_at,clock_timestamp()) WHERE confirmed_at IS NULL", "UPDATE gateway_recoveries SET invalidated_at=COALESCE(invalidated_at,UTC_TIMESTAMP(6)) WHERE confirmed_at IS NULL")); e != nil {
		return e
	}
	_, e := q.Exec(ctx, distributionNativeSQL(q, `UPDATE API_KEYS SET REVOKED_AT=COALESCE(REVOKED_AT,CURRENT_TIMESTAMP(6)),REVOKE_REASON=COALESCE(REVOKE_REASON,'restored_state_invalidated')`, "UPDATE api_keys SET revoked_at=COALESCE(revoked_at,clock_timestamp()),revoke_reason=COALESCE(revoke_reason,'restored_state_invalidated')", "UPDATE api_keys SET revoked_at=COALESCE(revoked_at,UTC_TIMESTAMP(6)),revoke_reason=COALESCE(revoke_reason,'restored_state_invalidated')"))
	return e
}
