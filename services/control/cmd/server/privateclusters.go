package main

import (
	"context"
	"errors"

	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/privateclusters"
	privateclusterhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/privateclusters/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func openPrivateClusters(ctx context.Context, c config.Config, selected *selectedDatabase, h *identityhttp.Handler) (*privateclusterhttp.Handler, error) {
	if e := c.ValidatePrivateClusterReference(); e != nil {
		return nil, e
	}
	if c.PrivateClusterConfigFile == "" {
		return nil, nil
	}
	bad := errors.New("PRIVATE_CLUSTER_OWNER_REQUIRED")
	if ctx == nil || selected == nil || h == nil || h.InitializationError() != nil || h.Service == nil ||
		h.Workspaces == nil || h.Licensing == nil || h.PrivateClusters != nil {
		return nil, bad
	}
	store, e := selectedIdentityStore(selected, h.Service)
	if e != nil || store.Dialect() != transaction.PostgreSQL && store.Dialect() != transaction.MySQL {
		return nil, bad
	}
	if _, e = installedTenant(ctx, store); e != nil {
		return nil, e
	}
	policy, e := privateclusters.LoadEnvironment(map[string]string{privateclusters.ConfigEnvironment: c.PrivateClusterConfigFile})
	if e != nil {
		return nil, e
	}
	if policy.Policy.OperationSeconds > c.Policy().HTTP.RequestSeconds {
		return nil, errors.New("CONFIG_PRIVATE_CLUSTER_HTTP_BUDGET_INVALID")
	}
	service, e := privateclusters.New(h.Service, h.Workspaces.Service, h.Licensing.Service, policy)
	if e != nil {
		return nil, e
	}
	return &privateclusterhttp.Handler{Service: service}, nil
}
