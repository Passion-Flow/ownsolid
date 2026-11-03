package workspaces

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const MaximumVersion = identity.MaximumAuthVersion
const MaximumReasonRunes = 500

func ValidID(value string) bool     { return workspaceID(value) }
func ValidVersion(value int64) bool { return value > 0 && value <= MaximumVersion }
func ValidReason(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, '\x00') && strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= MaximumReasonRunes
}
func mutationActor(a identity.Auth) bool {
	return workspaceID(a.Me.User.ID) && workspaceID(a.SessionID) && len(a.TokenHash) == sha256.Size && a.Epoch > 0 && (a.Me.Audience == "portal" || a.Me.Audience == "admin")
}
func incrementable(version int64) error {
	if !ValidVersion(version) || version == MaximumVersion {
		return fail("RESOURCE_LIMIT_REACHED")
	}
	return nil
}
func uniqueMutation(e error, code string) error {
	var storage *transaction.Error
	if errors.As(e, &storage) && storage.Kind == transaction.UniqueConflict {
		return fail(code)
	}
	return e
}

// mutation holds current actor/source, installation/tenant and resource authority
// in one actual transaction. No pool ownership or commit/retry authority escapes.
func (s *Service) mutation(ctx context.Context, a identity.Auth, users []string, creating bool, request string, fn func(context.Context, transaction.Executor, string) error) error {
	if !mutationActor(a) {
		return fail("AUTH_SESSION_EXPIRED")
	}
	if !workspaceID(request) {
		return fail("VALIDATION_FAILED")
	}
	for _, user := range users {
		if !workspaceID(user) {
			return fail("VALIDATION_FAILED")
		}
	}
	store, e := s.repository()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	ids := append([]string{a.Me.User.ID}, users...)
	sort.Strings(ids)
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		missing := false
		previous := ""
		for _, id := range ids {
			if id == previous {
				continue
			}
			previous = id
			var locked string
			e := q.QueryRow(ctx, workspaceSQL(q, "SELECT id::text FROM users WHERE id=$1 FOR NO KEY UPDATE", "SELECT id FROM users WHERE id=? FOR UPDATE", `SELECT ID FROM USERS WHERE ID=? FOR UPDATE`, id)).Scan(&locked)
			if errors.Is(e, sql.ErrNoRows) {
				if id == a.Me.User.ID {
					return fail("AUTH_SESSION_EXPIRED")
				}
				missing = true
				continue
			}
			if e != nil {
				return e
			}
		}
		if e := s.Identity.RecheckWithin(ctx, q, a); e != nil {
			return e
		}
		if missing {
			return fail("RESOURCE_NOT_FOUND")
		}
		lock := " FOR SHARE"
		if creating {
			lock = " FOR UPDATE"
		}
		var tenant string
		e := q.QueryRow(ctx, workspaceSQL(q, "SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete"+lock, "SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1"+lock, `SELECT TENANT_ID FROM INSTALLATION_STATE WHERE ID=1 AND BOOTSTRAP_COMPLETE=1 FOR UPDATE`)).Scan(&tenant)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("SERVICE_NOT_READY")
		}
		if e != nil {
			return e
		}
		if !workspaceID(tenant) {
			return fail("SERVICE_NOT_READY")
		}
		var active string
		e = q.QueryRow(ctx, workspaceSQL(q, "SELECT id::text FROM tenants WHERE id=$1 AND status='active' FOR SHARE", "SELECT id FROM tenants WHERE id=? AND status='active' FOR SHARE", `SELECT ID FROM TENANTS WHERE ID=? AND STATUS='active' FOR UPDATE`, tenant)).Scan(&active)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("SERVICE_NOT_READY")
		}
		if e != nil {
			return e
		}
		return fn(ctx, q, tenant)
	})
	return workspaceError(e)
}
func (s *Service) writableWithin(ctx context.Context, q transaction.Executor, a identity.Auth, id string, owner bool) (Workspace, error) {
	w, e := s.resolveWithin(ctx, q, a, id, scopeExclusive)
	if e != nil {
		return Workspace{}, e
	}
	if !admin(a) && (w.Role == nil || *w.Role == "viewer" || owner && *w.Role != "owner") {
		return Workspace{}, fail("ACCESS_DENIED")
	}
	return w, nil
}
func (s *Service) scopedAuditWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w Workspace, action, target, id, subject, reason, request string) error {
	n, e := q.Exec(ctx, workspaceSQL(q,
		`INSERT INTO audit_events(id,occurred_at,actor_user_id,subject_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,'success',$8,$9,NULLIF($10,''),$11)`,
		`INSERT INTO audit_events(id,occurred_at,actor_user_id,subject_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,NULLIF(?,''),?,?,?,?,'success',?,?,NULLIF(?,''),?)`, `INSERT INTO AUDIT_EVENTS(ID,OCCURRED_AT,ACTOR_USER_ID,SUBJECT_USER_ID,TENANT_ID,WORKSPACE_ID,AUDIENCE,ACTION,OUTCOME,TARGET_TYPE,TARGET_ID,REASON,REQUEST_ID) VALUES(?,CURRENT_TIMESTAMP(6),?,NULLIF(?,''),?,?,?,?,'success',?,?,NULLIF(?,''),?)`,
		uuid.NewString(), a.Me.User.ID, subject, w.TenantID, w.ID, a.Me.Audience, action, target, id, reason, request))
	if e == nil && n != 1 {
		return fail("AUDIT_WRITE_UNCONFIRMED")
	}
	return e
}
func (s *Service) membershipWithin(ctx context.Context, q transaction.Executor, w Workspace, user string) (Membership, error) {
	m, e := scanListedMember(q.QueryRow(ctx, workspaceSQL(q, "SELECT "+memberFields+" FROM workspace_memberships m JOIN users u ON u.id=m.user_id WHERE m.tenant_id=$1 AND m.workspace_id=$2 AND m.user_id=$3", "SELECT "+mysqlMemberFields+" FROM workspace_memberships m JOIN users u ON u.id=m.user_id WHERE m.tenant_id=? AND m.workspace_id=? AND m.user_id=?", `SELECT `+damengMemberFields+` FROM WORKSPACE_MEMBERSHIPS M JOIN USERS U ON U.ID=M.USER_ID WHERE M.TENANT_ID=? AND M.WORKSPACE_ID=? AND M.USER_ID=?`, w.TenantID, w.ID, user)))
	if errors.Is(e, sql.ErrNoRows) {
		return Membership{}, fail("RESOURCE_NOT_FOUND")
	}
	return m, e
}
func bumpWorkspace(ctx context.Context, q transaction.Executor, w Workspace) error {
	if e := incrementable(w.Version); e != nil {
		return e
	}
	_, e := q.Exec(ctx, workspaceSQL(q, "UPDATE workspaces SET version=version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND id=$2", "UPDATE workspaces SET version=version+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND id=?", `UPDATE WORKSPACES SET VERSION=VERSION+1,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE TENANT_ID=? AND ID=?`, w.TenantID, w.ID))
	return e
}
