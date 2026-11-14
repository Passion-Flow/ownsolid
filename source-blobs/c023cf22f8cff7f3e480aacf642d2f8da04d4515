package main

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func validInstallationState(complete bool, tenant, status sql.NullString) error {
	if !complete {
		if tenant.Valid || status.Valid {
			return errors.New("INSTALLATION_STATE_INVALID")
		}
		return nil
	}
	if !tenant.Valid || tenant.String == "" || !status.Valid || status.String != "active" {
		return errors.New("INSTALLATION_STATE_INVALID")
	}
	return nil
}

func installationBootstrapped(ctx context.Context, store transaction.Store) (bool, error) {
	if store == nil {
		return false, errors.New("INSTALLATION_REQUIRED")
	}
	var complete bool
	var tenant, status, workspace, admin sql.NullString
	var at sql.NullTime
	var graph, single bool
	if e := store.QueryRow(ctx, transaction.SQL(store.Dialect(), "SELECT i.bootstrap_complete,i.tenant_id,t.status,i.default_workspace_id,i.primary_admin_user_id,i.bootstrapped_at,(w.id IS NOT NULL AND u.id IS NOT NULL AND COALESCE(u.is_platform_admin,FALSE) AND COALESCE(u.status='active',FALSE)),(SELECT COUNT(*) FROM installation_state)=1 FROM installation_state i LEFT JOIN tenants t ON t.id=i.tenant_id LEFT JOIN workspaces w ON w.tenant_id=i.tenant_id AND w.id=i.default_workspace_id LEFT JOIN users u ON u.id=i.primary_admin_user_id WHERE i.id=1")).Scan(&complete, &tenant, &status, &workspace, &admin, &at, &graph, &single); e != nil {
		return false, errors.New("INSTALLATION_STATE_UNAVAILABLE")
	}
	if e := validInstallationState(complete, tenant, status); e != nil {
		return false, e
	}
	if !single || validInstallationAuthorities(complete, workspace, admin, at, graph) != nil {
		return false, errors.New("INSTALLATION_STATE_INVALID")
	}
	return complete, nil
}

func validInstallationAuthorities(complete bool, workspace, admin sql.NullString, at sql.NullTime, graph bool) error {
	if !complete {
		if workspace.Valid || admin.Valid || at.Valid || graph {
			return errors.New("INSTALLATION_STATE_INVALID")
		}
		return nil
	}
	if !workspace.Valid || workspace.String == "" || !admin.Valid || admin.String == "" || !at.Valid || at.Time.IsZero() || !graph {
		return errors.New("INSTALLATION_STATE_INVALID")
	}
	return nil
}

func validateUnbootstrappedFeatures(c config.Config) error {
	if c.PrivateClusterConfigFile != "" || c.MonitoringConfigFile != "" || c.MiddlewareConfigFile != "" || c.TaskEvents.Enabled || c.ImageRegistry != nil || c.ObjectStorage != nil && c.ObjectStorage.Enabled || c.Models.ArtifactRoot != "" || c.Datasets.ArtifactRoot != "" || c.Training.Enabled() || c.ModelPreparation.Enabled() || c.Knowledge.Enabled || c.Plugins.Enabled || c.Workflows.Enabled {
		return errors.New("BOOTSTRAP_REQUIRED_FOR_CONFIGURED_FEATURES")
	}
	return nil
}

func installedTenant(ctx context.Context, store transaction.Store) (string, error) {
	if store == nil || store.Dialect() != transaction.PostgreSQL && store.Dialect() != transaction.MySQL {
		return "", errors.New("INSTALLATION_REQUIRED")
	}
	var tenant string
	if e := store.QueryRow(ctx, transaction.SQL(store.Dialect(), "SELECT i.tenant_id FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND t.status='active'")).Scan(&tenant); e != nil || tenant == "" {
		return "", errors.New("INSTALLATION_REQUIRED")
	}
	return tenant, nil
}
