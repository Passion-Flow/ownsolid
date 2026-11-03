package models

import (
	"context"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	lifecyclewire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

// Private file operators have an installation lease at their command boundary.
// A legacy DB-only constructor remains PostgreSQL-only; an explicit Identity
// must never silently fall back to a different pool or engine.
func (s *Service) fileRepository() (transaction.Store, error) {
	if s == nil {
		return nil, fail("STORAGE_UNAVAILABLE")
	}
	if s.Identity == nil {
		if s.Workspaces != nil {
			return nil, fail("MODEL_IDENTITY_REQUIRED")
		}
		store, e := transaction.FromPostgreSQL(s.DB)
		if e != nil {
			return nil, fail("STORAGE_UNAVAILABLE")
		}
		return store, nil
	}
	if s.DB != nil && s.DB != s.Identity.DB || s.Workspaces != nil && (s.Workspaces.Identity != s.Identity || s.Workspaces.DB != nil && s.Workspaces.DB != s.Identity.DB) {
		return nil, fail("MODEL_STORAGE_MISMATCH")
	}
	store, e := s.Identity.Repository()
	if e != nil {
		return nil, fail("STORAGE_UNAVAILABLE")
	}
	if s.DB != nil && store.Dialect() != transaction.PostgreSQL {
		return nil, fail("MODEL_STORAGE_MISMATCH")
	}
	return store, nil
}

func fileRepositoryError(e error) error {
	if e == nil {
		return nil
	}
	switch e.Error() {
	case "MODEL_IDENTITY_REQUIRED", "MODEL_STORAGE_MISMATCH", "MODEL_SNAPSHOT_INVALID",
		"MODEL_FILE_INVALID", "MODEL_DERIVED_CATALOG_INVALID", "MODEL_IMPORT_BUSY",
		"RESOURCE_LIMIT_REACHED", "VALIDATION_FAILED", "STORAGE_UNAVAILABLE",
		string(transaction.Cancelled), string(transaction.Timeout), string(transaction.LockTimeout),
		string(transaction.Deadlock), string(transaction.CommitUnknown), string(transaction.CommitRolledBack):
		return fail(e.Error())
	default:
		return fail("STORAGE_UNAVAILABLE")
	}
}

func (s *Service) fileWithin(ctx context.Context, readOnly bool, operation func(context.Context, transaction.Executor) error) error {
	if ctx == nil || operation == nil {
		return fail("VALIDATION_FAILED")
	}
	store, e := s.fileRepository()
	if e != nil {
		return e
	}
	configuration := config.Config{}
	if s.Identity != nil {
		configuration = s.Identity.Config
	}
	bounded, cancel := context.WithTimeout(ctx, config.Seconds(configuration.Policy().HTTP.RequestSeconds))
	defer cancel()
	return fileRepositoryError(store.Within(bounded, readOnly, operation))
}

func fileInventoryWithin(ctx context.Context, q transaction.Executor, quiescent bool) (FileSnapshot, error) {
	out := FileSnapshot{SchemaVersion: 1, Models: []SnapshotModel{}}
	if e := q.QueryRow(ctx, modelNativeSQL(q, `SELECT TENANT_ID FROM INSTALLATION_STATE WHERE ID=1 AND BOOTSTRAP_COMPLETE=1`, `SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete`, `SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=TRUE`)).Scan(&out.TenantID); e != nil {
		return FileSnapshot{}, e
	}
	if !validID(out.TenantID) {
		return FileSnapshot{}, fail("MODEL_SNAPSHOT_INVALID")
	}
	rows, e := q.Query(ctx, modelNativeSQL(q, `SELECT ID,WORKSPACE_ID,STATE,CATALOG_DIGEST,KIND,MANIFEST FROM MODEL_VERSIONS WHERE TENANT_ID=? ORDER BY ID LIMIT 1001`,
		`SELECT id::text,workspace_id::text,state,catalog_digest,kind,manifest FROM model_versions WHERE tenant_id=$1 ORDER BY id LIMIT 1001`,
		`SELECT id,workspace_id,state,catalog_digest,kind,manifest FROM model_versions WHERE tenant_id=? ORDER BY id LIMIT 1001`, out.TenantID))
	if e != nil {
		return FileSnapshot{}, e
	}
	defer rows.Close()
	for rows.Next() {
		var model SnapshotModel
		var raw []byte
		var kind string
		if e = rows.Scan(&model.ID, &model.WorkspaceID, &model.State, &model.CatalogDigest, &kind, &raw); e != nil {
			return FileSnapshot{}, e
		}
		if !validID(model.ID) || !validID(model.WorkspaceID) || model.State != "ready" && model.State != "deleted" {
			return FileSnapshot{}, fail("MODEL_SNAPSHOT_INVALID")
		}
		version := Version{ID: model.ID, WorkspaceID: model.WorkspaceID, Kind: kind, CatalogDigest: model.CatalogDigest}
		if e = decodeVersionManifest(&version, raw, out.TenantID); e != nil {
			return FileSnapshot{}, e
		}
		if kind == "merged_lora" {
			catalog, err := lifecyclewire.DecodeDerivedCatalog(raw)
			if err != nil {
				return FileSnapshot{}, err
			}
			model.Kind, model.DerivedCatalog, out.SchemaVersion = kind, &catalog, 2
		}
		out.Models = append(out.Models, model)
		if len(out.Models) > 1000 {
			return FileSnapshot{}, fail("RESOURCE_LIMIT_REACHED")
		}
	}
	if e = rows.Err(); e != nil {
		return FileSnapshot{}, e
	}
	rows.Close()
	if quiescent {
		var pending bool
		if e = q.QueryRow(ctx, modelNativeSQL(q, `SELECT CASE WHEN EXISTS(SELECT 1 FROM MODEL_IMPORTS WHERE TENANT_ID=? AND STATE NOT IN ('ready','cancelled')) THEN 1 ELSE 0 END FROM DUAL`, `SELECT EXISTS(SELECT 1 FROM model_imports WHERE tenant_id=$1 AND state NOT IN ('ready','cancelled'))`, `SELECT EXISTS(SELECT 1 FROM model_imports WHERE tenant_id=? AND state NOT IN ('ready','cancelled'))`, out.TenantID)).Scan(&pending); e != nil {
			return FileSnapshot{}, e
		}
		if pending {
			return FileSnapshot{}, fail("MODEL_IMPORT_BUSY")
		}
	}
	return out, nil
}

func (s *Service) fileInventory(ctx context.Context, quiescent bool) (FileSnapshot, error) {
	var result FileSnapshot
	e := s.fileWithin(ctx, true, func(ctx context.Context, q transaction.Executor) error {
		var e error
		result, e = fileInventoryWithin(ctx, q, quiescent)
		return e
	})
	if e != nil {
		return FileSnapshot{}, e
	}
	return result, nil
}
