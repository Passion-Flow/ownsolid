package models

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

// ReadyReferencesInTransaction preserves ordinary current caller/Workspace/model
// authority without opening a nested caller-lock transaction for a tool step.
func (s *Service) ReadyReferencesInTransaction(ctx context.Context, tx pgx.Tx, a identity.Auth, workspace string, maximum int) ([]Version, error) {
	return s.ReadyReferencesWithin(ctx, transaction.PostgreSQLTransaction(tx), a, workspace, maximum)
}

func (s *Service) ReadyReferencesWithin(ctx context.Context, tx transaction.Executor, a identity.Auth, workspace string, maximum int) ([]Version, error) {
	if e := s.toolExecutor(ctx, tx); e != nil {
		return nil, e
	}
	if !validID(workspace) || maximum < 1 || maximum > 100 {
		return nil, fail("VALIDATION_FAILED")
	}
	if a.Me.Audience != "portal" && !admin(a) {
		return nil, fail("ACCESS_DENIED")
	}
	if e := s.Identity.RecheckOrderedWithin(ctx, tx, a); e != nil {
		return nil, e
	}
	w, e := s.Workspaces.RecheckScopeWithin(ctx, tx, a, workspace)
	if e != nil {
		return nil, e
	}
	if w.Status != "active" || w.Role == nil {
		return nil, fail("ACCESS_DENIED")
	}
	rows, e := tx.Query(ctx, modelPageSQL(tx, `SELECT ID,WORKSPACE_ID,NAME,MODEL_KEY,CATALOG_DIGEST,MANIFEST,STATE,CREATED_AT,KIND FROM MODEL_VERSIONS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND STATE='ready' ORDER BY CREATED_AT DESC,ID DESC`, `SELECT id::text,workspace_id::text,name,model_key,catalog_digest,manifest,state,created_at,kind FROM model_versions WHERE tenant_id=$1 AND workspace_id=$2 AND state='ready' ORDER BY created_at DESC,id DESC LIMIT $3`, `SELECT id,workspace_id,name,model_key,catalog_digest,manifest,state,created_at,kind FROM model_versions WHERE tenant_id=? AND workspace_id=? AND state='ready' ORDER BY created_at DESC,id DESC LIMIT ?`, w.TenantID, w.ID, maximum+1))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Version{}
	for rows.Next() {
		var item Version
		var manifest []byte
		if e = rows.Scan(&item.ID, &item.WorkspaceID, &item.Name, &item.ModelKey, &item.CatalogDigest, &manifest, &item.State, &item.CreatedAt, &item.Kind); e != nil {
			return nil, e
		}
		if e = decodeVersionManifest(&item, manifest, w.TenantID); e != nil {
			return nil, e
		}
		item.CreatedAt = item.CreatedAt.UTC()
		out = append(out, item)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if len(out) > maximum {
		return nil, fail("PLUGIN_MODEL_LIMIT_EXCEEDED")
	}
	return out, nil
}

// CheckToolReferenceInTransaction verifies only an actual returned immutable
// model reference after the caller checked identity/Workspace in this same
// transaction; newly added models do not invalidate historical snapshots.
func (s *Service) CheckToolReferenceInTransaction(ctx context.Context, tx pgx.Tx, tenant, workspace, id, name, kind, key, digest string) error {
	return s.CheckToolReferenceWithin(ctx, transaction.PostgreSQLTransaction(tx), tenant, workspace, id, name, kind, key, digest)
}

func (s *Service) CheckToolReferenceWithin(ctx context.Context, tx transaction.Executor, tenant, workspace, id, name, kind, key, digest string) error {
	if e := s.toolExecutor(ctx, tx); e != nil {
		return e
	}
	if !validID(tenant) || !validID(workspace) || !validID(id) {
		return fail("PLUGIN_MODEL_REFERENCE_CHANGED")
	}
	var current Version
	var manifest []byte
	e := tx.QueryRow(ctx, modelNativeSQL(tx, `SELECT ID,WORKSPACE_ID,NAME,MODEL_KEY,CATALOG_DIGEST,MANIFEST,STATE,CREATED_AT,KIND FROM MODEL_VERSIONS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=? AND STATE='ready' FOR UPDATE`, `SELECT id::text,workspace_id::text,name,model_key,catalog_digest,manifest,state,created_at,kind FROM model_versions WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 AND state='ready' FOR SHARE`, `SELECT id,workspace_id,name,model_key,catalog_digest,manifest,state,created_at,kind FROM model_versions WHERE tenant_id=? AND workspace_id=? AND id=? AND state='ready' FOR SHARE`, tenant, workspace, id)).Scan(&current.ID, &current.WorkspaceID, &current.Name, &current.ModelKey, &current.CatalogDigest, &manifest, &current.State, &current.CreatedAt, &current.Kind)
	if errors.Is(e, sql.ErrNoRows) {
		return fail("PLUGIN_MODEL_REFERENCE_CHANGED")
	}
	if e != nil {
		return e
	}
	if e = decodeVersionManifest(&current, manifest, tenant); e != nil {
		return e
	}
	if current.Name != name || current.Kind != kind || current.ModelKey != key || current.CatalogDigest != digest {
		return fail("PLUGIN_MODEL_REFERENCE_CHANGED")
	}
	return nil
}

func (s *Service) toolExecutor(ctx context.Context, q transaction.Executor) error {
	store, e := s.repository()
	if e != nil {
		return e
	}
	if ctx == nil || q == nil || store.Dialect() != q.Dialect() {
		return fail("MODEL_STORAGE_MISMATCH")
	}
	if _, pool := q.(transaction.Store); pool {
		return fail("MODEL_TRANSACTION_REQUIRED")
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return fail(string(transaction.Cancelled))
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fail(string(transaction.Timeout))
	}
	return nil
}
