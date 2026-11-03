package main

import (
	"context"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/releases"
	releasehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/releases/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func releaseViewStore(selected *selectedDatabase, service *identity.Service) (transaction.Store, error) {
	store, e := selectedIdentityStore(selected, service)
	if e != nil {
		return nil, errors.New("RELEASE_VIEW_CONFIGURATION_INVALID")
	}
	return store, nil
}

func openReleaseView(ctx context.Context, c config.Config, selected *selectedDatabase, h *identityhttp.Handler) (*releasehttp.Handler, error) {
	if c.ValidateReleaseViewReference() != nil || ctx == nil || h == nil || h.Service == nil || h.Locales == nil || h.Releases != nil {
		return nil, errors.New("RELEASE_VIEW_CONFIGURATION_INVALID")
	}
	store, e := releaseViewStore(selected, h.Service)
	if e != nil {
		return nil, errors.New("RELEASE_VIEW_CONFIGURATION_INVALID")
	}
	if _, e = installedTenant(ctx, store); e != nil {
		return nil, e
	}
	policy, e := releases.Load(c.ReleaseViewConfigFile)
	if e != nil {
		return nil, e
	}
	if policy.OperationSeconds > c.Policy().HTTP.RequestSeconds {
		return nil, errors.New("RELEASE_VIEW_CONFIGURATION_INVALID")
	}
	s, e := releases.New(h.Service, h.Locales, policy)
	if e != nil {
		return nil, e
	}
	if e = s.VerifyStored(ctx); e != nil {
		return nil, e
	}
	return &releasehttp.Handler{Service: s}, nil
}
