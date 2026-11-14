package main

import (
	"context"
	"errors"

	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/imageregistry"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func registrySchemaAdmission(c config.Config, store transaction.Store) error {
	if c.ImageRegistry == nil {
		if c.ImageRegistrySiteSourceFile != "" {
			return errors.New("CONFIG_IMAGE_REGISTRY_INVALID")
		}
		return nil
	}
	if store == nil || (store.Dialect() != transaction.PostgreSQL && store.Dialect() != transaction.MySQL) {
		return errors.New("REGISTRY_SCHEMA_NOT_ACTIVE")
	}
	return nil
}

func openImageRegistry(ctx context.Context, c config.Config, store transaction.Store, h *identityhttp.Handler) (*imageregistry.Service, error) {
	if e := registrySchemaAdmission(c, store); e != nil {
		return nil, e
	}
	if c.ImageRegistry == nil {
		return nil, imageregistry.RequireEmptyWhenDisabled(ctx, store)
	}
	if h == nil || h.InitializationError() != nil || h.Service == nil || h.Workspaces == nil || h.Nodes == nil || h.Deployments == nil || h.Licensing == nil {
		return nil, errors.New("REGISTRY_OWNER_REQUIRED")
	}
	if _, e := installedTenant(ctx, store); e != nil {
		return nil, e
	}
	if c.ImageRegistrySiteSourceFile != "" {
		return imageregistry.NewWithSiteReleaseSource(h.Service, h.Workspaces.Service, h.Nodes.Service, h.Deployments.Service, h.Licensing.Service, *c.ImageRegistry, c.ImageRegistrySiteSourceFile)
	}
	return imageregistry.New(h.Service, h.Workspaces.Service, h.Nodes.Service, h.Deployments.Service, h.Licensing.Service, *c.ImageRegistry)
}
