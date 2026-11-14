package workspaces

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// MembershipAuthorityWithin is a trusted lifetime lookup after the caller's
// current scope recheck. The owning Workspace transaction fence remains held.
func (s *Service) MembershipAuthorityWithin(ctx context.Context, q transaction.Executor, tenant, workspace, user string) (string, error) {
	if !workspaceID(tenant) || !workspaceID(workspace) || !workspaceID(user) {
		return "", fail("RESOURCE_NOT_FOUND")
	}
	if e := s.executor(q); e != nil {
		return "", e
	}
	var authority string
	e := q.QueryRow(ctx, workspaceSQL(q,
		`SELECT m.authority_id::text FROM workspace_memberships m WHERE m.tenant_id=$1 AND m.workspace_id=$2 AND m.user_id=$3 AND EXISTS(SELECT 1 FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id JOIN tenants t ON t.id=w.tenant_id WHERE w.id=m.workspace_id AND w.tenant_id=m.tenant_id AND i.id=1 AND i.bootstrap_complete AND w.status='active' AND t.status='active')`,
		`SELECT m.authority_id FROM workspace_memberships m WHERE m.tenant_id=? AND m.workspace_id=? AND m.user_id=? AND EXISTS(SELECT 1 FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id JOIN tenants t ON t.id=w.tenant_id WHERE w.id=m.workspace_id AND w.tenant_id=m.tenant_id AND i.id=1 AND i.bootstrap_complete=1 AND w.status='active' AND t.status='active')`, `SELECT M.AUTHORITY_ID FROM WORKSPACE_MEMBERSHIPS M WHERE M.TENANT_ID=? AND M.WORKSPACE_ID=? AND M.USER_ID=? AND EXISTS(SELECT 1 FROM WORKSPACES W JOIN INSTALLATION_STATE I ON I.TENANT_ID=W.TENANT_ID JOIN TENANTS T ON T.ID=W.TENANT_ID WHERE W.ID=M.WORKSPACE_ID AND W.TENANT_ID=M.TENANT_ID AND I.ID=1 AND I.BOOTSTRAP_COMPLETE=1 AND W.STATUS='active' AND T.STATUS='active')`, tenant, workspace, user)).Scan(&authority)
	if errors.Is(e, sql.ErrNoRows) {
		return "", fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return "", workspaceError(e)
	}
	if !workspaceID(authority) {
		return "", fail("STORAGE_UNAVAILABLE")
	}
	return authority, nil
}
func (s *Service) MembershipAuthority(ctx context.Context, tx pgx.Tx, tenant, workspace, user string) (string, error) {
	q, e := s.borrowed(tx)
	if e != nil {
		return "", e
	}
	return s.MembershipAuthorityWithin(ctx, q, tenant, workspace, user)
}
