package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const (
	adminTOTPEnrollmentSeconds = 600
	adminTOTPChallengeSeconds  = 180
	adminTOTPStepSeconds       = 30
	adminTOTPSeedBytes         = 20
	adminTOTPRecoveryCount     = 10
	adminTOTPRecoveryDays      = 90
)

// AdminTOTPCandidateReady reports whether the new browser operations can be
// mounted. Session issuance checks stored factors independently.
func (s *Service) AdminTOTPCandidateReady() bool {
	return s.adminTOTP != nil
}

// AdminTOTPEnrollment contains the one-time seed display for a pending factor.
type AdminTOTPEnrollment struct {
	ID        string    `json:"id"`
	Secret    string    `json:"secret"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AdminTOTPChallenge is pending authentication, with no browser session authority.
type AdminTOTPChallenge struct {
	Challenge string    `json:"challenge"`
	ExpiresAt time.Time `json:"expires_at"`
}

type adminTOTPFactor struct {
	id, tenant, key string
	nonce, seed     []byte
	lastStep        int64
	enabled         bool
	enrolling       *string
	expires         *time.Time
}

func adminTOTPFactorWithin(ctx context.Context, tx transaction.Executor, user string) (adminTOTPFactor, error) {
	var factor adminTOTPFactor
	if tx.Dialect() != transaction.PostgreSQL {
		return factor, nil
	}
	// The candidate schema is intentionally separate from production migration.
	// Once present, even a replica without the key file must enforce its rows.
	var installed bool
	if err := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
		"SELECT to_regclass('admin_totp_factors') IS NOT NULL")).Scan(&installed); err != nil || !installed {
		return factor, err
	}
	err := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT id::text,tenant_id::text,key_id,nonce,seed,
		last_step,enabled_at IS NOT NULL,enrollment_session_id::text,enrollment_expires_at
		FROM admin_totp_factors WHERE user_id=$1`, user)).
		Scan(&factor.id, &factor.tenant, &factor.key, &factor.nonce, &factor.seed, &factor.lastStep, &factor.enabled, &factor.enrolling, &factor.expires)
	if errors.Is(err, sql.ErrNoRows) {
		return adminTOTPFactor{}, nil
	}
	return factor, err
}

func adminTOTPTenant(ctx context.Context, tx transaction.Executor) (string, error) {
	var tenant *string
	err := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT "+invitationTenantSQL+"::text")).Scan(&tenant)
	if err != nil {
		return "", err
	}
	if tenant == nil {
		return "", failure("STORAGE_UNAVAILABLE")
	}
	return *tenant, nil
}

func adminTOTPAAD(tenant, user, factor string) string {
	return tenant + ":" + user + ":" + factor
}

func (s *Service) totpPassword(ctx context.Context, a Auth, password string) (PasswordConfirmation, error) {
	if !sessionActor(a) {
		return PasswordConfirmation{}, failure("AUTH_SESSION_EXPIRED")
	}
	if a.Me.Audience != "admin" {
		return PasswordConfirmation{}, failure("ACCESS_DENIED")
	}
	if s.adminTOTP == nil {
		return PasswordConfirmation{}, failure("STORAGE_UNAVAILABLE")
	}
	p := s.Config.Policy().Rates
	if _, err := s.Admit(ctx, Limit{Purpose: "totp.password", Identifier: a.Me.User.ID, Count: p.LoginEmailCount, Window: time.Duration(p.LoginEmailWindowSeconds) * time.Second}); err != nil {
		return PasswordConfirmation{}, err
	}
	proof, err := s.ConfirmPassword(ctx, a, password)
	if code(err) == "SSO_PASSWORD_REQUIRED" {
		err = failure("PASSWORD_CURRENT_INVALID")
	}
	return proof, err
}

func localAdminTOTPWithin(ctx context.Context, tx transaction.Executor, a Auth) error {
	if tx.Dialect() != transaction.PostgreSQL {
		return failure("STORAGE_UNAVAILABLE")
	}
	var local bool
	err := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
		"SELECT sso_connector_id IS NULL FROM sessions WHERE id=$1 AND user_id=$2", a.SessionID, a.Me.User.ID)).Scan(&local)
	if err == nil && !local {
		return failure("ACCESS_DENIED")
	}
	return err
}

// StartAdminTOTPEnrollment stores a short-lived factor bound to the current
// local Admin session after proving the current password.
func (s *Service) StartAdminTOTPEnrollment(ctx context.Context, a Auth, password string) (AdminTOTPEnrollment, error) {
	proof, err := s.totpPassword(ctx, a, password)
	if err != nil {
		return AdminTOTPEnrollment{}, err
	}
	store, err := s.Repository()
	if err != nil {
		return AdminTOTPEnrollment{}, err
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	var out AdminTOTPEnrollment
	err = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		if err := s.RecheckConfirmationWithin(ctx, tx, a, proof); err != nil {
			return err
		}
		if err := localAdminTOTPWithin(ctx, tx, a); err != nil {
			return err
		}
		factor, err := adminTOTPFactorWithin(ctx, tx, a.Me.User.ID)
		if err != nil {
			return err
		}
		tenant, err := adminTOTPTenant(ctx, tx)
		if err != nil {
			return err
		}
		if factor.id != "" && factor.tenant != tenant {
			return failure("ACCESS_DENIED")
		}
		if factor.enabled {
			return failure("VERSION_CONFLICT")
		}
		seed := make([]byte, adminTOTPSeedBytes)
		if _, err := rand.Read(seed); err != nil {
			return err
		}
		out.ID = uuid.NewString()
		defer clear(seed)
		key, nonce, encrypted, err := s.adminTOTP.encrypt(adminTOTPAAD(tenant, a.Me.User.ID, out.ID), seed)
		if err != nil {
			return failure("STORAGE_UNAVAILABLE")
		}
		const insert = `INSERT INTO admin_totp_factors(user_id,tenant_id,id,key_id,nonce,seed,enrollment_session_id,enrollment_expires_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,clock_timestamp()+make_interval(secs=>$8))
			ON CONFLICT(user_id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id,id=EXCLUDED.id,key_id=EXCLUDED.key_id,nonce=EXCLUDED.nonce,
			seed=EXCLUDED.seed,enrollment_session_id=EXCLUDED.enrollment_session_id,enrollment_expires_at=EXCLUDED.enrollment_expires_at
			WHERE admin_totp_factors.enabled_at IS NULL RETURNING enrollment_expires_at`
		if err := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, insert, a.Me.User.ID, tenant, out.ID, key, nonce, encrypted, a.SessionID, adminTOTPEnrollmentSeconds)).Scan(&out.ExpiresAt); err != nil {
			return err
		}
		out.ExpiresAt = out.ExpiresAt.UTC()
		out.Secret = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(seed)
		return nil
	})
	if err != nil {
		return AdminTOTPEnrollment{}, sessionError(err)
	}
	return out, nil
}

// ConfirmAdminTOTPEnrollment proves possession, retires old Admin sessions
// and returns the initial one-use recovery values once.
func (s *Service) ConfirmAdminTOTPEnrollment(ctx context.Context, a Auth, id, otp, requestID string) ([]string, error) {
	if !sessionID(id) {
		return nil, failure("VALIDATION_FAILED")
	}
	return s.changeAdminTOTP(ctx, a, nil, id, otp, requestID)
}

// RevokeAdminTOTP requires the current password and a fresh code before ending the factor.
func (s *Service) RevokeAdminTOTP(ctx context.Context, a Auth, password, otp, requestID string) error {
	proof, err := s.totpPassword(ctx, a, password)
	if err != nil {
		return err
	}
	_, err = s.changeAdminTOTP(ctx, a, &proof, "", otp, requestID)
	return err
}

func (s *Service) changeAdminTOTP(ctx context.Context, a Auth, proof *PasswordConfirmation, id, otp, requestID string) ([]string, error) {
	if !validAdminTOTPCode(otp) || !sessionID(requestID) {
		return nil, failure("VALIDATION_FAILED")
	}
	if a.Me.Audience != "admin" {
		return nil, failure("ACCESS_DENIED")
	}
	store, err := s.Repository()
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	var refused error
	var codes []string
	err = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		if proof == nil {
			if err := s.RecheckWithin(ctx, tx, a); err != nil {
				return err
			}
		} else if err := s.RecheckConfirmationWithin(ctx, tx, a, *proof); err != nil {
			return err
		}
		if err := localAdminTOTPWithin(ctx, tx, a); err != nil {
			return err
		}
		factor, err := adminTOTPFactorWithin(ctx, tx, a.Me.User.ID)
		if err != nil {
			return err
		}
		var now time.Time
		if err := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT clock_timestamp()")).Scan(&now); err != nil {
			return err
		}
		revoking := proof != nil
		if revoking && !factor.enabled || !revoking && (factor.enabled || factor.id != id ||
			factor.enrolling == nil || *factor.enrolling != a.SessionID || factor.expires == nil || !factor.expires.After(now)) {
			return failure("TOKEN_UNUSABLE")
		}
		tenant, err := adminTOTPTenant(ctx, tx)
		if err != nil {
			return err
		}
		if factor.tenant != tenant {
			return failure("TOKEN_UNUSABLE")
		}
		step, valid, err := s.verifyAdminTOTPWithin(ctx, tx, factor, a.Me.User.ID, otp, now)
		if err != nil {
			return err
		}
		if !valid {
			refused = failure("AUTH_INVALID_CREDENTIALS")
			return nil
		}
		action := "auth.totp_enabled"
		if revoking {
			if err := adminTOTPExecOne(ctx, tx, "DELETE FROM admin_totp_factors WHERE user_id=$1 AND id=$2", a.Me.User.ID, factor.id); err != nil {
				return err
			}
			action = "auth.totp_revoked"
		} else {
			if err := adminTOTPExecOne(ctx, tx, `UPDATE admin_totp_factors SET enabled_at=clock_timestamp(),
				last_step=$3,enrollment_session_id=NULL,enrollment_expires_at=NULL WHERE user_id=$1 AND id=$2`, a.Me.User.ID, factor.id, step); err != nil {
				return err
			}
			codes, err = s.createAdminTOTPRecoveryWithin(ctx, tx, factor, a.Me.User.ID)
			if err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, transaction.SQL(transaction.PostgreSQL,
			"UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='totp_changed' WHERE user_id=$1 AND audience='admin' AND revoked_at IS NULL", a.Me.User.ID)); err != nil {
			return err
		}
		return audit.WriteWithin(ctx, tx, a.Me.User.ID, a.Me.User.ID, "admin", action, "success", "user", a.Me.User.ID, "", requestID)
	})
	if err != nil {
		return nil, sessionError(err)
	}
	return codes, refused
}

func adminTOTPExecOne(ctx context.Context, tx transaction.Executor, query string, args ...any) error {
	n, err := tx.Exec(ctx, transaction.SQL(transaction.PostgreSQL, query, args...))
	if err == nil && n != 1 {
		return failure("IDENTITY_WRITE_UNCONFIRMED")
	}
	return err
}

func (s *Service) verifyAdminTOTPWithin(ctx context.Context, tx transaction.Executor, f adminTOTPFactor, user, otp string, now time.Time) (int64, bool, error) {
	if s.adminTOTP == nil {
		return 0, false, failure("STORAGE_UNAVAILABLE")
	}
	if err := s.admitAdminTOTPWithin(ctx, tx, user); err != nil {
		return 0, false, err
	}
	seed, err := s.adminTOTP.decrypt(f.key, adminTOTPAAD(f.tenant, user, f.id), f.nonce, f.seed)
	if err != nil {
		return 0, false, failure("STORAGE_UNAVAILABLE")
	}
	defer clear(seed)
	step, ok := adminTOTPStep(seed, otp, now, f.lastStep)
	return step, ok, nil
}

func (s *Service) admitAdminTOTPWithin(ctx context.Context, tx transaction.Executor, user string) error {
	p := s.Config.Policy().Rates
	_, err := s.AdmitWithin(ctx, tx, Limit{Purpose: "totp.verify", Identifier: user, Count: p.LoginEmailCount, Window: time.Duration(p.LoginEmailWindowSeconds) * time.Second})
	return err
}

func (s *Service) recoveryCodeDigest(f adminTOTPFactor, user, value string) []byte {
	return security.MAC(s.HMACKey, "admin-totp-recovery", f.tenant, user, f.id, value)
}

func (s *Service) createAdminTOTPRecoveryWithin(ctx context.Context, tx transaction.Executor, f adminTOTPFactor, user string) ([]string, error) {
	out := make([]string, adminTOTPRecoveryCount)
	const insert = `INSERT INTO admin_totp_recovery_codes(digest,user_id,tenant_id,factor_id,expires_at)
		VALUES($1,$2,$3,$4,clock_timestamp()+make_interval(days=>$5))`
	for i := range out {
		out[i] = security.Token()
		if err := adminTOTPExecOne(ctx, tx, insert, s.recoveryCodeDigest(f, user, out[i]), user, f.tenant, f.id, adminTOTPRecoveryDays); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Service) useAdminTOTPRecoveryWithin(ctx context.Context, tx transaction.Executor, f adminTOTPFactor, user, value string) (bool, error) {
	if err := s.admitAdminTOTPWithin(ctx, tx, user); err != nil {
		return false, err
	}
	const consume = `DELETE FROM admin_totp_recovery_codes WHERE digest=$1 AND user_id=$2 AND tenant_id=$3
		AND factor_id=$4 AND expires_at>clock_timestamp()`
	n, err := tx.Exec(ctx, transaction.SQL(transaction.PostgreSQL, consume, s.recoveryCodeDigest(f, user, value), user, f.tenant, f.id))
	return n == 1, err
}

func advanceAdminTOTPStep(ctx context.Context, tx transaction.Executor, user, factorID string, step int64) error {
	return adminTOTPExecOne(ctx, tx, "UPDATE admin_totp_factors SET last_step=$3 WHERE user_id=$1 AND id=$2 AND last_step<$3", user, factorID, step)
}

func validAdminTOTPCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, digit := range code {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func adminTOTPStep(seed []byte, code string, now time.Time, last int64) (int64, bool) {
	current := now.Unix() / adminTOTPStepSeconds
	matched := int64(-1)
	replayed := false
	for step := current - 1; step <= current+1; step++ {
		var counter [8]byte
		binary.BigEndian.PutUint64(counter[:], uint64(step))
		mac := hmac.New(sha1.New, seed)
		_, _ = mac.Write(counter[:])
		sum := mac.Sum(nil)
		offset := sum[len(sum)-1] & 15
		value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
		expected := fmt.Sprintf("%06d", value%1_000_000)
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) != 1 {
			continue
		}
		if step <= last {
			replayed = true
		} else {
			matched = step
		}
	}
	return matched, matched >= 0 && !replayed
}
