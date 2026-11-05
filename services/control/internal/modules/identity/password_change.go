package identity

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
)

// passwordChangeRevokeReason marks the sessions a password change ended.
const passwordChangeRevokeReason = "password_changed"

// ChangePassword replaces the signed-in account's password once the current one checks
// out (ADR 0023). Every session of the account ends and unused reset links stop working;
// the calling browser receives a new session, so the person stays signed in only here.
// The account is told by email. A wrong current password fails with
// PASSWORD_CURRENT_INVALID and is audited; the new one must pass the password policy and
// differ from the current one (PASSWORD_UNCHANGED).
func (s *Service) ChangePassword(ctx context.Context, a Auth, current, next, request string) (IssuedSession, error) {
	var out IssuedSession
	if !sessionActor(a) {
		return out, failure("AUTH_SESSION_EXPIRED")
	}
	if !passwordInput(current) || !passwordInput(next) {
		return out, failure("VALIDATION_FAILED")
	}
	store, e := s.recoveryStore(true)
	if e != nil {
		return out, e
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	var phc, email, locale string
	var version int64
	e = store.QueryRow(ctx, identitySQL(store,
		"SELECT c.password_phc,c.version,u.email_canonical,u.locale FROM password_credentials c JOIN users u ON u.id=c.user_id WHERE c.user_id=$1 AND u.status='active' AND u.auth_epoch=$2",
		"SELECT c.password_phc,c.version,u.email_canonical,u.locale FROM password_credentials c JOIN users u ON u.id=c.user_id WHERE c.user_id=? AND u.status='active' AND u.auth_epoch=?",
		`SELECT C.PASSWORD_PHC,C.VERSION,U.EMAIL_CANONICAL,U.LOCALE FROM PASSWORD_CREDENTIALS C JOIN USERS U ON U.ID=C.USER_ID WHERE C.USER_ID=? AND U.STATUS='active' AND U.AUTH_EPOCH=?`,
		a.Me.User.ID, a.Epoch)).Scan(&phc, &version, &email, &locale)
	if errors.Is(e, sql.ErrNoRows) {
		return out, failure("AUTH_SESSION_EXPIRED")
	}
	if e != nil {
		return out, sessionError(e)
	}
	ok, e := s.Passwords.Verify(ctx, phc, current)
	if e != nil {
		return out, verifyError(e)
	}
	if !ok {
		e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			return audit.WriteWithin(ctx, tx, a.Me.User.ID, a.Me.User.ID, a.Me.Audience, "auth.password_change_failed", "failure", "user", a.Me.User.ID, "", request)
		})
		if e != nil {
			return out, sessionError(e)
		}
		return out, failure("PASSWORD_CURRENT_INVALID")
	}
	next, e = s.passwordPolicy(ctx, next, email)
	if e != nil {
		return out, e
	}
	same, e := s.Passwords.Verify(ctx, phc, next)
	if e != nil {
		return out, verifyError(e)
	}
	if same {
		return out, failure("PASSWORD_UNCHANGED")
	}
	replacement, e := s.Passwords.Hash(ctx, next)
	if e != nil {
		return out, e
	}
	e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		if e := s.RecheckWithin(ctx, tx, a); e != nil {
			return e
		}
		if e := s.RecheckCredentialVersionWithin(ctx, tx, a, version); e != nil {
			// A concurrent change or reset already replaced the password this request checked.
			if code(e) == "SSO_PASSWORD_CHANGED" {
				return failure("PASSWORD_CURRENT_INVALID")
			}
			return e
		}
		if e := updateRecoveryCredential(ctx, tx, a.Me.User.ID, replacement); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, identitySQL(tx,
			"UPDATE users SET auth_epoch=auth_epoch+1,version=version+1,updated_at=clock_timestamp() WHERE id=$1",
			"UPDATE users SET auth_epoch=auth_epoch+1,version=version+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?",
			`UPDATE USERS SET AUTH_EPOCH=AUTH_EPOCH+1,VERSION=VERSION+1,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE ID=?`, a.Me.User.ID)); e != nil {
			return e
		}
		if e := revokeAuthorityWithin(ctx, tx, a.Me.User.ID, passwordChangeRevokeReason); e != nil {
			return e
		}
		issued, e := s.issueSessionWithin(ctx, tx, a.Me.User.ID, a.Epoch+1, a.Me.Audience, "", request, nil, "")
		if e != nil {
			return e
		}
		if _, e = s.Mail.EnqueueWithin(ctx, tx, mail.Input{Purpose: "password_changed", Recipient: email, Locale: locale, UserID: a.Me.User.ID, Audience: a.Me.Audience}); e != nil {
			return e
		}
		if e = audit.WriteWithin(ctx, tx, a.Me.User.ID, a.Me.User.ID, a.Me.Audience, "auth.password_changed", "success", "user", a.Me.User.ID, "", request); e != nil {
			return e
		}
		out = issued
		return nil
	})
	if e != nil {
		return IssuedSession{}, sessionError(e)
	}
	return out, nil
}

// passwordInput bounds a password as it arrives, before the policy normalizes it.
func passwordInput(value string) bool {
	return len(value) > 0 && len(value) <= security.MaximumPasswordInputBytes
}

// verifyError keeps hashing capacity visible to the caller and hides everything else.
func verifyError(e error) error {
	if code(e) == "AUTH_CAPACITY_EXCEEDED" {
		return e
	}
	return failure("INTERNAL_ERROR")
}
