package audit

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func Write(ctx context.Context, tx pgx.Tx, actor, subject, audience, action, outcome, targetType, targetID, reason, requestID string) error {
	return WriteWithin(ctx, transaction.PostgreSQLTransaction(tx), actor, subject, audience, action, outcome, targetType, targetID, reason, requestID)
}

func WriteAnonymous(ctx context.Context, tx pgx.Tx, audience, action, outcome, requestID string, digest []byte) error {
	return WriteAnonymousWithin(ctx, transaction.PostgreSQLTransaction(tx), audience, action, outcome, requestID, digest)
}
