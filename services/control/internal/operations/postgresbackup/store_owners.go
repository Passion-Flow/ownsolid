// Reuses the existing four domain snapshot owners without changing their source.
package postgresbackup

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	lifecyclewire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	trainingwire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type storeOwners struct {
	models        *models.Service
	datasets      *datasets.Service
	training      *training.Service
	preparations  *modelpreparations.Service
	configuration config.Config
	tenant        string
}

func relatedPaths(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}
func storeRoots(c config.Config) []string {
	return []string{c.Models.ArtifactRoot, c.Datasets.ArtifactRoot, c.Training.ArtifactRoot, c.ModelPreparation.ArtifactRoot}
}
func validateStoreRoots(c config.Config, extra ...string) error {
	roots := append(storeRoots(c), extra...)
	for i, path := range roots {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) {
			return errors.New("POSTGRES_BACKUP_FOUR_CONFIGURED_STORES_REQUIRED")
		}
		for _, other := range roots[:i] {
			if relatedPaths(path, other) {
				return errors.New("POSTGRES_BACKUP_RELATED_PATHS_REFUSED")
			}
		}
	}
	return nil
}

// openOwners opens only real file-store/integrity owners. Processor/model
// execution is unnecessary for a stopped-runtime immutable filesystem snapshot.
func openOwners(ctx context.Context, c config.Config, accounts *identity.Service, tenant string) (*storeOwners, error) {
	return openStoreOwners(ctx, c, accounts, tenant, false)
}

func openVerificationOwners(ctx context.Context, c config.Config, accounts *identity.Service, tenant string) (*storeOwners, error) {
	return openStoreOwners(ctx, c, accounts, tenant, true)
}

func openStoreOwners(ctx context.Context, c config.Config, accounts *identity.Service, tenant string, verification bool) (*storeOwners, error) {
	if e := validateStoreRoots(c); e != nil {
		return nil, e
	}
	owners := &storeOwners{models: &models.Service{Identity: accounts, RootDirectory: c.Models.ArtifactRoot}, datasets: &datasets.Service{Identity: accounts}, configuration: c, tenant: tenant}
	owners.training = &training.Service{Identity: accounts, Datasets: owners.datasets}
	owners.preparations = &modelpreparations.Service{Identity: accounts, Datasets: owners.datasets, Training: owners.training}
	var e error
	failed := func(e error) (*storeOwners, error) { return nil, errors.Join(e, owners.Close()) }
	openDataset, openTraining, openPreparation := datasets.OpenStore, training.OpenStore, modelpreparations.OpenStore
	if verification {
		openDataset, openTraining, openPreparation = datasets.OpenStoreForVerification, training.OpenStoreForVerification, modelpreparations.OpenStoreForVerification
	}
	owners.datasets.Store, e = openDataset(c.Datasets.ArtifactRoot, tenant, c.Policy().Datasets.MinimumFreeBytes)
	if e != nil {
		return failed(e)
	}
	owners.training.Store, e = openTraining(c.Training.ArtifactRoot, tenant, c.Policy().Training.MinimumFreeBytes, trainingwire.MaximumCheckpointBytes, trainingwire.MaximumCheckpointFiles)
	if e != nil {
		return failed(e)
	}
	owners.preparations.Store, e = openPreparation(c.ModelPreparation.ArtifactRoot, tenant, c.Policy().ModelLifecycle.MinimumFreeBytes, lifecyclewire.MaximumModelBytes, lifecyclewire.MaximumModelFiles)
	if e != nil {
		return failed(e)
	}
	if e = owners.Verify(ctx); e != nil {
		return failed(e)
	}
	return owners, nil
}
func (s *storeOwners) Close() (result error) {
	if s == nil {
		return nil
	}
	if s.preparations != nil && s.preparations.Store != nil {
		result = errors.Join(result, s.preparations.Store.Close())
	}
	if s.training != nil && s.training.Store != nil {
		result = errors.Join(result, s.training.Store.Close())
	}
	if s.datasets != nil && s.datasets.Store != nil {
		result = errors.Join(result, s.datasets.Store.Close())
	}
	return result
}
func (s *storeOwners) Verify(ctx context.Context) error {
	if s == nil || s.models == nil || s.datasets == nil || s.training == nil || s.preparations == nil {
		return errors.New("POSTGRES_BACKUP_FOUR_CONFIGURED_STORES_REQUIRED")
	}
	for _, verify := range []func(context.Context) error{s.models.VerifyStoredVersions, s.datasets.VerifyStored, s.training.VerifyStored, s.preparations.VerifyStored} {
		if e := verify(ctx); e != nil {
			return e
		}
	}
	return nil
}
func (s *storeOwners) Export(ctx context.Context, root string) error {
	if e := validateStoreRoots(s.configuration, root); e != nil {
		return e
	}
	if e := s.Verify(ctx); e != nil {
		return e
	}
	for _, item := range []struct {
		kind   string
		export func(context.Context, string) error
	}{{"models", s.models.ExportFiles}, {"datasets", s.datasets.ExportFiles}, {"training", s.training.ExportFiles}, {"preparations", s.preparations.ExportFiles}} {
		if e := item.export(ctx, filepath.Join(root, item.kind)); e != nil {
			return e
		}
	}
	return s.Verify(ctx)
}

func importRestoreOwners(ctx context.Context, c config.Config, accounts *identity.Service, source string) error {
	if e := validateStoreRoots(c, source); e != nil {
		return e
	}
	model := &models.Service{Identity: accounts, RootDirectory: c.Models.ArtifactRoot}
	data := &datasets.Service{Identity: accounts}
	train := &training.Service{Identity: accounts, Datasets: data}
	preparation := &modelpreparations.Service{Identity: accounts, Datasets: data, Training: train}
	for _, item := range []struct {
		kind string
		run  func() error
	}{
		{"models", func() error { return model.ImportFiles(ctx, filepath.Join(source, "models")) }},
		{"datasets", func() error { return data.ImportFiles(ctx, filepath.Join(source, "datasets"), c.Datasets.ArtifactRoot) }},
		{"training", func() error {
			return train.ImportFiles(ctx, filepath.Join(source, "training"), c.Training.ArtifactRoot)
		}},
		{"preparations", func() error {
			return preparation.ImportFiles(ctx, filepath.Join(source, "preparations"), c.ModelPreparation.ArtifactRoot)
		}},
	} {
		if e := item.run(); e != nil {
			return e
		}
	}
	return nil
}
