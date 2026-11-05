package identity

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func (s *Service) Authenticate(ctx context.Context, token, audience string) (Auth, error) {
	var a Auth
	if !security.ValidToken(token) || !sessionAudience(audience) {
		return a, failure("AUTH_REQUIRED")
	}
	store, e := s.Repository()
	if e != nil {
		return a, e
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	var u User
	// One statement observes session eligibility and its current user consistently.
	e = store.QueryRow(ctx, identitySQL(store,
		`SELECT se.id::text,se.auth_epoch,u.id::text,u.email_display,u.display_name,u.locale,u.status,u.is_platform_admin,u.email_verified_at,u.version,u.created_at,u.updated_at,u.auth_epoch FROM sessions se JOIN users u ON u.id=se.user_id WHERE se.token_hash=$1 AND se.audience=$2 AND se.revoked_at IS NULL AND se.absolute_expires_at>clock_timestamp() AND se.last_seen_at>clock_timestamp()-$3::bigint*interval '1 microsecond' AND u.status='active' AND u.auth_epoch=se.auth_epoch AND (se.audience='portal' OR u.is_platform_admin)`,
		`SELECT se.id,se.auth_epoch,u.id,u.email_display,u.display_name,u.locale,u.status,u.is_platform_admin,u.email_verified_at,u.version,u.created_at,u.updated_at,u.auth_epoch FROM sessions se JOIN users u ON u.id=se.user_id WHERE se.token_hash=? AND se.audience=? AND se.revoked_at IS NULL AND se.absolute_expires_at>UTC_TIMESTAMP(6) AND se.last_seen_at>TIMESTAMPADD(MICROSECOND,-?,UTC_TIMESTAMP(6)) AND u.status='active' AND u.auth_epoch=se.auth_epoch AND (se.audience='portal' OR u.is_platform_admin=1)`, `SELECT SE.ID,SE.AUTH_EPOCH,U.ID,U.EMAIL_DISPLAY,U.DISPLAY_NAME,U.LOCALE,U.STATUS,U.IS_PLATFORM_ADMIN,U.EMAIL_VERIFIED_AT,U.VERSION,U.CREATED_AT,U.UPDATED_AT,U.AUTH_EPOCH FROM SESSIONS SE JOIN USERS U ON U.ID=SE.USER_ID WHERE SE.TOKEN_HASH=? AND SE.AUDIENCE=? AND SE.REVOKED_AT IS NULL AND SE.ABSOLUTE_EXPIRES_AT>CURRENT_TIMESTAMP(6) AND SE.LAST_SEEN_AT>CURRENT_TIMESTAMP(6)-NUMTODSINTERVAL(?/1000000.0,'SECOND') AND U.STATUS='active' AND U.AUTH_EPOCH=SE.AUTH_EPOCH AND (SE.AUDIENCE='portal' OR U.IS_PLATFORM_ADMIN=1)`, security.Digest(token), audience, s.Idle(audience).Microseconds())).Scan(&a.SessionID, &a.Epoch, &u.ID, &u.Email, &u.DisplayName, &u.Locale, &u.Status, &u.IsPlatformAdmin, &u.EmailVerifiedAt, &u.Version, &u.CreatedAt, &u.UpdatedAt, &u.Epoch)
	if errors.Is(e, sql.ErrNoRows) {
		return Auth{}, failure("AUTH_SESSION_EXPIRED")
	}
	if e != nil {
		return Auth{}, sessionError(e)
	}
	if e = s.checkSourceWithin(ctx, store, false, a.SessionID, u.ID); e != nil {
		return Auth{}, sessionError(e)
	}
	w, e := s.sessionWorkspace(ctx, store, u.ID)
	if e != nil {
		return Auth{}, sessionError(e)
	}
	a.Me, a.TokenHash = Me{u, w, audience}, security.Digest(token)
	_, e = store.Exec(ctx, identitySQL(store,
		`UPDATE sessions SET last_seen_at=clock_timestamp() WHERE id=$1 AND user_id=$2 AND token_hash=$3 AND audience=$4 AND auth_epoch=$5 AND revoked_at IS NULL AND absolute_expires_at>clock_timestamp() AND last_seen_at>clock_timestamp()-$6::bigint*interval '1 microsecond' AND last_seen_at<clock_timestamp()-$7::bigint*interval '1 microsecond'`,
		`UPDATE sessions SET last_seen_at=UTC_TIMESTAMP(6) WHERE id=? AND user_id=? AND token_hash=? AND audience=? AND auth_epoch=? AND revoked_at IS NULL AND absolute_expires_at>UTC_TIMESTAMP(6) AND last_seen_at>TIMESTAMPADD(MICROSECOND,-?,UTC_TIMESTAMP(6)) AND last_seen_at<TIMESTAMPADD(MICROSECOND,-?,UTC_TIMESTAMP(6))`, `UPDATE SESSIONS SET LAST_SEEN_AT=CURRENT_TIMESTAMP(6) WHERE ID=? AND USER_ID=? AND TOKEN_HASH=? AND AUDIENCE=? AND AUTH_EPOCH=? AND REVOKED_AT IS NULL AND ABSOLUTE_EXPIRES_AT>CURRENT_TIMESTAMP(6) AND LAST_SEEN_AT>CURRENT_TIMESTAMP(6)-NUMTODSINTERVAL(?/1000000.0,'SECOND') AND LAST_SEEN_AT<CURRENT_TIMESTAMP(6)-NUMTODSINTERVAL(?/1000000.0,'SECOND')`, a.SessionID, u.ID, a.TokenHash, audience, a.Epoch, s.Idle(audience).Microseconds(), config.Seconds(s.Config.Policy().Identity.SessionTouchSeconds).Microseconds()))
	if e != nil {
		return Auth{}, sessionError(e)
	}
	return a, nil
}

func (s *Service) Login(ctx context.Context, email, password, audience, previous, request string) (Me, string, time.Time, error) {
	var out Me
	if !sessionAudience(audience) {
		return out, "", time.Time{}, failure("AUTH_INVALID_CREDENTIALS")
	}
	store, e := s.Repository()
	if e != nil {
		return out, "", time.Time{}, e
	}
	if s.Passwords == nil || len(s.HMACKey) < security.MinimumHMACKeyBytes || len(s.HMACKey) > security.MaximumHMACKeyBytes {
		return out, "", time.Time{}, failure("IDENTITY_LOGIN_CONFIGURATION_REQUIRED")
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	canonical, _, e := security.Email(email)
	if e != nil {
		return out, "", time.Time{}, e
	}
	var id, phc, status string
	var credentialVersion, epoch int64
	var admin bool
	e = store.QueryRow(ctx, identitySQL(store,
		`SELECT u.id::text,c.password_phc,c.version,u.auth_epoch,u.status,u.is_platform_admin FROM users u JOIN password_credentials c ON c.user_id=u.id WHERE u.email_canonical=$1`,
		`SELECT u.id,c.password_phc,c.version,u.auth_epoch,u.status,u.is_platform_admin FROM users u JOIN password_credentials c ON c.user_id=u.id WHERE u.email_canonical=?`, `SELECT U.ID,C.PASSWORD_PHC,C.VERSION,U.AUTH_EPOCH,U.STATUS,U.IS_PLATFORM_ADMIN FROM USERS U JOIN PASSWORD_CREDENTIALS C ON C.USER_ID=U.ID WHERE U.EMAIL_CANONICAL=?`, canonical)).Scan(&id, &phc, &credentialVersion, &epoch, &status, &admin)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, "", time.Time{}, sessionError(e)
	}
	eligible := e == nil && status == "active" && (audience == "portal" || admin)
	if !eligible {
		phc = s.Passwords.Dummy
	}
	ok, e := s.Passwords.Verify(ctx, phc, password)
	if e != nil {
		return out, "", time.Time{}, verifyError(e)
	}
	if !eligible || !ok {
		e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			return audit.WriteAnonymousWithin(ctx, tx, audience, "auth.login_failed", "failure", request, security.MAC(s.HMACKey, "m01-audit", canonical))
		})
		if e != nil {
			return out, "", time.Time{}, sessionError(e)
		}
		return out, "", time.Time{}, failure("AUTH_INVALID_CREDENTIALS")
	}
	var issued IssuedSession
	e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		if e := s.LockSessionUsersWithin(ctx, tx, id, previous, audience); e != nil {
			return e
		}
		u, e := scanUser(tx.QueryRow(ctx, identitySQL(tx, "SELECT "+userColumns+" FROM users WHERE id=$1", "SELECT "+mysqlUserColumns+" FROM users WHERE id=?", `SELECT `+damengUserColumns+` FROM USERS WHERE ID=?`, id)))
		if e != nil {
			return e
		}
		var current string
		var version int64
		if e = tx.QueryRow(ctx, identitySQL(tx, "SELECT password_phc,version FROM password_credentials WHERE user_id=$1", "SELECT password_phc,version FROM password_credentials WHERE user_id=?", `SELECT PASSWORD_PHC,VERSION FROM PASSWORD_CREDENTIALS WHERE USER_ID=?`, id)).Scan(&current, &version); e != nil {
			return e
		}
		if current != phc || version != credentialVersion || u.Epoch != epoch || u.Status != "active" || audience == "admin" && !u.IsPlatformAdmin {
			return failure("AUTH_INVALID_CREDENTIALS")
		}
		issued, e = s.IssueSessionWithin(ctx, tx, id, epoch, audience, previous, request, nil)
		return e
	})
	if e != nil {
		return out, "", time.Time{}, sessionError(e)
	}
	return issued.Me, issued.Token, issued.ExpiresAt, nil
}

func (s *Service) Logout(ctx context.Context, token, audience, request string) error {
	if !security.ValidToken(token) {
		return nil
	}
	if !sessionAudience(audience) {
		return failure("AUTH_REQUIRED")
	}
	store, e := s.Repository()
	if e != nil {
		return e
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		var id, user string
		e := tx.QueryRow(ctx, identitySQL(tx, "SELECT id::text,user_id::text FROM sessions WHERE token_hash=$1 AND audience=$2", "SELECT id,user_id FROM sessions WHERE token_hash=? AND audience=?", `SELECT ID,USER_ID FROM SESSIONS WHERE TOKEN_HASH=? AND AUDIENCE=?`, security.Digest(token), audience)).Scan(&id, &user)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		var locked string
		if e = tx.QueryRow(ctx, identitySQL(tx, "SELECT id::text FROM users WHERE id=$1 FOR UPDATE", "SELECT id FROM users WHERE id=? FOR UPDATE", `SELECT ID FROM USERS WHERE ID=? FOR UPDATE`, user)).Scan(&locked); e != nil {
			return e
		}
		n, e := tx.Exec(ctx, identitySQL(tx, "UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='logout' WHERE id=$1 AND token_hash=$2 AND audience=$3 AND revoked_at IS NULL", "UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='logout' WHERE id=? AND token_hash=? AND audience=? AND revoked_at IS NULL", `UPDATE SESSIONS SET REVOKED_AT=CURRENT_TIMESTAMP(6),REVOKE_REASON='logout' WHERE ID=? AND TOKEN_HASH=? AND AUDIENCE=? AND REVOKED_AT IS NULL`, id, security.Digest(token), audience))
		if e != nil {
			return e
		}
		if n == 0 {
			return nil
		}
		return audit.WriteWithin(ctx, tx, user, user, audience, "auth.logout", "success", "session", id, "", request)
	})
	return sessionError(e)
}
