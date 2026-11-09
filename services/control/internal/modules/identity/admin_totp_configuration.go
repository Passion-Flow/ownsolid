package identity

import (
	"context"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// AdminTOTPConfiguration is the identity startup input for the local Admin
// flow. A zero value preserves a legacy deployment's browser contract.
type AdminTOTPConfiguration struct {
	Enabled     bool
	KeyringFile string
}

// ConfigureAdminTOTP validates the selected deployment before serving. It
// publishes the keyring only after the schema and retained factors are usable.
// An unconfigured service still enforces any stored factor during issuance.
func (s *Service) ConfigureAdminTOTP(ctx context.Context, c AdminTOTPConfiguration) error {
	if s == nil || s.adminTOTP != nil || !c.Enabled && c.KeyringFile != "" {
		return failure("ADMIN_TOTP_CONFIGURATION_INVALID")
	}
	if !c.Enabled {
		return nil
	}
	if c.KeyringFile == "" {
		return failure("ADMIN_TOTP_KEYRING_REQUIRED")
	}
	if c.KeyringFile == s.Config.Security.MailKeyringFile || c.KeyringFile == s.Config.Security.AuthHMACKeyFile ||
		c.KeyringFile == s.Config.SSO.KeyringFile {
		return failure("ADMIN_TOTP_KEYRING_FILE_INVALID")
	}
	keys, err := loadAdminTOTPKeyring(c.KeyringFile)
	if err != nil {
		return err
	}
	store, err := s.Repository()
	if err != nil || store.Dialect() != transaction.PostgreSQL {
		return failure("ADMIN_TOTP_POSTGRESQL_REQUIRED")
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	if err := store.Within(ctx, true, func(ctx context.Context, tx transaction.Executor) error {
		return verifyAdminTOTPStartup(ctx, tx, keys)
	}); err != nil {
		switch code(err) {
		case "ADMIN_TOTP_SCHEMA_UNAVAILABLE", "ADMIN_TOTP_STORED_FACTOR_INVALID":
			return err
		default:
			return failure("ADMIN_TOTP_STORAGE_UNAVAILABLE")
		}
	}
	s.adminTOTP = keys
	return nil
}

func verifyAdminTOTPStartup(ctx context.Context, tx transaction.Executor, keys *adminTOTPKeyring) error {
	// Migration owns constraints and table versions. Resolve every relation and
	// column used by the browser flow before mounting any of its routes.
	const schema = `SELECT f.user_id,f.tenant_id,f.id,f.key_id,f.nonce,f.seed,f.last_step,f.enabled_at,f.enrollment_session_id,f.enrollment_expires_at,
		c.challenge_hash,c.browser_hash,c.previous_hash,c.user_id,c.tenant_id,c.factor_id,c.auth_epoch,c.credential_version,c.expires_at,
		r.digest,r.user_id,r.tenant_id,r.factor_id,r.expires_at
		FROM admin_totp_factors f CROSS JOIN admin_totp_challenges c CROSS JOIN admin_totp_recovery_codes r WHERE false`
	projection, err := tx.Query(ctx, transaction.SQL(transaction.PostgreSQL, schema))
	if err != nil {
		return failure("ADMIN_TOTP_SCHEMA_UNAVAILABLE")
	}
	projection.Close()
	if projection.Err() != nil {
		return failure("ADMIN_TOTP_SCHEMA_UNAVAILABLE")
	}

	const factors = `SELECT tenant_id::text,user_id::text,id::text,key_id,nonce,seed FROM admin_totp_factors
		WHERE enabled_at IS NOT NULL OR enrollment_expires_at>statement_timestamp()`
	rows, err := tx.Query(ctx, transaction.SQL(transaction.PostgreSQL, factors))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var tenant, user, factor, key string
		var nonce, seed []byte
		if err := rows.Scan(&tenant, &user, &factor, &key, &nonce, &seed); err != nil {
			return err
		}
		plain, err := keys.decrypt(key, adminTOTPAAD(tenant, user, factor), nonce, seed)
		if err != nil {
			return err
		}
		clear(plain)
	}
	return rows.Err()
}
