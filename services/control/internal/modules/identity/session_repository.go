package identity

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"errors"
	"sort"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const maximumConfirmationPasswordBytes = 4096
const mysqlUserColumns = "id,email_display,display_name,locale,status,is_platform_admin,email_verified_at,version,created_at,updated_at,auth_epoch"
const damengUserColumns = "ID,EMAIL_DISPLAY,DISPLAY_NAME,LOCALE,STATUS,IS_PLATFORM_ADMIN,EMAIL_VERIFIED_AT,VERSION,CREATED_AT,UPDATED_AT,AUTH_EPOCH"

func sessionAudience(value string) bool { return value == "portal" || value == "admin" }
func sessionID(value string) bool {
	id, e := uuid.Parse(value)
	return e == nil && id != uuid.Nil && id.String() == value
}
func sessionActor(a Auth) bool {
	return sessionID(a.Me.User.ID) && sessionID(a.SessionID) && sessionAudience(a.Me.Audience) && a.Epoch > 0 && len(a.TokenHash) == sha256.Size
}
func (s *Service) sessionExecutor(q transaction.Executor) error {
	store, e := s.Repository()
	if e != nil {
		return e
	}
	if q == nil || q.Dialect() != store.Dialect() {
		return failure("IDENTITY_TRANSACTION_STORAGE_MISMATCH")
	}
	return nil
}
func (s *Service) sessionTransaction(tx pgx.Tx) (transaction.Executor, error) {
	if tx == nil {
		return nil, failure("IDENTITY_TRANSACTION_REQUIRED")
	}
	q := transaction.PostgreSQLTransaction(tx)
	return q, s.sessionExecutor(q)
}
func sessionError(e error) error {
	if outcome, ok := transaction.CommitOutcome(e); ok {
		return failure(string(outcome))
	}
	var storage *transaction.Error
	if errors.As(e, &storage) && storage.Kind != transaction.CommitUnknown {
		return failure("STORAGE_UNAVAILABLE")
	}
	return e
}
func (s *Service) sessionContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, config.Seconds(s.Config.Policy().HTTP.RequestSeconds))
}

func (s *Service) sessionWorkspace(ctx context.Context, q transaction.Executor, user string) (*Workspace, error) {
	var w Workspace
	e := q.QueryRow(ctx, identitySQL(q,
		`SELECT w.id::text,w.tenant_id::text,w.name,m.role FROM installation_state i JOIN workspaces w ON w.tenant_id=i.tenant_id JOIN tenants t ON t.id=w.tenant_id JOIN workspace_memberships m ON m.workspace_id=w.id AND m.tenant_id=w.tenant_id WHERE i.bootstrap_complete AND m.user_id=$1 AND w.status='active' AND t.status='active' ORDER BY (w.id=i.default_workspace_id) DESC,w.created_at,w.id LIMIT 1`,
		`SELECT w.id,w.tenant_id,w.name,m.role FROM installation_state i JOIN workspaces w ON w.tenant_id=i.tenant_id JOIN tenants t ON t.id=w.tenant_id JOIN workspace_memberships m ON m.workspace_id=w.id AND m.tenant_id=w.tenant_id WHERE i.bootstrap_complete=1 AND m.user_id=? AND w.status='active' AND t.status='active' ORDER BY (w.id=i.default_workspace_id) DESC,w.created_at,w.id LIMIT 1`, `SELECT W.ID,W.TENANT_ID,W.NAME,M.ROLE FROM INSTALLATION_STATE I JOIN WORKSPACES W ON W.TENANT_ID=I.TENANT_ID JOIN TENANTS T ON T.ID=W.TENANT_ID JOIN WORKSPACE_MEMBERSHIPS M ON M.WORKSPACE_ID=W.ID AND M.TENANT_ID=W.TENANT_ID WHERE I.BOOTSTRAP_COMPLETE=1 AND M.USER_ID=? AND W.STATUS='active' AND T.STATUS='active' ORDER BY CASE WHEN W.ID=I.DEFAULT_WORKSPACE_ID THEN 1 ELSE 0 END DESC,W.CREATED_AT,W.ID LIMIT 1`, user)).Scan(&w.ID, &w.TenantID, &w.Name, &w.Role)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return &w, nil
}

// LockSessionUsersWithin resolves the browser user first,then acquires only exact
// user rows in canonical order. No source/installation lock precedes these locks.
func (s *Service) LockSessionUsersWithin(ctx context.Context, q transaction.Executor, user, previous, audience string) error {
	if !sessionID(user) || !sessionAudience(audience) {
		return failure("AUTH_INVALID_CREDENTIALS")
	}
	if e := s.sessionExecutor(q); e != nil {
		return e
	}
	ids := []string{user}
	if security.ValidToken(previous) {
		var old string
		e := q.QueryRow(ctx, identitySQL(q, "SELECT user_id::text FROM sessions WHERE token_hash=$1 AND audience=$2", "SELECT user_id FROM sessions WHERE token_hash=? AND audience=?", `SELECT USER_ID FROM SESSIONS WHERE TOKEN_HASH=? AND AUDIENCE=?`, security.Digest(previous), audience)).Scan(&old)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e == nil && old != user {
			ids = append(ids, old)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		var locked string
		e := q.QueryRow(ctx, identitySQL(q, "SELECT id::text FROM users WHERE id=$1 FOR NO KEY UPDATE", "SELECT id FROM users WHERE id=? FOR UPDATE", `SELECT ID FROM USERS WHERE ID=? FOR UPDATE`, id)).Scan(&locked)
		if errors.Is(e, sql.ErrNoRows) {
			return failure("AUTH_INVALID_CREDENTIALS")
		}
		if e != nil {
			return e
		}
	}
	return nil
}

func (s *Service) checkSourceWithin(ctx context.Context, q transaction.Executor, locked bool, id, user string) error {
	var tenant, connector, binding, flow *string
	var version *int64
	e := q.QueryRow(ctx, identitySQL(q,
		"SELECT sso_tenant_id::text,sso_connector_id::text,sso_binding_id::text,sso_auth_version,sso_flow_id::text FROM sessions WHERE id=$1 AND user_id=$2",
		"SELECT sso_tenant_id,sso_connector_id,sso_binding_id,sso_auth_version,sso_flow_id FROM sessions WHERE id=? AND user_id=?", `SELECT SSO_TENANT_ID,SSO_CONNECTOR_ID,SSO_BINDING_ID,SSO_AUTH_VERSION,SSO_FLOW_ID FROM SESSIONS WHERE ID=? AND USER_ID=?`, id, user)).Scan(&tenant, &connector, &binding, &version, &flow)
	if errors.Is(e, sql.ErrNoRows) {
		return failure("AUTH_SESSION_EXPIRED")
	}
	if e != nil {
		return e
	}
	if tenant == nil && connector == nil && binding == nil && version == nil && flow == nil {
		return nil
	}
	if tenant == nil || connector == nil || binding == nil || version == nil || flow == nil || s.Sources == nil {
		return failure("AUTH_SESSION_EXPIRED")
	}
	source := SessionSource{*tenant, *connector, *binding, *flow, *version}
	if !validSource(source) {
		return failure("AUTH_SESSION_EXPIRED")
	}
	if locked {
		return s.Sources.CheckSessionSourceWithin(ctx, q, source, user)
	}
	return s.Sources.CheckSessionSource(ctx, nil, source, user)
}

// IssueSessionWithin shares actual rotation,source claim,session and audit commit.
// The caller validates its credential/provider claim and owns the transaction.
func (s *Service) IssueSessionWithin(ctx context.Context, q transaction.Executor, user string, epoch int64, audience, previous, request string, source *SessionSource) (IssuedSession, error) {
	action := "auth.login_succeeded"
	if source != nil {
		action = "auth.sso_login_succeeded"
	}
	return s.issueSessionWithin(ctx, q, user, epoch, audience, previous, request, source, action)
}

// issueSessionWithin issues the session and records action as its audit event. An empty
// action leaves the audit to the caller, which records why the session was issued.
func (s *Service) issueSessionWithin(ctx context.Context, q transaction.Executor, user string, epoch int64, audience, previous, request string, source *SessionSource, action string) (IssuedSession, error) {
	return s.issueAuthorizedSessionWithin(ctx, q, user, epoch, audience, previous, request, source, action, false)
}

func (s *Service) issueAuthorizedSessionWithin(ctx context.Context, q transaction.Executor, user string, epoch int64, audience, previous, request string, source *SessionSource, action string, adminFactorChecked bool) (IssuedSession, error) {
	var out IssuedSession
	if epoch < 1 {
		return out, failure("AUTH_INVALID_CREDENTIALS")
	}
	if e := s.LockSessionUsersWithin(ctx, q, user, previous, audience); e != nil {
		return out, e
	}
	u, e := scanUser(q.QueryRow(ctx, identitySQL(q, "SELECT "+userColumns+" FROM users WHERE id=$1", "SELECT "+mysqlUserColumns+" FROM users WHERE id=?", `SELECT `+damengUserColumns+` FROM USERS WHERE ID=?`, user)))
	if e != nil {
		return out, e
	}
	if u.Status != "active" || u.Epoch != epoch || audience == "admin" && !u.IsPlatformAdmin {
		return out, failure("AUTH_INVALID_CREDENTIALS")
	}
	if audience == "admin" && !adminFactorChecked {
		factor, err := adminTOTPFactorWithin(ctx, q, user)
		if err != nil {
			return out, err
		}
		if factor.enabled {
			return out, failure("ACCESS_DENIED")
		}
	}
	if source != nil {
		if !validSource(*source) || s.Sources == nil {
			return out, failure("AUTH_SESSION_EXPIRED")
		}
		if e = s.Sources.CheckSessionSourceWithin(ctx, q, *source, user); e != nil {
			return out, e
		}
	}
	w, e := s.sessionWorkspace(ctx, q, user)
	if e != nil {
		return out, e
	}
	if security.ValidToken(previous) {
		if _, e = q.Exec(ctx, identitySQL(q, "UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='login_rotation' WHERE token_hash=$1 AND audience=$2 AND revoked_at IS NULL", "UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='login_rotation' WHERE token_hash=? AND audience=? AND revoked_at IS NULL", `UPDATE SESSIONS SET REVOKED_AT=CURRENT_TIMESTAMP(6),REVOKE_REASON='login_rotation' WHERE TOKEN_HASH=? AND AUDIENCE=? AND REVOKED_AT IS NULL`, security.Digest(previous), audience)); e != nil {
			return out, e
		}
	}
	out.Me, out.Token, out.SessionID = Me{u, w, audience}, security.Token(), uuid.NewString()
	var tenant, connector, binding, flow, version any
	if source != nil {
		tenant, connector, binding, flow, version = source.TenantID, source.ConnectorID, source.BindingID, source.FlowID, source.AuthVersion
	}
	args := []any{out.SessionID, user, audience, security.Digest(out.Token), epoch, s.Absolute(audience).Microseconds(), tenant, connector, binding, version, flow}
	n, e := q.Exec(ctx, identitySQL(q,
		`INSERT INTO sessions(id,user_id,audience,token_hash,auth_epoch,created_at,last_seen_at,absolute_expires_at,sso_tenant_id,sso_connector_id,sso_binding_id,sso_auth_version,sso_flow_id) VALUES($1,$2,$3,$4,$5,clock_timestamp(),clock_timestamp(),clock_timestamp()+$6::bigint*interval '1 microsecond',$7,$8,$9,$10,$11)`,
		`INSERT INTO sessions(id,user_id,audience,token_hash,auth_epoch,created_at,last_seen_at,absolute_expires_at,sso_tenant_id,sso_connector_id,sso_binding_id,sso_auth_version,sso_flow_id) VALUES(?,?,?,?,?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),TIMESTAMPADD(MICROSECOND,?,UTC_TIMESTAMP(6)),?,?,?,?,?)`, `INSERT INTO SESSIONS(ID,USER_ID,AUDIENCE,TOKEN_HASH,AUTH_EPOCH,CREATED_AT,LAST_SEEN_AT,ABSOLUTE_EXPIRES_AT,SSO_TENANT_ID,SSO_CONNECTOR_ID,SSO_BINDING_ID,SSO_AUTH_VERSION,SSO_FLOW_ID) VALUES(?,?,?,?,?,CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)+NUMTODSINTERVAL(?/1000000.0,'SECOND'),?,?,?,?,?)`, args...))
	if e != nil {
		return IssuedSession{}, e
	}
	if n != 1 {
		return IssuedSession{}, failure("IDENTITY_WRITE_UNCONFIRMED")
	}
	if e = q.QueryRow(ctx, identitySQL(q, "SELECT absolute_expires_at FROM sessions WHERE id=$1 AND user_id=$2", "SELECT absolute_expires_at FROM sessions WHERE id=? AND user_id=?", `SELECT ABSOLUTE_EXPIRES_AT FROM SESSIONS WHERE ID=? AND USER_ID=?`, out.SessionID, user)).Scan(&out.ExpiresAt); e != nil {
		return IssuedSession{}, e
	}
	if action == "" {
		return out, nil
	}
	if e = audit.WriteWithin(ctx, q, user, user, audience, action, "success", "user", user, "", request); e != nil {
		return IssuedSession{}, e
	}
	return out, nil
}

// RecheckWithin locks user -> current source -> exact session before business
// writes. Callers with additional users/connectors must lock their complete sets
// in canonical order first; repeat rechecks retain those same authority fences.
func (s *Service) RecheckWithin(ctx context.Context, q transaction.Executor, a Auth) error {
	return s.recheckWithin(ctx, q, a, true)
}

// CurrentWithin is RecheckWithin without the user and session locks on PostgreSQL, for an
// agent call that acts on a person's grant: every node working for that person shares
// those rows (ADR 0020). A call in flight when a revocation commits ends as if it arrived
// just before it; the next call fails. The other chains keep the locks.
func (s *Service) CurrentWithin(ctx context.Context, q transaction.Executor, a Auth) error {
	if q == nil {
		return failure("AUTH_SESSION_EXPIRED")
	}
	return s.recheckWithin(ctx, q, a, q.Dialect() != transaction.PostgreSQL)
}

func (s *Service) recheckWithin(ctx context.Context, q transaction.Executor, a Auth, lock bool) error {
	if ctx == nil || !sessionActor(a) {
		return failure("AUTH_SESSION_EXPIRED")
	}
	if e := s.sessionExecutor(q); e != nil {
		return e
	}
	var epoch int64
	var status string
	var admin bool
	userLock, sessionLock := "", ""
	if lock {
		userLock, sessionLock = " FOR NO KEY UPDATE", " FOR UPDATE"
	}
	e := q.QueryRow(ctx, identitySQL(q, "SELECT auth_epoch,status,is_platform_admin FROM users WHERE id=$1"+userLock, "SELECT auth_epoch,status,is_platform_admin FROM users WHERE id=? FOR UPDATE", `SELECT AUTH_EPOCH,STATUS,IS_PLATFORM_ADMIN FROM USERS WHERE ID=? FOR UPDATE`, a.Me.User.ID)).Scan(&epoch, &status, &admin)
	if errors.Is(e, sql.ErrNoRows) {
		return failure("AUTH_SESSION_EXPIRED")
	}
	if e != nil {
		return e
	}
	if epoch != a.Epoch || status != "active" || a.Me.Audience == "admin" && !admin {
		return failure("AUTH_SESSION_EXPIRED")
	}
	// A connector revoker owns its connector before bulk-locking sessions.
	// Inspect immutable source fields without a session lock, then acquire the
	// source fence before the exact live-session predicate below. Failed source
	// checks stop here; no SQL follows a poisoned native statement.
	if e := s.checkSourceWithin(ctx, q, true, a.SessionID, a.Me.User.ID); e != nil {
		return e
	}
	var locked string
	e = q.QueryRow(ctx, identitySQL(q,
		`SELECT id::text FROM sessions WHERE id=$1 AND user_id=$2 AND token_hash=$3 AND auth_epoch=$4 AND audience=$5 AND revoked_at IS NULL AND absolute_expires_at>clock_timestamp() AND last_seen_at>clock_timestamp()-$6::bigint*interval '1 microsecond'`+sessionLock,
		`SELECT id FROM sessions WHERE id=? AND user_id=? AND token_hash=? AND auth_epoch=? AND audience=? AND revoked_at IS NULL AND absolute_expires_at>UTC_TIMESTAMP(6) AND last_seen_at>TIMESTAMPADD(MICROSECOND,-?,UTC_TIMESTAMP(6)) FOR UPDATE`, `SELECT ID FROM SESSIONS WHERE ID=? AND USER_ID=? AND TOKEN_HASH=? AND AUTH_EPOCH=? AND AUDIENCE=? AND REVOKED_AT IS NULL AND ABSOLUTE_EXPIRES_AT>CURRENT_TIMESTAMP(6) AND LAST_SEEN_AT>CURRENT_TIMESTAMP(6)-NUMTODSINTERVAL(?/1000000.0,'SECOND') FOR UPDATE`, a.SessionID, a.Me.User.ID, a.TokenHash, a.Epoch, a.Me.Audience, s.Idle(a.Me.Audience).Microseconds())).Scan(&locked)
	if errors.Is(e, sql.ErrNoRows) {
		return failure("AUTH_SESSION_EXPIRED")
	}
	if e != nil {
		return e
	}
	return nil
}

func (s *Service) ConfirmPassword(ctx context.Context, a Auth, password string) (PasswordConfirmation, error) {
	var proof PasswordConfirmation
	if !sessionActor(a) || len(password) == 0 || len(password) > maximumConfirmationPasswordBytes {
		return proof, failure("SSO_PASSWORD_REQUIRED")
	}
	store, e := s.Repository()
	if e != nil {
		return proof, e
	}
	if s.Passwords == nil {
		return proof, failure("IDENTITY_PASSWORD_CONFIGURATION_REQUIRED")
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	var phc string
	var version int64
	e = store.QueryRow(ctx, identitySQL(store, "SELECT c.password_phc,c.version FROM password_credentials c JOIN users u ON u.id=c.user_id WHERE c.user_id=$1 AND u.status='active' AND u.auth_epoch=$2", "SELECT c.password_phc,c.version FROM password_credentials c JOIN users u ON u.id=c.user_id WHERE c.user_id=? AND u.status='active' AND u.auth_epoch=?", `SELECT C.PASSWORD_PHC,C.VERSION FROM PASSWORD_CREDENTIALS C JOIN USERS U ON U.ID=C.USER_ID WHERE C.USER_ID=? AND U.STATUS='active' AND U.AUTH_EPOCH=?`, a.Me.User.ID, a.Epoch)).Scan(&phc, &version)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return proof, sessionError(e)
	}
	eligible := e == nil
	if !eligible {
		phc = s.Passwords.Dummy
	}
	ok, e := s.Passwords.Verify(ctx, phc, password)
	if e != nil {
		return proof, e
	}
	if !eligible || !ok {
		return proof, failure("SSO_PASSWORD_REQUIRED")
	}
	return PasswordConfirmation{a.Me.User.ID, a.SessionID, a.Me.Audience, append([]byte(nil), a.TokenHash...), a.Epoch, version}, nil
}
func (s *Service) RecheckConfirmationWithin(ctx context.Context, q transaction.Executor, a Auth, proof PasswordConfirmation) error {
	if proof.user != a.Me.User.ID || proof.session != a.SessionID || proof.audience != a.Me.Audience || proof.epoch != a.Epoch || len(proof.tokenHash) != sha256.Size || subtle.ConstantTimeCompare(proof.tokenHash, a.TokenHash) != 1 {
		return failure("SSO_PASSWORD_REQUIRED")
	}
	if e := s.RecheckWithin(ctx, q, a); e != nil {
		return e
	}
	return s.RecheckCredentialVersionWithin(ctx, q, a, proof.version)
}
func (s *Service) RecheckCredentialVersionWithin(ctx context.Context, q transaction.Executor, a Auth, version int64) error {
	if !sessionActor(a) {
		return failure("AUTH_SESSION_EXPIRED")
	}
	if e := s.sessionExecutor(q); e != nil {
		return e
	}
	var current int64
	e := q.QueryRow(ctx, identitySQL(q, "SELECT version FROM password_credentials WHERE user_id=$1", "SELECT version FROM password_credentials WHERE user_id=?", `SELECT VERSION FROM PASSWORD_CREDENTIALS WHERE USER_ID=?`, a.Me.User.ID)).Scan(&current)
	if errors.Is(e, sql.ErrNoRows) {
		return failure("SSO_PASSWORD_CHANGED")
	}
	if e != nil {
		return e
	}
	if version < 1 || current != version {
		return failure("SSO_PASSWORD_CHANGED")
	}
	return nil
}
