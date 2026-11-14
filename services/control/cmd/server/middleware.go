package main

import (
	"context"
	"errors"

	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/middleware"
	middlewarehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/middleware/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func openMiddleware(ctx context.Context, c config.Config, selected *selectedDatabase, h *identityhttp.Handler) (*middlewarehttp.Handler, error) {
	if e := c.ValidateMiddlewareReferences(); e != nil {
		return nil, e
	}
	if c.MiddlewareConfigFile == "" {
		return nil, nil
	}
	if ctx == nil || selected == nil || h == nil || h.InitializationError() != nil || h.Service == nil ||
		h.Workspaces == nil || h.Workspaces.Service == nil || h.Middleware != nil {
		return nil, errors.New("MIDDLEWARE_IDENTITY_REPOSITORY_REQUIRED")
	}
	store, e := selectedIdentityStore(selected, h.Service)
	if e != nil || h.Workspaces.Service.Identity != h.Service {
		return nil, errors.New("MIDDLEWARE_IDENTITY_REPOSITORY_REQUIRED")
	}
	if store.Dialect() != transaction.PostgreSQL && store.Dialect() != transaction.MySQL {
		return nil, errors.New("MIDDLEWARE_SCHEMA_REJECTED")
	}
	if _, e = installedTenant(ctx, store); e != nil {
		return nil, e
	}
	configuration, e := middleware.LoadFiles(c.MiddlewareConfigFile, c.MiddlewareEnvironmentFile)
	if e != nil {
		return nil, e
	}
	s, e := middleware.New(h.Service, h.Workspaces.Service, configuration)
	if e != nil {
		return nil, e
	}
	return &middlewarehttp.Handler{Service: s}, nil
}
