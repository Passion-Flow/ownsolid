package workspaces

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const workspaceFields = `w.id::text,w.tenant_id::text,w.name,w.status,w.version,(w.id=i.default_workspace_id),w.created_at,w.updated_at`
const mysqlWorkspaceFields = `w.id,w.tenant_id,w.name,w.status,w.version,(w.id=(SELECT default_workspace_id FROM installation_state WHERE id=1)),w.created_at,w.updated_at`
const mysqlNoteFields = `id,tenant_id,workspace_id,title,text,author_user_id,version,created_at,updated_at`
const damengWorkspaceFields = `W.ID,W.TENANT_ID,W.NAME,W.STATUS,W.VERSION,CASE WHEN W.ID=(SELECT DEFAULT_WORKSPACE_ID FROM INSTALLATION_STATE WHERE ID=1) THEN 1 ELSE 0 END,W.CREATED_AT,W.UPDATED_AT`
const damengNoteFields = `ID,TENANT_ID,WORKSPACE_ID,TITLE,TEXT,AUTHOR_USER_ID,VERSION,CREATED_AT,UPDATED_AT`

type scopeLock uint8

const (
	scopeUnlocked scopeLock = iota
	scopeShared
	scopeExclusive
)

func workspaceID(value string) bool {
	id, e := uuid.Parse(value)
	return e == nil && id != uuid.Nil && id.String() == value
}
func (s *Service) repository() (transaction.Store, error) {
	if s == nil || s.Identity == nil {
		return nil, fail("WORKSPACE_IDENTITY_REQUIRED")
	}
	if s.DB != nil && s.DB != s.Identity.DB {
		return nil, fail("WORKSPACE_STORAGE_MISMATCH")
	}
	return s.Identity.Repository()
}
func (s *Service) executor(q transaction.Executor) error {
	store, e := s.repository()
	if e != nil {
		return e
	}
	if q == nil {
		return fail("WORKSPACE_TRANSACTION_REQUIRED")
	}
	if _, pool := q.(transaction.Store); pool {
		return fail("WORKSPACE_TRANSACTION_REQUIRED")
	}
	if q.Dialect() != store.Dialect() {
		return fail("WORKSPACE_STORAGE_MISMATCH")
	}
	return nil
}
func (s *Service) borrowed(tx pgx.Tx) (transaction.Executor, error) {
	if tx == nil {
		return nil, fail("WORKSPACE_TRANSACTION_REQUIRED")
	}
	q := transaction.PostgreSQLTransaction(tx)
	return q, s.executor(q)
}
func workspaceSQL(q transaction.Executor, pg, my, dm string, args ...any) transaction.Statement {
	switch q.Dialect() {
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, my, args...)
	case transaction.Dameng:
		return transaction.SQL(transaction.Dameng, dm, args...)
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, pg, args...)
	default:
		return transaction.SQL(q.Dialect(), "")
	}
}
func workspaceError(e error) error {
	if outcome, ok := transaction.CommitOutcome(e); ok {
		// Preserve the original safe typed outcome where it is already the whole
		// error. Joined errors must expose only the selected outcome, without
		// leaking vendor details or allowing a timeout to hide uncertainty.
		if direct, ok := e.(*transaction.Error); ok && direct.Kind == outcome {
			return direct
		}
		return &transaction.Error{Kind: outcome}
	}
	var safe *transaction.Error
	if errors.As(e, &safe) && safe.Kind != transaction.CommitUnknown {
		return fail("STORAGE_UNAVAILABLE")
	}
	return e
}
func scanWorkspace(row transaction.Row) (Workspace, error) {
	var w Workspace
	e := row.Scan(&w.ID, &w.TenantID, &w.Name, &w.Status, &w.Version, &w.IsDefault, &w.CreatedAt, &w.UpdatedAt)
	return w, e
}
func (s *Service) resolveWithin(ctx context.Context, q transaction.Executor, a identity.Auth, id string, lock scopeLock) (Workspace, error) {
	if !workspaceID(id) {
		return Workspace{}, fail("RESOURCE_NOT_FOUND")
	}
	pg := "SELECT " + workspaceFields + ` FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id JOIN tenants t ON t.id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' AND w.id=$1`
	my := "SELECT " + mysqlWorkspaceFields + ` FROM workspaces w WHERE w.id=? AND EXISTS(SELECT 1 FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND i.tenant_id=w.tenant_id AND t.status='active')`
	dm := "SELECT " + damengWorkspaceFields + ` FROM WORKSPACES W WHERE W.ID=? AND EXISTS(SELECT 1 FROM INSTALLATION_STATE I JOIN TENANTS T ON T.ID=I.TENANT_ID WHERE I.ID=1 AND I.BOOTSTRAP_COMPLETE=1 AND I.TENANT_ID=W.TENANT_ID AND T.STATUS='active')`
	if lock != scopeUnlocked {
		// DM has no admitted FOR SHARE syntax. Lock only the exact Workspace;
		// retain the caller's actor/source order and read membership afterward.
		dm += " FOR UPDATE OF W.ID"
	}
	switch lock {
	case scopeShared:
		pg += " FOR SHARE OF w"
		my += " FOR SHARE"
	case scopeExclusive:
		pg += " FOR UPDATE OF w"
		my += " FOR UPDATE"
	}
	w, e := scanWorkspace(q.QueryRow(ctx, workspaceSQL(q, pg, my, dm, id)))
	if errors.Is(e, sql.ErrNoRows) {
		return Workspace{}, fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return Workspace{}, e
	}
	// Workspace writers serialize membership changes on this row. Read roles only
	// after the lock wait,never from a snapshot or client-selected tenant.
	var role string
	e = q.QueryRow(ctx, workspaceSQL(q, "SELECT role FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3", "SELECT role FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?", `SELECT ROLE FROM WORKSPACE_MEMBERSHIPS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND USER_ID=?`, w.TenantID, w.ID, a.Me.User.ID)).Scan(&role)
	if errors.Is(e, sql.ErrNoRows) {
		if !admin(a) {
			return Workspace{}, fail("RESOURCE_NOT_FOUND")
		}
	} else if e != nil {
		return Workspace{}, e
	} else {
		w.Role = &role
	}
	if !admin(a) && w.Status != "active" {
		return Workspace{}, fail("RESOURCE_NOT_FOUND")
	}
	return w, nil
}

// resolve is the existing real PostgreSQL mutation adapter. Its caller already
// holds current user/session/source authority in the owning transaction.
func (s *Service) resolve(ctx context.Context, tx pgx.Tx, a identity.Auth, id string, lock bool) (Workspace, error) {
	q, e := s.borrowed(tx)
	if e != nil {
		return Workspace{}, e
	}
	mode := scopeUnlocked
	if lock {
		mode = scopeExclusive
	}
	return s.resolveWithin(ctx, q, a, id, mode)
}
func (s *Service) readActor(ctx context.Context, a identity.Auth, fn func(context.Context, transaction.Executor) error) error {
	store, e := s.repository()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		if e := s.Identity.RecheckWithin(ctx, tx, a); e != nil {
			return e
		}
		return fn(ctx, tx)
	})
	return workspaceError(e)
}
func (s *Service) readScope(ctx context.Context, a identity.Auth, id string, owner bool, fn func(context.Context, transaction.Executor, Workspace) error) error {
	if !workspaceID(id) {
		return fail("RESOURCE_NOT_FOUND")
	}
	return s.readActor(ctx, a, func(ctx context.Context, tx transaction.Executor) error {
		w, e := s.resolveWithin(ctx, tx, a, id, scopeShared)
		if e != nil {
			return e
		}
		if owner && !admin(a) && (w.Role == nil || *w.Role != "owner") {
			return fail("ACCESS_DENIED")
		}
		return fn(ctx, tx, w)
	})
}
func (s *Service) Get(ctx context.Context, a identity.Auth, id string, owner bool) (Workspace, error) {
	var out Workspace
	e := s.readScope(ctx, a, id, owner, func(_ context.Context, _ transaction.Executor, w Workspace) error { out = w; return nil })
	if e != nil {
		return Workspace{}, e
	}
	return out, nil
}
func (s *Service) GetNote(ctx context.Context, a identity.Auth, workspace, id string) (Note, error) {
	if !workspaceID(id) {
		return Note{}, fail("RESOURCE_NOT_FOUND")
	}
	var out Note
	e := s.readScope(ctx, a, workspace, false, func(ctx context.Context, q transaction.Executor, w Workspace) error {
		var e error
		out, e = scanNote(q.QueryRow(ctx, workspaceSQL(q, "SELECT "+noteFields+" FROM workspace_notes WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 AND deleted_at IS NULL", "SELECT "+mysqlNoteFields+" FROM workspace_notes WHERE tenant_id=? AND workspace_id=? AND id=? AND deleted_at IS NULL", `SELECT `+damengNoteFields+` FROM WORKSPACE_NOTES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=? AND DELETED_AT IS NULL`, w.TenantID, w.ID, id)))
		return e
	})
	if e != nil {
		return Note{}, e
	}
	return out, nil
}

// RecheckScopeWithin rechecks ordinary authority before taking the exclusive
// Workspace fence. The caller owns its real deadline/transaction and lock order.
func (s *Service) RecheckScopeWithin(ctx context.Context, q transaction.Executor, a identity.Auth, id string) (Workspace, error) {
	if !workspaceID(id) {
		return Workspace{}, fail("RESOURCE_NOT_FOUND")
	}
	if e := s.executor(q); e != nil {
		return Workspace{}, e
	}
	if e := s.Identity.RecheckWithin(ctx, q, a); e != nil {
		return Workspace{}, e
	}
	w, e := s.resolveWithin(ctx, q, a, id, scopeExclusive)
	if e != nil {
		return Workspace{}, workspaceError(e)
	}
	return w, nil
}

// ReadScopeWithin borrows the caller's actual transaction. A shared Workspace
// fence prevents its membership/availability changing before private reads end.
// Callers with source-backed sessions acquire their ordered actor fences first.
func (s *Service) ReadScopeWithin(ctx context.Context, q transaction.Executor, a identity.Auth, id string) (Workspace, error) {
	if !workspaceID(id) {
		return Workspace{}, fail("RESOURCE_NOT_FOUND")
	}
	if e := s.executor(q); e != nil {
		return Workspace{}, e
	}
	if e := s.Identity.RecheckWithin(ctx, q, a); e != nil {
		return Workspace{}, e
	}
	w, e := s.resolveWithin(ctx, q, a, id, scopeShared)
	if e != nil {
		// The borrowing module owns response normalization. Preserve the already
		// sanitized transaction deadline/cancellation classification at this boundary.
		return Workspace{}, e
	}
	return w, nil
}
func (s *Service) RecheckScope(ctx context.Context, tx pgx.Tx, a identity.Auth, id string) (Workspace, error) {
	q, e := s.borrowed(tx)
	if e != nil {
		return Workspace{}, e
	}
	return s.RecheckScopeWithin(ctx, q, a, id)
}
