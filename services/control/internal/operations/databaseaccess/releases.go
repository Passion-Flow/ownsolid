package databaseaccess

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
)

const ReleasesUpgradeCommand = "upgrade-releases"
const ReleasesUpgradeComplete = "RELEASES_UPGRADE_COMPLETE"

// UpgradeReleases admits the delivered M16 catalog and existing principal. M17
// adds only a journal projection, so neither DDL nor grants/password repair is
// needed. The offline operator separately owns backup/image/activation history.
func UpgradeReleases(ctx context.Context, p *pgxpool.Pool, lease *pgxpool.Conn, input RuntimeInput) (string, error) {
	if !input.configuration.ReleasesRuntime() || input.configuration.ValidateApplicationProfile() != nil {
		return "", errors.New("RELEASES_UPGRADE_PROFILE_REQUIRED")
	}
	target, err := lockedTarget(ctx, p, lease, input)
	if err != nil {
		return "", err
	}
	if err = database.Check(ctx, p, input.configuration.Server.Profile()); err != nil {
		return "", err
	}
	exists, err := inspectRole(ctx, lease, target, input)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", errors.New("RELEASES_EXISTING_RUNTIME_ROLE_REQUIRED")
	}
	if err = checkRuntimeConnection(ctx, target, input); err != nil {
		return "", err
	}
	return ReleasesUpgradeComplete, nil
}
