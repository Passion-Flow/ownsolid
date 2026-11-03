package workspaces

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type Service struct {
	DB       *pgxpool.Pool
	Identity *identity.Service
}

func fail(code string) error     { return errors.New(code) }
func admin(a identity.Auth) bool { return a.Me.Audience == "admin" && a.Me.User.IsPlatformAdmin }

const memberFields = `m.user_id::text,u.email_display,u.display_name,u.status,m.role,m.version,m.created_at,m.updated_at`

func (s *Service) Create(ctx context.Context, a identity.Auth, name, owner, requestID string) (Workspace, error) {
	if !mutationActor(a) {
		return Workspace{}, fail("AUTH_SESSION_EXPIRED")
	}
	if !admin(a) {
		return Workspace{}, fail("ACCESS_DENIED")
	}
	name, e := security.Name(name)
	if e != nil {
		return Workspace{}, e
	}
	var out Workspace
	e = s.mutation(ctx, a, []string{owner}, true, requestID, func(ctx context.Context, q transaction.Executor, tenant string) error {
		var active bool
		e := q.QueryRow(ctx, workspaceSQL(q, "SELECT status='active' FROM users WHERE id=$1", "SELECT status='active' FROM users WHERE id=?", `SELECT CASE WHEN STATUS='active' THEN 1 ELSE 0 END FROM USERS WHERE ID=?`, owner)).Scan(&active)
		if e != nil {
			return e
		}
		if !active {
			return fail("RESOURCE_NOT_FOUND")
		}
		var count int
		if e = q.QueryRow(ctx, workspaceSQL(q, "SELECT count(*) FROM workspaces WHERE tenant_id=$1", "SELECT count(*) FROM workspaces WHERE tenant_id=?", `SELECT COUNT(*) FROM WORKSPACES WHERE TENANT_ID=?`, tenant)).Scan(&count); e != nil {
			return e
		}
		if count >= s.Identity.Config.Policy().Workspaces.MaximumCount {
			return fail("RESOURCE_LIMIT_REACHED")
		}
		id := uuid.NewString()
		if _, e = q.Exec(ctx, workspaceSQL(q, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,$3,'active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,?,'active')", `INSERT INTO WORKSPACES(ID,TENANT_ID,NAME,STATUS) VALUES(?,?,?,'active')`, id, tenant, name)); e != nil {
			return uniqueMutation(e, "WORKSPACE_EXISTS")
		}
		if _, e = q.Exec(ctx, membershipInsertSQL(q, owner, tenant, id, "owner")); e != nil {
			return e
		}
		out, e = s.resolveWithin(ctx, q, a, id, scopeUnlocked)
		if e != nil {
			return e
		}
		return s.scopedAuditWithin(ctx, q, a, out, "workspace.created", "workspace", id, owner, "", requestID)
	})
	if e != nil {
		return Workspace{}, e
	}
	return out, nil
}
func (s *Service) Update(ctx context.Context, a identity.Auth, id, name, status string, version int64, reason, requestID string) (Workspace, error) {
	name, e := security.Name(name)
	if e != nil {
		return Workspace{}, e
	}
	if !workspaceID(id) || !ValidVersion(version) || !ValidReason(reason) || (status != "active" && status != "disabled") {
		return Workspace{}, fail("VALIDATION_FAILED")
	}
	var out Workspace
	e = s.mutation(ctx, a, nil, false, requestID, func(ctx context.Context, q transaction.Executor, _ string) error {
		w, e := s.writableWithin(ctx, q, a, id, true)
		if e != nil {
			return e
		}
		if w.Version != version {
			return fail("VERSION_CONFLICT")
		}
		if !admin(a) && status != "active" {
			return fail("ACCESS_DENIED")
		}
		if w.IsDefault && status == "disabled" {
			return fail("DEFAULT_WORKSPACE_PROTECTED")
		}
		if e = incrementable(w.Version); e != nil {
			return e
		}
		_, e = q.Exec(ctx, workspaceSQL(q, "UPDATE workspaces SET name=$1,status=$2,version=version+1,updated_at=clock_timestamp() WHERE tenant_id=$3 AND id=$4", "UPDATE workspaces SET name=?,status=?,version=version+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND id=?", `UPDATE WORKSPACES SET NAME=?,STATUS=?,VERSION=VERSION+1,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE TENANT_ID=? AND ID=?`, name, status, w.TenantID, w.ID))
		if e != nil {
			return uniqueMutation(e, "WORKSPACE_EXISTS")
		}
		action := "workspace.updated"
		if w.Status != status {
			if status == "active" {
				action = "workspace.restored"
			} else {
				action = "workspace.archived"
			}
		}
		if e = s.scopedAuditWithin(ctx, q, a, w, action, "workspace", w.ID, "", reason, requestID); e != nil {
			return e
		}
		out, e = s.resolveWithin(ctx, q, a, w.ID, scopeUnlocked)
		return e
	})
	if e != nil {
		return Workspace{}, e
	}
	return out, nil
}
func (s *Service) AddMember(ctx context.Context, a identity.Auth, id, email, role, requestID string) (Membership, error) {
	if !mutationActor(a) {
		return Membership{}, fail("AUTH_SESSION_EXPIRED")
	}
	if !workspaceID(id) || !roleValid(role) || !workspaceID(requestID) {
		return Membership{}, fail("VALIDATION_FAILED")
	}
	canonical, _, e := security.Email(email)
	if e != nil {
		return Membership{}, e
	}
	if _, e = s.Get(ctx, a, id, true); e != nil {
		return Membership{}, e
	}
	store, e := s.repository()
	if e != nil {
		return Membership{}, e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	var user string
	e = store.QueryRow(ctx, workspaceSQL(store, "SELECT id::text FROM users WHERE email_canonical=$1 AND status='active'", "SELECT id FROM users WHERE email_canonical=? AND status='active'", `SELECT ID FROM USERS WHERE EMAIL_CANONICAL=? AND STATUS='active'`, canonical)).Scan(&user)
	if errors.Is(e, sql.ErrNoRows) {
		return Membership{}, fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return Membership{}, workspaceError(e)
	}
	var out Membership
	e = s.mutation(ctx, a, []string{user}, false, requestID, func(ctx context.Context, q transaction.Executor, _ string) error {
		w, e := s.writableWithin(ctx, q, a, id, true)
		if e != nil {
			return e
		}
		var active bool
		if e = q.QueryRow(ctx, workspaceSQL(q, "SELECT status='active' FROM users WHERE id=$1", "SELECT status='active' FROM users WHERE id=?", `SELECT CASE WHEN STATUS='active' THEN 1 ELSE 0 END FROM USERS WHERE ID=?`, user)).Scan(&active); e != nil {
			return e
		}
		if !active {
			return fail("RESOURCE_NOT_FOUND")
		}
		if e = incrementable(w.Version); e != nil {
			return e
		}
		_, e = q.Exec(ctx, membershipInsertSQL(q, user, w.TenantID, w.ID, role))
		if e != nil {
			return uniqueMutation(e, "MEMBERSHIP_EXISTS")
		}
		if e = bumpWorkspace(ctx, q, w); e != nil {
			return e
		}
		if e = s.scopedAuditWithin(ctx, q, a, w, "workspace.member_added", "workspace_membership", user, user, "", requestID); e != nil {
			return e
		}
		out, e = s.membershipWithin(ctx, q, w, user)
		return e
	})
	if e != nil {
		return Membership{}, e
	}
	return out, nil
}
func (s *Service) ChangeMember(ctx context.Context, a identity.Auth, id, user, role string, version int64, reason, requestID string, remove bool) (Membership, error) {
	if !mutationActor(a) {
		return Membership{}, fail("AUTH_SESSION_EXPIRED")
	}
	if !workspaceID(requestID) || !workspaceID(id) || !workspaceID(user) || !ValidVersion(version) || !ValidReason(reason) || (!remove && !roleValid(role)) {
		return Membership{}, fail("VALIDATION_FAILED")
	}
	if _, e := s.Get(ctx, a, id, true); e != nil {
		return Membership{}, e
	}
	var out Membership
	e := s.mutation(ctx, a, []string{user}, false, requestID, func(ctx context.Context, q transaction.Executor, _ string) error {
		w, e := s.writableWithin(ctx, q, a, id, true)
		if e != nil {
			return e
		}
		m, e := s.membershipWithin(ctx, q, w, user)
		if e != nil {
			return e
		}
		if m.Version != version {
			return fail("VERSION_CONFLICT")
		}
		if e = incrementable(w.Version); e != nil {
			return e
		}
		if !remove {
			if e = incrementable(m.Version); e != nil {
				return e
			}
		}
		if m.Role == "owner" && (remove || role != "owner") {
			var owners int
			if e = q.QueryRow(ctx, workspaceSQL(q, "SELECT count(*) FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND role='owner'", "SELECT count(*) FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND role='owner'", `SELECT COUNT(*) FROM WORKSPACE_MEMBERSHIPS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ROLE='owner'`, w.TenantID, w.ID)).Scan(&owners); e != nil {
				return e
			}
			if owners <= 1 {
				return fail("LAST_WORKSPACE_OWNER")
			}
		}
		action := "workspace.member_updated"
		if remove {
			action = "workspace.member_removed"
			_, e = q.Exec(ctx, workspaceSQL(q, "DELETE FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3", "DELETE FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?", `DELETE FROM WORKSPACE_MEMBERSHIPS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND USER_ID=?`, w.TenantID, w.ID, user))
		} else {
			_, e = q.Exec(ctx, workspaceSQL(q, "UPDATE workspace_memberships SET role=$1,version=version+1,updated_at=clock_timestamp() WHERE tenant_id=$2 AND workspace_id=$3 AND user_id=$4", "UPDATE workspace_memberships SET role=?,version=version+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND user_id=?", `UPDATE WORKSPACE_MEMBERSHIPS SET ROLE=?,VERSION=VERSION+1,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE TENANT_ID=? AND WORKSPACE_ID=? AND USER_ID=?`, role, w.TenantID, w.ID, user))
		}
		if e != nil {
			return e
		}
		if e = bumpWorkspace(ctx, q, w); e != nil {
			return e
		}
		if e = s.scopedAuditWithin(ctx, q, a, w, action, "workspace_membership", user, user, reason, requestID); e != nil {
			return e
		}
		if remove {
			return nil
		}
		out, e = s.membershipWithin(ctx, q, w, user)
		return e
	})
	if e != nil {
		return Membership{}, e
	}
	return out, nil
}
