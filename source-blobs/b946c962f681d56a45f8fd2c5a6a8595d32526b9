package recovery

import (
	"context"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/middleware"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func fenceRestoredMiddlewareWithin(ctx context.Context, q transaction.Executor, accounts *identity.Service, reason string) error {
	query := "SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name IN ('middleware_instances','middleware_operations')"
	if q.Dialect() == transaction.MySQL {
		query = "SELECT count(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('middleware_instances','middleware_operations')"
	}
	var tables int
	if e := q.QueryRow(ctx, transaction.SQL(q.Dialect(), query)).Scan(&tables); e != nil {
		return e
	}
	if tables == 0 && accounts.Config.MiddlewareConfigFile == "" {
		return nil
	}
	if tables != 2 {
		return errors.New("MIDDLEWARE_SCHEMA_REJECTED")
	}
	return middleware.InvalidateAuthoritiesWithin(ctx, q, reason)
}
