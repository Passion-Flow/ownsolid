package identity

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
)

const maximumOperatorReasonRunes = 500

func recoveryReason(value string) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= maximumOperatorReasonRunes
}
func (s *Service) recoveryStore(outbox bool) (transaction.Store, error) {
	store, e := s.Repository()
	if e != nil {
		return nil, e
	}
	if s.Passwords == nil {
		return nil, failure("IDENTITY_PASSWORD_CONFIGURATION_REQUIRED")
	}
	if outbox && (s.Mail == nil || s.Mail.Keys == nil || len(s.HMACKey) < security.MinimumHMACKeyBytes || len(s.HMACKey) > security.MaximumHMACKeyBytes) {
		return nil, failure("IDENTITY_RECOVERY_CONFIGURATION_REQUIRED")
	}
	return store, nil
}

func (s *Service) RequestReset(ctx context.Context, email, audience, ip, request string) error {
	if !sessionAudience(audience) {
		return failure("VALIDATION_FAILED")
	}
	store, e := s.recoveryStore(true)
	if e != nil {
		return e
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	start, policy := time.Now(), s.Config.Policy()
	canonical, _, e := security.Email(email)
	if e != nil {
		return e
	}
	quiet, e := s.Admit(ctx, Limit{"reset.ip." + audience, ip, policy.Rates.ResetIPCount, config.Seconds(policy.Rates.ResetIPWindowSeconds), 0, false}, Limit{"reset.email", canonical, policy.Rates.ResetEmailCount, config.Seconds(policy.Rates.ResetEmailWindowSeconds), config.Seconds(policy.Rates.ResetCooldownSeconds), true})
	if e != nil {
		return e
	}
	e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		var user, locale string
		e := tx.QueryRow(ctx, identitySQL(tx,
			`SELECT id::text,locale FROM users WHERE email_canonical=$1 AND status='active' AND ($2='portal' OR is_platform_admin) FOR UPDATE`,
			`SELECT id,locale FROM users WHERE email_canonical=? AND status='active' AND (?='portal' OR is_platform_admin=1) FOR UPDATE`, `SELECT ID,LOCALE FROM USERS WHERE EMAIL_CANONICAL=? AND STATUS='active' AND (?='portal' OR IS_PLATFORM_ADMIN=1) FOR UPDATE`, canonical, audience)).Scan(&user, &locale)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e == nil && !quiet {
			var count int
			if e = tx.QueryRow(ctx, identitySQL(tx, "SELECT count(*) FROM password_reset_tokens WHERE user_id=$1 AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>clock_timestamp()", "SELECT count(*) FROM password_reset_tokens WHERE user_id=? AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>UTC_TIMESTAMP(6)", `SELECT COUNT(*) FROM PASSWORD_RESET_TOKENS WHERE USER_ID=? AND CONSUMED_AT IS NULL AND REVOKED_AT IS NULL AND EXPIRES_AT>CURRENT_TIMESTAMP(6)`, user)).Scan(&count); e != nil {
				return e
			}
			if count < policy.Identity.MaxLiveResetTokens {
				id, token := uuid.NewString(), security.Token()
				if _, e = tx.Exec(ctx, identitySQL(tx,
					`INSERT INTO password_reset_tokens(id,user_id,audience,token_hash,created_at,expires_at) VALUES($1,$2,$3,$4,clock_timestamp(),clock_timestamp()+$5::bigint*interval '1 second')`,
					`INSERT INTO password_reset_tokens(id,user_id,audience,token_hash,created_at,expires_at) VALUES(?,?,?,?,UTC_TIMESTAMP(6),TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)))`, `INSERT INTO PASSWORD_RESET_TOKENS(ID,USER_ID,AUDIENCE,TOKEN_HASH,CREATED_AT,EXPIRES_AT) VALUES(?,?,?,?,CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)+NUMTODSINTERVAL(?,'SECOND'))`, id, user, audience, security.Digest(token), policy.Identity.ResetTTLSeconds)); e != nil {
					return e
				}
				if _, e = s.Mail.EnqueueWithin(ctx, tx, mail.Input{Purpose: "password_reset", Recipient: canonical, Locale: locale, ResetID: id, UserID: user, Token: token, Audience: audience}); e != nil {
					return e
				}
			}
		}
		return audit.WriteAnonymousWithin(ctx, tx, audience, "auth.recovery_requested", "accepted", request, security.MAC(s.HMACKey, "m01-audit", canonical))
	})
	if e != nil {
		return sessionError(e)
	}
	jitter, e := rand.Int(rand.Reader, big.NewInt(int64(policy.Identity.ResetJitterMilliseconds)+1))
	if e != nil {
		return failure("INTERNAL_ERROR")
	}
	timer := time.NewTimer(time.Until(start.Add(config.Milliseconds(policy.Identity.ResetMinimumMilliseconds) + time.Duration(jitter.Int64())*time.Millisecond)))
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// revokeAuthorityWithin requires the caller's prior user write fence.
func revokeAuthorityWithin(ctx context.Context, tx transaction.Executor, user, reason string) error {
	if tx == nil || !sessionID(user) {
		return failure("IDENTITY_AUTHORITY_CONTEXT_REQUIRED")
	}
	// MySQL placeholders are positional;do not reuse the PostgreSQL argument order.
	query := identitySQL(tx, "UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason=$2 WHERE user_id=$1 AND revoked_at IS NULL", "", "", user, reason)
	if tx.Dialect() == transaction.MySQL {
		query = transaction.SQL(transaction.MySQL, "UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason=? WHERE user_id=? AND revoked_at IS NULL", reason, user)
	} else if tx.Dialect() == transaction.Dameng {
		query = transaction.SQL(transaction.Dameng, "UPDATE SESSIONS SET REVOKED_AT=CURRENT_TIMESTAMP(6),REVOKE_REASON=? WHERE USER_ID=? AND REVOKED_AT IS NULL", reason, user)
	}
	if _, e := tx.Exec(ctx, query); e != nil {
		return e
	}
	if _, e := tx.Exec(ctx, identitySQL(tx, "UPDATE password_reset_tokens SET token_hash=NULL,revoked_at=clock_timestamp() WHERE user_id=$1 AND consumed_at IS NULL AND revoked_at IS NULL", "UPDATE password_reset_tokens SET token_hash=NULL,revoked_at=UTC_TIMESTAMP(6) WHERE user_id=? AND consumed_at IS NULL AND revoked_at IS NULL", `UPDATE PASSWORD_RESET_TOKENS SET TOKEN_HASH=NULL,REVOKED_AT=CURRENT_TIMESTAMP(6) WHERE USER_ID=? AND CONSUMED_AT IS NULL AND REVOKED_AT IS NULL`, user)); e != nil {
		return e
	}
	return mail.CancelUserResetWithin(ctx, tx, user)
}

func (s *Service) Reset(ctx context.Context, token, password, audience, request string) error {
	if !security.ValidToken(token) || !sessionAudience(audience) {
		return failure("TOKEN_UNUSABLE")
	}
	store, e := s.recoveryStore(true)
	if e != nil {
		return e
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	var user, email string
	e = store.QueryRow(ctx, identitySQL(store,
		`SELECT t.user_id::text,u.email_canonical FROM password_reset_tokens t JOIN users u ON u.id=t.user_id WHERE t.token_hash=$1 AND t.audience=$2 AND t.consumed_at IS NULL AND t.revoked_at IS NULL AND t.expires_at>clock_timestamp() AND u.status='active'`,
		`SELECT t.user_id,u.email_canonical FROM password_reset_tokens t JOIN users u ON u.id=t.user_id WHERE t.token_hash=? AND t.audience=? AND t.consumed_at IS NULL AND t.revoked_at IS NULL AND t.expires_at>UTC_TIMESTAMP(6) AND u.status='active'`, `SELECT T.USER_ID,U.EMAIL_CANONICAL FROM PASSWORD_RESET_TOKENS T JOIN USERS U ON U.ID=T.USER_ID WHERE T.TOKEN_HASH=? AND T.AUDIENCE=? AND T.CONSUMED_AT IS NULL AND T.REVOKED_AT IS NULL AND T.EXPIRES_AT>CURRENT_TIMESTAMP(6) AND U.STATUS='active'`, security.Digest(token), audience)).Scan(&user, &email)
	if errors.Is(e, sql.ErrNoRows) {
		return failure("TOKEN_UNUSABLE")
	}
	if e != nil {
		return sessionError(e)
	}
	password, e = s.passwordPolicy(ctx, password, email)
	if e != nil {
		return e
	}
	phc, e := s.Passwords.Hash(ctx, password)
	if e != nil {
		return e
	}
	e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		u, e := scanUser(tx.QueryRow(ctx, identitySQL(tx, "SELECT "+userColumns+" FROM users WHERE id=$1 FOR UPDATE", "SELECT "+mysqlUserColumns+" FROM users WHERE id=? FOR UPDATE", `SELECT `+damengUserColumns+` FROM USERS WHERE ID=? FOR UPDATE`, user)))
		if errors.Is(e, sql.ErrNoRows) {
			return failure("TOKEN_UNUSABLE")
		}
		if e != nil {
			return e
		}
		if u.Status != "active" {
			return failure("TOKEN_UNUSABLE")
		}
		var id string
		e = tx.QueryRow(ctx, identitySQL(tx,
			`SELECT id::text FROM password_reset_tokens WHERE user_id=$1 AND token_hash=$2 AND audience=$3 AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>clock_timestamp() FOR UPDATE`,
			`SELECT id FROM password_reset_tokens WHERE user_id=? AND token_hash=? AND audience=? AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>UTC_TIMESTAMP(6) FOR UPDATE`, `SELECT ID FROM PASSWORD_RESET_TOKENS WHERE USER_ID=? AND TOKEN_HASH=? AND AUDIENCE=? AND CONSUMED_AT IS NULL AND REVOKED_AT IS NULL AND EXPIRES_AT>CURRENT_TIMESTAMP(6) FOR UPDATE`, user, security.Digest(token), audience)).Scan(&id)
		if errors.Is(e, sql.ErrNoRows) {
			return failure("TOKEN_UNUSABLE")
		}
		if e != nil {
			return e
		}
		var valid bool
		if e = tx.QueryRow(ctx, identitySQL(tx, "SELECT expires_at>clock_timestamp() FROM password_reset_tokens WHERE id=$1", "SELECT expires_at>UTC_TIMESTAMP(6) FROM password_reset_tokens WHERE id=?", `SELECT CASE WHEN EXPIRES_AT>CURRENT_TIMESTAMP(6) THEN 1 ELSE 0 END FROM PASSWORD_RESET_TOKENS WHERE ID=?`, id)).Scan(&valid); e != nil {
			return e
		}
		if !valid {
			return failure("TOKEN_UNUSABLE")
		}
		if e = updateRecoveryCredential(ctx, tx, user, phc); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, identitySQL(tx, "UPDATE users SET auth_epoch=auth_epoch+1,version=version+1,updated_at=clock_timestamp(),email_verified_at=COALESCE(email_verified_at,clock_timestamp()) WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1,version=version+1,updated_at=UTC_TIMESTAMP(6),email_verified_at=COALESCE(email_verified_at,UTC_TIMESTAMP(6)) WHERE id=?", `UPDATE USERS SET AUTH_EPOCH=AUTH_EPOCH+1,VERSION=VERSION+1,UPDATED_AT=CURRENT_TIMESTAMP(6),EMAIL_VERIFIED_AT=COALESCE(EMAIL_VERIFIED_AT,CURRENT_TIMESTAMP(6)) WHERE ID=?`, user)); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, identitySQL(tx, "UPDATE password_reset_tokens SET token_hash=NULL,consumed_at=clock_timestamp() WHERE id=$1", "UPDATE password_reset_tokens SET token_hash=NULL,consumed_at=UTC_TIMESTAMP(6) WHERE id=?", `UPDATE PASSWORD_RESET_TOKENS SET TOKEN_HASH=NULL,CONSUMED_AT=CURRENT_TIMESTAMP(6) WHERE ID=?`, id)); e != nil {
			return e
		}
		if e = revokeAuthorityWithin(ctx, tx, user, "password_reset"); e != nil {
			return e
		}
		if _, e = s.Mail.EnqueueWithin(ctx, tx, mail.Input{Purpose: "password_changed", Recipient: email, Locale: u.Locale, UserID: user, Audience: audience}); e != nil {
			return e
		}
		return audit.WriteWithin(ctx, tx, "", user, audience, "auth.password_reset", "success", "user", user, "", request)
	})
	return sessionError(e)
}

func updateRecoveryCredential(ctx context.Context, tx transaction.Executor, user, phc string) error {
	n, e := tx.Exec(ctx, identitySQL(tx, "UPDATE password_credentials SET password_phc=$1,version=version+1,changed_at=clock_timestamp() WHERE user_id=$2", "UPDATE password_credentials SET password_phc=?,version=version+1,changed_at=UTC_TIMESTAMP(6) WHERE user_id=?", `UPDATE PASSWORD_CREDENTIALS SET PASSWORD_PHC=?,VERSION=VERSION+1,CHANGED_AT=CURRENT_TIMESTAMP(6) WHERE USER_ID=?`, phc, user))
	if e == nil && n != 1 {
		return failure("STORAGE_UNAVAILABLE")
	}
	return e
}

func (s *Service) RecoverAdmin(ctx context.Context, password, reason string) error {
	if !recoveryReason(reason) {
		return failure("OPERATOR_REASON_REQUIRED")
	}
	store, e := s.recoveryStore(false)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Config.Policy().HTTP.StartupSeconds))
	defer cancel()
	var user, email string
	e = store.QueryRow(ctx, identitySQL(store, "SELECT u.id::text,u.email_canonical FROM installation_state i JOIN users u ON u.id=i.primary_admin_user_id WHERE i.id=1 AND i.bootstrap_complete", "SELECT u.id,u.email_canonical FROM installation_state i JOIN users u ON u.id=i.primary_admin_user_id WHERE i.id=1 AND i.bootstrap_complete=1", `SELECT U.ID,U.EMAIL_CANONICAL FROM INSTALLATION_STATE I JOIN USERS U ON U.ID=I.PRIMARY_ADMIN_USER_ID WHERE I.ID=1 AND I.BOOTSTRAP_COMPLETE=1`)).Scan(&user, &email)
	if errors.Is(e, sql.ErrNoRows) {
		return failure("SERVICE_NOT_READY")
	}
	if e != nil {
		return sessionError(e)
	}
	password, e = s.passwordPolicy(ctx, password, email)
	if e != nil {
		return e
	}
	phc, e := s.Passwords.Hash(ctx, password)
	if e != nil {
		return e
	}
	e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		var locked string
		if e := tx.QueryRow(ctx, identitySQL(tx, "SELECT id::text FROM users WHERE id=$1 FOR UPDATE", "SELECT id FROM users WHERE id=? FOR UPDATE", `SELECT ID FROM USERS WHERE ID=? FOR UPDATE`, user)).Scan(&locked); e != nil {
			return e
		}
		var current bool
		if e := tx.QueryRow(ctx, identitySQL(tx, "SELECT bootstrap_complete AND primary_admin_user_id=$1 FROM installation_state WHERE id=1", "SELECT bootstrap_complete=1 AND primary_admin_user_id=? FROM installation_state WHERE id=1", `SELECT CASE WHEN BOOTSTRAP_COMPLETE=1 AND PRIMARY_ADMIN_USER_ID=? THEN 1 ELSE 0 END FROM INSTALLATION_STATE WHERE ID=1`, user)).Scan(&current); e != nil {
			return e
		}
		if !current {
			return failure("SERVICE_NOT_READY")
		}
		if e := updateRecoveryCredential(ctx, tx, user, phc); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, identitySQL(tx, "UPDATE users SET status='active',auth_epoch=auth_epoch+1,version=version+1,updated_at=clock_timestamp() WHERE id=$1", "UPDATE users SET status='active',auth_epoch=auth_epoch+1,version=version+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?", `UPDATE USERS SET STATUS='active',AUTH_EPOCH=AUTH_EPOCH+1,VERSION=VERSION+1,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE ID=?`, user)); e != nil {
			return e
		}
		if e := revokeAuthorityWithin(ctx, tx, user, "operator_recovery"); e != nil {
			return e
		}
		return audit.WriteWithin(ctx, tx, "", user, "operator", "operator.admin_recovered", "success", "user", user, reason, "")
	})
	return sessionError(e)
}

// InvalidateAccountAuthoritiesWithin is restricted to trusted restore/operator
// construction. The enclosing multi-module restore retains commit authority.
func (s *Service) InvalidateAccountAuthoritiesWithin(ctx context.Context, tx transaction.Executor, reason string) error {
	if !recoveryReason(reason) {
		return failure("OPERATOR_REASON_REQUIRED")
	}
	if e := s.sessionExecutor(tx); e != nil {
		return e
	}
	rows, e := tx.Query(ctx, identitySQL(tx, "SELECT id::text FROM users ORDER BY id FOR UPDATE", "SELECT id FROM users ORDER BY id FOR UPDATE", `SELECT ID FROM USERS ORDER BY ID FOR UPDATE`))
	if e != nil {
		return e
	}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	commands := []transaction.Statement{
		identitySQL(tx, "UPDATE users SET auth_epoch=auth_epoch+1,version=version+1,updated_at=clock_timestamp()", "UPDATE users SET auth_epoch=auth_epoch+1,version=version+1,updated_at=UTC_TIMESTAMP(6)", `UPDATE USERS SET AUTH_EPOCH=AUTH_EPOCH+1,VERSION=VERSION+1,UPDATED_AT=CURRENT_TIMESTAMP(6)`),
		identitySQL(tx, "UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='restore' WHERE revoked_at IS NULL", "UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='restore' WHERE revoked_at IS NULL", `UPDATE SESSIONS SET REVOKED_AT=CURRENT_TIMESTAMP(6),REVOKE_REASON='restore' WHERE REVOKED_AT IS NULL`),
		identitySQL(tx, "UPDATE password_reset_tokens SET token_hash=NULL,revoked_at=clock_timestamp() WHERE consumed_at IS NULL AND revoked_at IS NULL", "UPDATE password_reset_tokens SET token_hash=NULL,revoked_at=UTC_TIMESTAMP(6) WHERE consumed_at IS NULL AND revoked_at IS NULL", `UPDATE PASSWORD_RESET_TOKENS SET TOKEN_HASH=NULL,REVOKED_AT=CURRENT_TIMESTAMP(6) WHERE CONSUMED_AT IS NULL AND REVOKED_AT IS NULL`),
		identitySQL(tx, "UPDATE invitations SET status='revoked',token_hash=NULL,revoked_at=clock_timestamp(),version=version+1,updated_at=clock_timestamp() WHERE status='pending'", "UPDATE invitations SET status='revoked',token_hash=NULL,revoked_at=UTC_TIMESTAMP(6),version=version+1,updated_at=UTC_TIMESTAMP(6) WHERE status='pending'", `UPDATE INVITATIONS SET STATUS='revoked',TOKEN_HASH=NULL,REVOKED_AT=CURRENT_TIMESTAMP(6),VERSION=VERSION+1,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE STATUS='pending'`),
		identitySQL(tx, "UPDATE mail_deliveries SET state='cancelled',payload_key_id=NULL,payload_nonce=NULL,payload_ciphertext=NULL,lease_token=NULL,locked_until=NULL,completed_at=clock_timestamp(),updated_at=clock_timestamp(),error_category='restore_invalidated' WHERE state IN ('queued','sending')", "UPDATE mail_deliveries SET state='cancelled',payload_key_id=NULL,payload_nonce=NULL,payload_ciphertext=NULL,lease_token=NULL,locked_until=NULL,completed_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6),error_category='restore_invalidated' WHERE state IN ('queued','sending')", `UPDATE MAIL_DELIVERIES SET STATE='cancelled',PAYLOAD_KEY_ID=NULL,PAYLOAD_NONCE=NULL,PAYLOAD_CIPHERTEXT=NULL,LEASE_TOKEN=NULL,LOCKED_UNTIL=NULL,COMPLETED_AT=CURRENT_TIMESTAMP(6),UPDATED_AT=CURRENT_TIMESTAMP(6),ERROR_CATEGORY='restore_invalidated' WHERE STATE IN ('queued','sending')`),
		identitySQL(tx, "DELETE FROM idempotency_records", "DELETE FROM idempotency_records", `DELETE FROM IDEMPOTENCY_RECORDS`),
	}
	for _, q := range commands {
		if _, e = tx.Exec(ctx, q); e != nil {
			return e
		}
	}
	return audit.WriteWithin(ctx, tx, "", "", "operator", "operator.auth_invalidated", "success", "installation", "", reason, "")
}
