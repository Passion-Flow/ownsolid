package postgresmaintenance

import (
	"context"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Embedded nil methods intentionally panic on any query: an invalid context
// must be refused before selecting or contacting a database owner.
type noQueryExecutor struct {
	transaction.Executor
	dialect transaction.Dialect
}

func (q noQueryExecutor) Dialect() transaction.Dialect { return q.dialect }

func TestPostgresMaintenanceRejectsForeignOrMissingAuthorityBeforeQueries(t *testing.T) {
	for _, v := range []struct {
		name    string
		ctx     context.Context
		q       transaction.Executor
		profile string
	}{
		{"nil-context", nil, noQueryExecutor{dialect: transaction.PostgreSQL}, ApplicationProfile},
		{"nil-owner", context.Background(), nil, ApplicationProfile},
		{"mysql", context.Background(), noQueryExecutor{dialect: transaction.MySQL}, ApplicationProfile},
		{"dameng", context.Background(), noQueryExecutor{dialect: transaction.Dameng}, ApplicationProfile},
		{"platform", context.Background(), noQueryExecutor{dialect: transaction.PostgreSQL}, "platform"},
		{"old-profile", context.Background(), noQueryExecutor{dialect: transaction.PostgreSQL}, "plugins"},
	} {
		t.Run(v.name, func(t *testing.T) {
			result, e := CheckWithin(v.ctx, v.q, v.profile)
			if e == nil || e.Error() != "POSTGRES_MAINTENANCE_RELEASES_CONTEXT_REQUIRED" || result.CurrentLockHeld || result.Status != "" || len(result.Checks) != 0 {
				t.Fatal("foreign/missing maintenance authority", result, e)
			}
		})
	}
}
