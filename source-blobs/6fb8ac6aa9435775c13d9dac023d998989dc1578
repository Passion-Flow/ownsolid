package sso

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/jackc/pgx/v5"
)

const connectorColumns = `id::text,name,provider,origin,client_id::text,enabled,revision,auth_version,created_at,updated_at`

func scanConnector(row transaction.Row, extra ...any) (Connector, error) {
	var c Connector
	e := row.Scan(append([]any{&c.ID, &c.Name, &c.Provider, &c.Origin, &c.ClientID, &c.Enabled, &c.Revision, &c.AuthVersion, &c.CreatedAt, &c.UpdatedAt}, extra...)...)
	return c, e
}

const connectorSecretColumns = "secret_key_id,secret_nonce,secret_ciphertext"

func (v *sealedSecret) scanTargets() []any {
	return []any{&v.keyID, &v.nonce, &v.ciphertext}
}

func (s *Service) verifyConnectorCredential(tenant, id string, sealed sealedSecret) error {
	return s.verifyStoredSecret(secretScope{tenant, id, id, clientSecretPurpose}, sealed)
}

// Read under the caller's existing connector fence.
func (s *Service) verifyConnectorCredentialWithin(ctx context.Context, q transaction.Executor, tenant, id string) error {
	var sealed sealedSecret
	if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
		"SELECT "+connectorSecretColumns+" FROM sso_connectors WHERE tenant_id=$1 AND id=$2", tenant, id)).Scan(sealed.scanTargets()...); e != nil {
		return e
	}
	return s.verifyConnectorCredential(tenant, id, sealed)
}

func validReason(value string) bool {
	return utf8.ValidString(value) && len(value) <= 2000 && utf8.RuneCountInString(value) <= 500 && strings.TrimSpace(value) != "" && !strings.ContainsFunc(value, unicode.IsControl)
}

func (s *Service) fingerprint(operation, target string, value any) ([]byte, error) {
	raw, e := json.Marshal(value)
	if e != nil {
		return nil, fail("VALIDATION_FAILED")
	}
	return security.MAC(s.Identity.HMACKey, "m11-mutation-intent", operation, target, string(raw)), nil
}

func replay(ctx context.Context, tx pgx.Tx, tenantID string, a identity.Auth, key, operation, target string, fingerprint []byte, result any) (bool, error) {
	var oldOperation, oldTarget string
	var oldFingerprint, raw []byte
	e := tx.QueryRow(ctx, `SELECT operation,target_id::text,fingerprint,result FROM sso_mutation_intents WHERE tenant_id=$1 AND actor_user_id=$2 AND idempotency_key=$3`, tenantID, a.Me.User.ID, key).Scan(&oldOperation, &oldTarget, &oldFingerprint, &raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if oldOperation != operation || target != "" && target != oldTarget || !hmac.Equal(oldFingerprint, fingerprint) {
		return false, fail("IDEMPOTENCY_CONFLICT")
	}
	if json.Unmarshal(raw, result) != nil {
		return false, fail("STORAGE_UNAVAILABLE")
	}
	return true, nil
}

func saveIntent(ctx context.Context, tx pgx.Tx, tenantID string, a identity.Auth, key, operation, target string, fingerprint []byte, result any) error {
	raw, e := json.Marshal(result)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO sso_mutation_intents(tenant_id,actor_user_id,idempotency_key,operation,target_id,fingerprint,result) VALUES($1,$2,$3,$4,$5,$6,$7)`, tenantID, a.Me.User.ID, key, operation, target, fingerprint, raw)
	return e
}

func invalidateFlows(ctx context.Context, tx pgx.Tx, tenantID, connector, user, code string) error {
	_, e := tx.Exec(ctx, `UPDATE sso_flows SET state='failed',error_code=$4,finished_at=clock_timestamp(),verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE tenant_id=$1 AND connector_id=$2 AND state IN ('pending','claimed') AND ($3::uuid IS NULL OR initiating_user_id=$3)`, tenantID, connector, nullableID(user), code)
	return e
}

func nullableID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

type pageCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
