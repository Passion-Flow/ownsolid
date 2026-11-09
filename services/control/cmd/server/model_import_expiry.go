package main

import (
	"context"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func resolveModelImportExpiry(ctx context.Context, store transaction.Store, configured *config.ModelImportExpiryPolicy) (*models.ImportExpiryPolicy, error) {
	if configured == nil {
		return nil, nil
	}
	if store == nil || store.Dialect() != transaction.PostgreSQL {
		return nil, errors.New("MODEL_IMPORT_EXPIRY_POSTGRESQL_REQUIRED")
	}
	var trusted bool
	err := store.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT EXISTS (
		SELECT 1 FROM installation_state i
		JOIN tenants t ON t.id=i.tenant_id
		JOIN workspaces w ON w.tenant_id=t.id
		WHERE i.id=1 AND i.bootstrap_complete AND t.status='active'
		  AND w.id=$1 AND w.status='active'
	)`, configured.WorkspaceID)).Scan(&trusted)
	if err != nil {
		return nil, errors.New("MODEL_IMPORT_EXPIRY_SCOPE_CHECK_FAILED")
	}
	if !trusted {
		return nil, errors.New("MODEL_IMPORT_EXPIRY_WORKSPACE_UNAVAILABLE")
	}
	return &models.ImportExpiryPolicy{
		WorkspaceID: configured.WorkspaceID,
		InactiveFor: config.Seconds(configured.InactiveSeconds),
	}, nil
}
