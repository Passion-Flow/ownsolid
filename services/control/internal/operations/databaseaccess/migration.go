package databaseaccess

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const MigrationCommand = "migrate-with-runtime-context"

// MigrateWithRuntimeContext verifies the intended runtime connection context and
// actual privileged pool/locked-session target before any migration DDL. The
// intended runtime role may not exist yet; its later real login remains a
// separate check-runtime-access prerequisite, never inferred here.
func MigrateWithRuntimeContext(ctx context.Context, pool *pgxpool.Pool, lease *pgxpool.Conn, input RuntimeInput) error {
	if _, e := lockedTarget(ctx, pool, lease, input); e != nil {
		return e
	}
	if e := database.Migrate(ctx, lease, input.configuration.Server.Profile()); e != nil {
		// An unknown final COMMIT takes precedence over generic cancellation or
		// failure. The caller must observe the original ledger before recovery.
		if outcome, ok := transaction.CommitOutcome(e); ok {
			return errors.New(string(outcome))
		}
		return errors.New("MIGRATION_FAILED")
	}
	return nil
}
