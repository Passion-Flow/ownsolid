package mysqlbackup

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
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
			return errors.New("MYSQL_BACKUP_FOUR_CONFIGURED_STORES_REQUIRED")
		}
		for _, other := range roots[:i] {
			if relatedPaths(path, other) {
				return errors.New("MYSQL_BACKUP_RELATED_PATHS_REFUSED")
			}
		}
	}
	return nil
}
func installedTenant(ctx context.Context, q transaction.Executor) (string, error) {
	if q == nil || q.Dialect() != transaction.MySQL {
		return "", errors.New("MYSQL_BACKUP_SELECTED_IDENTITY_REQUIRED")
	}
	var tenant string
	e := q.QueryRow(ctx, transaction.SQL(transaction.MySQL, `SELECT i.tenant_id FROM installation_state i JOIN tenants t ON t.id=i.tenant_id JOIN workspaces w ON w.id=i.default_workspace_id AND w.tenant_id=t.id JOIN users u ON u.id=i.primary_admin_user_id JOIN workspace_memberships m ON m.user_id=u.id AND m.workspace_id=w.id AND m.tenant_id=t.id WHERE i.id=1 AND i.bootstrap_complete=1 AND i.bootstrapped_at IS NOT NULL AND t.status='active' AND w.status='active' AND u.status='active' AND u.is_platform_admin=1 AND m.role='owner' AND (SELECT COUNT(*) FROM installation_state)=1`)).Scan(&tenant)
	id, parseError := uuid.Parse(tenant)
	if e != nil || parseError != nil || id == uuid.Nil || id.String() != tenant {
		return "", errors.New("MYSQL_BACKUP_ACTIVE_INSTALLATION_REQUIRED")
	}
	return tenant, nil
}

// openOwners opens only real file-store/integrity owners. Processor/model
// execution is unnecessary for a stopped-runtime immutable filesystem snapshot.
func openOwners(ctx context.Context, o *mysqloperator.Context, c config.Config, accounts *identity.Service) (*storeOwners, error) {
	if e := validateStoreRoots(c); e != nil {
		return nil, e
	}
	if e := o.VerifyConfiguration(ctx, c); e != nil {
		return nil, e
	}
	if e := o.VerifyIdentity(ctx, accounts); e != nil {
		return nil, e
	}
	q, e := accounts.Repository()
	if e != nil {
		return nil, e
	}
	tenant, e := installedTenant(ctx, q)
	if e != nil {
		return nil, e
	}
	owners := &storeOwners{models: &models.Service{Identity: accounts, RootDirectory: c.Models.ArtifactRoot}, datasets: &datasets.Service{Identity: accounts}, configuration: c, tenant: tenant}
	owners.training = &training.Service{Identity: accounts, Datasets: owners.datasets}
	owners.preparations = &modelpreparations.Service{Identity: accounts, Datasets: owners.datasets, Training: owners.training}
	failed := func(e error) (*storeOwners, error) { return nil, errors.Join(e, owners.Close()) }
	owners.datasets.Store, e = datasets.OpenStore(c.Datasets.ArtifactRoot, tenant, c.Policy().Datasets.MinimumFreeBytes)
	if e != nil {
		return failed(e)
	}
	owners.training.Store, e = training.OpenStore(c.Training.ArtifactRoot, tenant, c.Policy().Training.MinimumFreeBytes, trainingwire.MaximumCheckpointBytes, trainingwire.MaximumCheckpointFiles)
	if e != nil {
		return failed(e)
	}
	owners.preparations.Store, e = modelpreparations.OpenStore(c.ModelPreparation.ArtifactRoot, tenant, c.Policy().ModelLifecycle.MinimumFreeBytes, lifecyclewire.MaximumModelBytes, lifecyclewire.MaximumModelFiles)
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
		return errors.New("MYSQL_BACKUP_FOUR_CONFIGURED_STORES_REQUIRED")
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

func importOwners(ctx context.Context, o *mysqloperator.Context, c config.Config, accounts *identity.Service, root string) error {
	if e := validateStoreRoots(c, root); e != nil {
		return e
	}
	if e := o.VerifyConfiguration(ctx, c); e != nil {
		return e
	}
	if e := o.VerifyIdentity(ctx, accounts); e != nil {
		return e
	}
	// Each real owner refuses a populated target, binds the restored database
	// references and validates every copied byte. Partial scope is retained.
	model := &models.Service{Identity: accounts, RootDirectory: c.Models.ArtifactRoot}
	data := &datasets.Service{Identity: accounts}
	train := &training.Service{Identity: accounts, Datasets: data}
	preparation := &modelpreparations.Service{Identity: accounts, Datasets: data, Training: train}
	if e := model.ImportFiles(ctx, filepath.Join(root, "models")); e != nil {
		return e
	}
	if e := data.ImportFiles(ctx, filepath.Join(root, "datasets"), c.Datasets.ArtifactRoot); e != nil {
		return e
	}
	if e := train.ImportFiles(ctx, filepath.Join(root, "training"), c.Training.ArtifactRoot); e != nil {
		return e
	}
	if e := preparation.ImportFiles(ctx, filepath.Join(root, "preparations"), c.ModelPreparation.ArtifactRoot); e != nil {
		return e
	}
	owners, e := openOwners(ctx, o, c, accounts)
	if e != nil {
		return e
	}
	return owners.Close()
}
