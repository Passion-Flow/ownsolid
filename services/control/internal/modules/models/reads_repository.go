package models

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const mysqlImportFields = "id,workspace_id,name,catalog_digest,expected_bytes,uploaded_bytes,state,error_code,model_id,attempts,created_at,updated_at"
const versionFields = "id::text,workspace_id::text,name,model_key,catalog_digest,manifest,state,created_at,kind"
const mysqlVersionFields = "id,workspace_id,name,model_key,catalog_digest,manifest,state,created_at,kind"

func (s *Service) modelScope(ctx context.Context, q transaction.Executor, a identity.Auth, workspace string) (workspaces.Workspace, error) {
	if !validID(workspace) {
		return workspaces.Workspace{}, fail("RESOURCE_NOT_FOUND")
	}
	return s.Workspaces.ReadScopeWithin(ctx, q, a, workspace)
}

func scanVersion(row transaction.Row, tenant string) (Version, error) {
	var out Version
	var raw []byte
	e := row.Scan(&out.ID, &out.WorkspaceID, &out.Name, &out.ModelKey, &out.CatalogDigest, &raw, &out.State, &out.CreatedAt, &out.Kind)
	if errors.Is(e, sql.ErrNoRows) {
		return Version{}, fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return Version{}, e
	}
	if e = decodeVersionManifest(&out, raw, tenant); e != nil {
		return Version{}, e
	}
	out.CreatedAt = out.CreatedAt.UTC()
	return out, nil
}

func (s *Service) listVersions(ctx context.Context, a identity.Auth, workspace string) ([]Version, error) {
	if !validID(workspace) {
		return nil, fail("VALIDATION_FAILED")
	}
	out := []Version{}
	var fill *directoryFill
	e := s.readActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		w, e := s.modelScope(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		out, fill, e = s.cachedDirectory(ctx, q, w)
		return e
	})
	if e != nil {
		return nil, e
	}
	// Populate only after the authoritative transaction completed successfully.
	// Failed/unknown commits never publish either a response or cache bytes.
	if fill != nil {
		_ = s.DirectoryCache.Set(ctx, fill.key, fill.value)
	}
	return out, nil
}

func (s *Service) getVersion(ctx context.Context, a identity.Auth, workspace, id string) (Version, error) {
	if !validID(id) || workspace == "" && !admin(a) || workspace != "" && !validID(workspace) {
		return Version{}, fail("RESOURCE_NOT_FOUND")
	}
	var out Version
	e := s.readActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		if workspace == "" {
			// Discover only the installation-owned scope before its authorization.
			e := q.QueryRow(ctx, modelNativeSQL(q, `SELECT M.WORKSPACE_ID FROM MODEL_VERSIONS M JOIN INSTALLATION_STATE I ON I.TENANT_ID=M.TENANT_ID WHERE I.ID=1 AND I.BOOTSTRAP_COMPLETE=1 AND M.ID=? AND M.STATE='ready'`,
				`SELECT m.workspace_id::text FROM model_versions m JOIN installation_state i ON i.tenant_id=m.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND m.id=$1 AND m.state='ready'`,
				`SELECT m.workspace_id FROM model_versions m JOIN installation_state i ON i.tenant_id=m.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND m.id=? AND m.state='ready'`, id)).Scan(&workspace)
			if errors.Is(e, sql.ErrNoRows) {
				return fail("RESOURCE_NOT_FOUND")
			}
			if e != nil {
				return e
			}
		}
		w, e := s.modelScope(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		out, e = scanVersion(q.QueryRow(ctx, modelNativeSQL(q, "SELECT "+nativeVersionFields+" FROM MODEL_VERSIONS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=? AND STATE='ready'",
			"SELECT "+versionFields+" FROM model_versions WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 AND state='ready'",
			"SELECT "+mysqlVersionFields+" FROM model_versions WHERE tenant_id=? AND workspace_id=? AND id=? AND state='ready'",
			w.TenantID, w.ID, id)), w.TenantID)
		return e
	})
	if e != nil {
		return Version{}, e
	}
	return out, nil
}

func (s *Service) listImports(ctx context.Context, a identity.Auth, workspace string) ([]Import, error) {
	if !admin(a) {
		return nil, fail("ACCESS_DENIED")
	}
	if !validID(workspace) {
		return nil, fail("VALIDATION_FAILED")
	}
	out := []Import{}
	e := s.readActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		w, e := s.modelScope(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		rows, e := q.Query(ctx, modelPageSQL(q, "SELECT "+nativeImportFields+" FROM MODEL_IMPORTS WHERE TENANT_ID=? AND WORKSPACE_ID=? ORDER BY CREATED_AT DESC,ID DESC",
			"SELECT "+importFields+" FROM model_imports WHERE tenant_id=$1 AND workspace_id=$2 ORDER BY created_at DESC,id DESC LIMIT $3",
			"SELECT "+mysqlImportFields+" FROM model_imports WHERE tenant_id=? AND workspace_id=? ORDER BY created_at DESC,id DESC LIMIT ?",
			w.TenantID, w.ID, s.Identity.Config.Policy().UI.LookupPageSize))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			value, e := scanImport(rows)
			if e != nil {
				return e
			}
			value.CreatedAt, value.UpdatedAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC()
			out = append(out, value)
		}
		return rows.Err()
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}

func (s *Service) getImport(ctx context.Context, a identity.Auth, id string) (Import, error) {
	if !admin(a) {
		return Import{}, fail("ACCESS_DENIED")
	}
	if !validID(id) {
		return Import{}, fail("RESOURCE_NOT_FOUND")
	}
	var out Import
	e := s.readActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		var workspace string
		e := q.QueryRow(ctx, modelNativeSQL(q, `SELECT M.WORKSPACE_ID FROM MODEL_IMPORTS M JOIN INSTALLATION_STATE I ON I.TENANT_ID=M.TENANT_ID WHERE I.ID=1 AND I.BOOTSTRAP_COMPLETE=1 AND M.ID=?`,
			`SELECT m.workspace_id::text FROM model_imports m JOIN installation_state i ON i.tenant_id=m.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND m.id=$1`,
			`SELECT m.workspace_id FROM model_imports m JOIN installation_state i ON i.tenant_id=m.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND m.id=?`, id)).Scan(&workspace)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("RESOURCE_NOT_FOUND")
		}
		if e != nil {
			return e
		}
		w, e := s.modelScope(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		out, e = scanImport(q.QueryRow(ctx, modelNativeSQL(q, "SELECT "+nativeImportFields+" FROM MODEL_IMPORTS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=?",
			"SELECT "+importFields+" FROM model_imports WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3",
			"SELECT "+mysqlImportFields+" FROM model_imports WHERE tenant_id=? AND workspace_id=? AND id=?",
			w.TenantID, w.ID, id)))
		out.CreatedAt, out.UpdatedAt = out.CreatedAt.UTC(), out.UpdatedAt.UTC()
		return e
	})
	if e != nil {
		return Import{}, e
	}
	return out, nil
}
