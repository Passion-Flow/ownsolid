package databaseaccess

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const LocalizationUpgradeCommand = "upgrade-localization"
const LocalizationUpgradeComplete = "LOCALIZATION_UPGRADE_COMPLETE"

// UpgradeLocalization preserves the installed principal and identical DML grants.
// Repeating after an uncertain schema COMMIT reads the exact ledger before work.
func UpgradeLocalization(ctx context.Context, p *pgxpool.Pool, lease *pgxpool.Conn, input RuntimeInput) (string, error) {
	if !input.configuration.LocalizationOnly() || input.configuration.ValidateApplicationProfile() != nil {
		return "", errors.New("LOCALIZATION_UPGRADE_PROFILE_REQUIRED")
	}
	target, e := lockedTarget(ctx, p, lease, input)
	if e != nil {
		return "", e
	}
	if database.Check(ctx, p, "portal") != nil {
		if e = database.Check(ctx, p, "localization"); e != nil {
			return "", e
		}
	}
	exists, e := inspectRole(ctx, lease, target, input)
	if e != nil {
		return "", e
	}
	if !exists {
		return "", errors.New("LOCALIZATION_EXISTING_RUNTIME_ROLE_REQUIRED")
	}
	if e = database.Migrate(ctx, lease, "localization"); e != nil {
		if outcome, ok := transaction.CommitOutcome(e); ok {
			return "", errors.New(string(outcome))
		}
		return "", errors.New("LOCALIZATION_UPGRADE_MIGRATION_FAILED")
	}
	if e = checkRuntimeConnection(ctx, target, input); e != nil {
		return "", e
	}
	return LocalizationUpgradeComplete, nil
}
