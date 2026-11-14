package identity

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func (s *Service) startAdminTOTPWithin(ctx context.Context, tx transaction.Executor, factor adminTOTPFactor, user string, epoch, version int64, previous, browser string) (*AdminTOTPChallenge, error) {
	if s.adminTOTP == nil {
		return nil, failure("STORAGE_UNAVAILABLE")
	}
	if !security.ValidToken(browser) {
		return nil, failure("ACCESS_DENIED")
	}
	tenant, err := adminTOTPTenant(ctx, tx)
	if err != nil {
		return nil, err
	}
	if factor.tenant != tenant {
		return nil, failure("ACCESS_DENIED")
	}
	if _, err := tx.Exec(ctx, transaction.SQL(transaction.PostgreSQL,
		"DELETE FROM admin_totp_challenges WHERE user_id=$1 AND expires_at<=clock_timestamp()", user)); err != nil {
		return nil, err
	}
	out := AdminTOTPChallenge{Challenge: security.Token()}
	const insert = `INSERT INTO admin_totp_challenges(challenge_hash,browser_hash,previous_hash,user_id,tenant_id,factor_id,auth_epoch,credential_version,expires_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,clock_timestamp()+make_interval(secs=>$9))
		ON CONFLICT(user_id,browser_hash) DO UPDATE SET challenge_hash=EXCLUDED.challenge_hash,previous_hash=EXCLUDED.previous_hash,
		tenant_id=EXCLUDED.tenant_id,factor_id=EXCLUDED.factor_id,auth_epoch=EXCLUDED.auth_epoch,
		credential_version=EXCLUDED.credential_version,expires_at=EXCLUDED.expires_at RETURNING expires_at`
	err = tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, insert, security.Digest(out.Challenge),
		security.Digest(browser), security.Digest(previous), user, tenant, factor.id, epoch, version, adminTOTPChallengeSeconds)).Scan(&out.ExpiresAt)
	if err != nil {
		return nil, err
	}
	out.ExpiresAt = out.ExpiresAt.UTC()
	return &out, nil
}

// CompleteAdminTOTPLogin consumes the challenge and the accepted time step in
// the same transaction as issuing the complete Admin session.
func (s *Service) CompleteAdminTOTPLogin(ctx context.Context, challenge, otp, previous, browser, requestID string) (IssuedSession, error) {
	recovery := security.ValidToken(otp)
	if !security.ValidToken(challenge) || !validAdminTOTPCode(otp) && !recovery || !sessionID(requestID) {
		return IssuedSession{}, failure("VALIDATION_FAILED")
	}
	if !security.ValidToken(browser) {
		return IssuedSession{}, failure("AUTH_INVALID_CREDENTIALS")
	}
	store, err := s.Repository()
	if err != nil {
		return IssuedSession{}, err
	}
	if store.Dialect() != transaction.PostgreSQL || s.adminTOTP == nil {
		return IssuedSession{}, failure("STORAGE_UNAVAILABLE")
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	var issued IssuedSession
	var refused error
	args := []any{security.Digest(challenge), security.Digest(browser), security.Digest(previous)}
	err = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		const matches = " WHERE challenge_hash=$1 AND browser_hash=$2 AND previous_hash=$3"
		var user string
		err := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
			"SELECT user_id::text FROM admin_totp_challenges"+matches, args...)).Scan(&user)
		if errors.Is(err, sql.ErrNoRows) {
			return failure("AUTH_INVALID_CREDENTIALS")
		}
		if err != nil {
			return err
		}
		if err := s.LockSessionUsersWithin(ctx, tx, user, previous, "admin"); err != nil {
			return err
		}
		var tenant, factorID string
		var epoch, version int64
		var now, expiry time.Time
		err = tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
			"SELECT tenant_id::text,factor_id::text,auth_epoch,credential_version,clock_timestamp(),expires_at FROM admin_totp_challenges"+matches+" AND user_id=$4",
			append(args, user)...)).Scan(&tenant, &factorID, &epoch, &version, &now, &expiry)
		if errors.Is(err, sql.ErrNoRows) || err == nil && !expiry.After(now) {
			return failure("AUTH_INVALID_CREDENTIALS")
		}
		if err != nil {
			return err
		}
		currentTenant, err := adminTOTPTenant(ctx, tx)
		if err != nil {
			return err
		}
		factor, err := adminTOTPFactorWithin(ctx, tx, user)
		if err != nil {
			return err
		}
		if tenant != currentTenant || tenant != factor.tenant || factor.id != factorID || !factor.enabled {
			return failure("AUTH_INVALID_CREDENTIALS")
		}
		var current bool
		err = tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT EXISTS(
			SELECT 1 FROM users u JOIN password_credentials c ON c.user_id=u.id WHERE u.id=$1
			AND u.auth_epoch=$2 AND c.version=$3 AND u.status='active' AND u.is_platform_admin)`, user, epoch, version)).Scan(&current)
		if err != nil {
			return err
		}
		if !current {
			return failure("AUTH_INVALID_CREDENTIALS")
		}
		var valid bool
		if recovery {
			valid, err = s.useAdminTOTPRecoveryWithin(ctx, tx, factor, user, otp)
		} else {
			var step int64
			step, valid, err = s.verifyAdminTOTPWithin(ctx, tx, factor, user, otp, now)
			if err == nil && valid {
				err = advanceAdminTOTPStep(ctx, tx, user, factorID, step)
			}
		}
		if err != nil {
			return err
		}
		if !valid {
			// Persist verification attempts even when no authority is issued.
			refused = failure("AUTH_INVALID_CREDENTIALS")
			return nil
		}
		if err := adminTOTPExecOne(ctx, tx, "DELETE FROM admin_totp_challenges WHERE challenge_hash=$1", args[0]); err != nil {
			return err
		}
		issued, err = s.issueAuthorizedSessionWithin(ctx, tx, user, epoch, "admin", previous, requestID, nil, "auth.login_succeeded", true)
		return err
	})
	if err != nil {
		return IssuedSession{}, sessionError(err)
	}
	return issued, refused
}
