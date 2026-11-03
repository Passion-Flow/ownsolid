package databaseaccess

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
)

const PluginsUpgradeCommand = "upgrade-plugins"
const PluginsUpgradeComplete = "PLUGINS_UPGRADE_COMPLETE"

// UpgradePlugins extends only the delivered M13 installation and its existing
// runtime principal. Schema and grants each commit atomically. A crash between
// them fails serving closed; a repeat accepts only the exact old or new grant set.
// It never creates/rekeys a role, changes old checksums or bootstraps users.
func UpgradePlugins(ctx context.Context, p *pgxpool.Pool, lease *pgxpool.Conn, input RuntimeInput) (string, error) {
	if !input.configuration.PluginsOnly() || input.configuration.ValidateApplicationProfile() != nil {
		return "", errors.New("PLUGINS_UPGRADE_PROFILE_REQUIRED")
	}
	target, e := lockedTarget(ctx, p, lease, input)
	if e != nil {
		return "", e
	}
	old := input
	c := input.configuration
	profile := "workflows"
	c.Server.ApplicationProfile = &profile
	old.configuration = c
	if database.Check(ctx, p, "workflows") != nil {
		if e = database.Check(ctx, p, "plugins"); e != nil {
			return "", e
		}
		if exists, err := inspectRole(ctx, lease, target, input); err == nil && exists {
			if e = checkRuntimeConnection(ctx, target, input); e != nil {
				return "", e
			}
			return PluginsUpgradeComplete, nil
		}
	}
	exists, e := inspectRole(ctx, lease, target, old)
	if e != nil {
		return "", e
	}
	if !exists {
		return "", errors.New("PLUGINS_EXISTING_RUNTIME_ROLE_REQUIRED")
	}
	if e = database.Migrate(ctx, lease, "plugins"); e != nil {
		return "", errors.New("PLUGINS_UPGRADE_MIGRATION_FAILED")
	}
	tx, e := lease.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	for _, table := range []string{"plugin_versions", "plugin_grants", "plugin_mutation_intents", "workflow_plugin_references"} {
		if _, e = tx.Exec(ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON TABLE "+pgx.Identifier{target.Schema, table}.Sanitize()+" TO "+pgx.Identifier{input.connection.ConnConfig.User}.Sanitize()); e != nil {
			return "", errors.New("PLUGINS_UPGRADE_GRANT_FAILED")
		}
	}
	if _, e = inspectRole(ctx, tx, target, input); e != nil {
		return "", e
	}
	if e = tx.Commit(ctx); e != nil {
		return "", errors.New("PLUGINS_UPGRADE_OUTCOME_UNKNOWN")
	}
	if e = checkRuntimeConnection(ctx, target, input); e != nil {
		return "", e
	}
	return PluginsUpgradeComplete, nil
}
