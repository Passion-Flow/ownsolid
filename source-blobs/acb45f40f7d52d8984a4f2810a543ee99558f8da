package main

import (
	"context"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/artifactstores"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
	"strings"
	"unicode/utf8"
)

func artifactSchemaAdmission(c config.Config, store transaction.Store) error {
	if c.ObjectStorage == nil || !c.ObjectStorage.Enabled {
		return nil
	}
	if store == nil || (store.Dialect() != transaction.PostgreSQL && store.Dialect() != transaction.MySQL) {
		return errors.New("OBJECT_SCHEMA_NOT_ACTIVE")
	}
	return nil
}

// openArtifactStores transfers both handles only after the real owner admits them.
func openArtifactStores(ctx context.Context, c config.Config, store transaction.Store, h *identityhttp.Handler) (out *artifactstores.Service, result error) {
	if c.ObjectStorage == nil || !c.ObjectStorage.Enabled {
		return nil, nil
	}
	if e := artifactSchemaAdmission(c, store); e != nil {
		return nil, e
	}
	tenant, e := installedTenant(ctx, store)
	if e != nil {
		return nil, e
	}
	transport, e := objectstore.NewS3(*c.ObjectStorage)
	if e != nil {
		return nil, e
	}
	defer func() {
		if out == nil {
			result = errors.Join(result, transport.Close())
		}
	}()
	stage, e := objectstore.OpenStage(*c.ObjectStorage, tenant)
	if e != nil {
		return nil, e
	}
	defer func() {
		if out == nil {
			result = errors.Join(result, stage.Close())
		}
	}()
	return artifactstores.NewService(ctx, h.Service, h.Models.Service.Workspaces, h.Models.Service, h.Datasets.Service, *c.ObjectStorage, transport, stage)
}

func verifyArtifactStore(ctx context.Context, c config.Config, store transaction.Store, h *identityhttp.Handler) (result error) {
	if c.ObjectStorage == nil || !c.ObjectStorage.Enabled {
		return errors.New("OBJECT_NOT_ADMITTED")
	}
	if e := h.InitializationError(); e != nil {
		return e
	}
	if e := h.Datasets.Service.OpenConfiguredStore(ctx); e != nil {
		return e
	}
	if h.Datasets.Service.Store != nil {
		defer func() { result = errors.Join(result, h.Datasets.Service.Store.Close()) }()
	}
	s, e := openArtifactStores(ctx, c, store, h)
	if e != nil {
		return e
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.Seconds(c.Policy().HTTP.ShutdownSeconds))
		defer cancel()
		result = errors.Join(result, s.Close(cleanup))
	}()
	return s.VerifyStored(ctx)
}

func validArtifactOperatorReason(reason string) bool {
	return utf8.ValidString(reason) && strings.TrimSpace(reason) != "" && len([]rune(reason)) <= 500
}
