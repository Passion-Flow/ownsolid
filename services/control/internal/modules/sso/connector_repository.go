package sso

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"time"

	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type connectorOperation string

const (
	connectorCreate             connectorOperation = "create_connector"
	connectorUpdate             connectorOperation = "update_connector"
	maximumConnectorIntentBytes                    = 8192
)

type connectorFence uint8

const (
	connectorPeek connectorFence = iota
	connectorReadFence
	connectorCreateFence
)

type connectorLock struct{ tenant, id string }

func connectorTenant(ctx context.Context, q transaction.Executor, mode connectorFence) (string, error) {
	suffix, damengSuffix := "", ""
	switch mode {
	case connectorPeek:
	case connectorReadFence:
		suffix = " FOR SHARE"
	case connectorCreateFence:
		suffix = " FOR UPDATE"
	default:
		return "", fail("SSO_TRANSACTION_STORAGE_MISMATCH")
	}
	if mode != connectorPeek {
		damengSuffix = " FOR UPDATE OF ID"
	}
	var id sql.NullString
	var complete bool
	e := q.QueryRow(ctx, sourceSQL(q, `SELECT tenant_id::text,bootstrap_complete FROM installation_state WHERE id=1`+suffix, `SELECT tenant_id,bootstrap_complete FROM installation_state WHERE id=1`+suffix, "SELECT TENANT_ID,BOOTSTRAP_COMPLETE FROM INSTALLATION_STATE WHERE ID=1"+damengSuffix)).Scan(&id, &complete)
	if errors.Is(e, sql.ErrNoRows) || e == nil && (!complete || !id.Valid || !canonicalID(id.String)) {
		return "", fail("SSO_DISABLED")
	}
	if e != nil || mode == connectorPeek {
		return id.String, e
	}
	var status string
	e = q.QueryRow(ctx, sourceSQL(q, `SELECT status FROM tenants WHERE id=$1 FOR SHARE`, `SELECT status FROM tenants WHERE id=? FOR SHARE`, "SELECT STATUS FROM TENANTS WHERE ID=? FOR UPDATE OF ID", id.String)).Scan(&status)
	if errors.Is(e, sql.ErrNoRows) || e == nil && status != "active" {
		return "", fail("SSO_DISABLED")
	}
	return id.String, e
}

func connectorLocks(values []connectorLock) []connectorLock {
	sort.Slice(values, func(i, j int) bool {
		return values[i].tenant < values[j].tenant || values[i].tenant == values[j].tenant && values[i].id < values[j].id
	})
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func (s *Service) connectorMutation(ctx context.Context, a identity.Auth, operation connectorOperation, target string, fn func(context.Context, transaction.Executor, string) error) error {
	fence := connectorReadFence
	if operation == connectorCreate {
		fence = connectorCreateFence
	} else if operation != connectorUpdate {
		return fail("SSO_TRANSACTION_STORAGE_MISMATCH")
	}
	return s.actorTransaction(ctx, a, fence, connectorExclusive, func(_ context.Context, _ transaction.Executor, tenant string) ([]connectorLock, error) {
		return []connectorLock{{tenant, target}}, nil
	}, fn)
}

func connectorTimestamp(value time.Time) bool {
	_, offset := value.Zone()
	return !value.IsZero() && value.Year() >= minimumSSOCatalogYear && value.Year() <= maximumSSOCatalogYear && offset == 0 && value.Nanosecond()%int(time.Microsecond) == 0
}

func decodeConnectorIntent(raw []byte, target string) (Connector, error) {
	var result Connector
	invalid := fail("STORAGE_UNAVAILABLE")
	if len(raw) == 0 || len(raw) > maximumConnectorIntentBytes {
		return result, invalid
	}
	// The strict walker rejects duplicates,nulls and invalid Unicode. Validate
	// exact DTO field names separately so time.Time is decoded as its JSON string.
	var fields map[string]json.RawMessage
	if strict.DecodeStrict(raw, &fields) != nil {
		return result, invalid
	}
	typ := reflect.TypeOf(result)
	if len(fields) != typ.NumField() {
		return result, invalid
	}
	for i := 0; i < typ.NumField(); i++ {
		if _, present := fields[strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]]; !present {
			return result, invalid
		}
	}
	if json.Unmarshal(raw, &result) != nil {
		return Connector{}, invalid
	}
	// Historical pgx snapshots can contain the host's RFC3339 offset. Preserve
	// their instant while publishing the same UTC representation as new writes.
	result.CreatedAt, result.UpdatedAt = result.CreatedAt.UTC(), result.UpdatedAt.UTC()
	name, e := security.Name(result.Name)
	if e != nil || name != result.Name || result.ID != target || !canonicalID(result.ID) || !canonicalID(result.ClientID) || result.Provider != ProviderGitea || !config.SSOOrigin(result.Origin) || result.Revision < 1 || result.Revision > identity.MaximumAuthVersion || result.AuthVersion < 1 || result.AuthVersion > identity.MaximumAuthVersion || !connectorTimestamp(result.CreatedAt) || !connectorTimestamp(result.UpdatedAt) {
		return Connector{}, invalid
	}
	return result, nil
}

func replayConnectorIntent(ctx context.Context, q transaction.Executor, tenant string, a identity.Auth, key string, operation connectorOperation, target string, fingerprint []byte) (Connector, bool, error) {
	var storedOperation, storedTarget string
	var storedFingerprint, raw []byte
	e := q.QueryRow(ctx, sourceSQL(q,
		`SELECT operation,target_id::text,fingerprint,result FROM sso_mutation_intents WHERE tenant_id=$1 AND actor_user_id=$2 AND idempotency_key=$3`,
		`SELECT operation,target_id,fingerprint,result FROM sso_mutation_intents WHERE tenant_id=? AND actor_user_id=? AND idempotency_key=?`, "SELECT OPERATION,TARGET_ID,FINGERPRINT,RESULT FROM SSO_MUTATION_INTENTS WHERE TENANT_ID=? AND ACTOR_USER_ID=? AND IDEMPOTENCY_KEY=?", tenant, a.Me.User.ID, key)).Scan(&storedOperation, &storedTarget, &storedFingerprint, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		return Connector{}, false, nil
	}
	if e != nil {
		return Connector{}, false, e
	}
	if storedOperation != string(operation) || storedTarget != target || !hmac.Equal(storedFingerprint, fingerprint) {
		return Connector{}, false, fail("IDEMPOTENCY_CONFLICT")
	}
	result, e := decodeConnectorIntent(raw, target)
	return result, e == nil, e
}

func saveConnectorIntent(ctx context.Context, q transaction.Executor, tenant string, a identity.Auth, key string, operation connectorOperation, fingerprint []byte, result Connector) error {
	raw, e := json.Marshal(result)
	if e != nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	if _, e = decodeConnectorIntent(raw, result.ID); e != nil {
		return e
	}
	changed, e := q.Exec(ctx, sourceSQL(q,
		`INSERT INTO sso_mutation_intents(tenant_id,actor_user_id,idempotency_key,operation,target_id,fingerprint,result) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		`INSERT INTO sso_mutation_intents(tenant_id,actor_user_id,idempotency_key,operation,target_id,fingerprint,result) VALUES(?,?,?,?,?,?,?)`, "INSERT INTO SSO_MUTATION_INTENTS(TENANT_ID,ACTOR_USER_ID,IDEMPOTENCY_KEY,OPERATION,TARGET_ID,FINGERPRINT,RESULT) VALUES(?,?,?,?,?,?,?)", tenant, a.Me.User.ID, key, string(operation), result.ID, fingerprint, string(raw)))
	if e == nil && changed != 1 {
		return fail("STORAGE_UNAVAILABLE")
	}
	return e
}

func selectConnectorWithin(ctx context.Context, q transaction.Executor, tenant, id string) (Connector, error) {
	result, e := scanConnector(q.QueryRow(ctx, sourceSQL(q, `SELECT `+connectorColumns+` FROM sso_connectors WHERE tenant_id=$1 AND id=$2`, `SELECT `+mysqlConnectorColumns+` FROM sso_connectors WHERE tenant_id=? AND id=?`, "SELECT "+damengConnectorColumns+" FROM SSO_CONNECTORS WHERE TENANT_ID=? AND ID=?", tenant, id)))
	// pgx's binary timestamptz codec defaults to time.Local even when the SQL
	// session uses UTC; the repository owns its public timestamp representation.
	result.CreatedAt, result.UpdatedAt = result.CreatedAt.UTC(), result.UpdatedAt.UTC()
	return result, e
}

func (s *Service) CreateConnector(ctx context.Context, a identity.Auth, input CreateConnector, key, requestID string) (Connector, error) {
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return Connector{}, fail("ACCESS_DENIED")
	}
	if s == nil || !s.enabled {
		return Connector{}, fail("SSO_DISABLED")
	}
	name, e := security.Name(input.Name)
	if e != nil || !canonicalID(key) || !canonicalID(input.ID) || !canonicalID(input.ClientID) || !canonicalID(requestID) || input.Provider != ProviderGitea || !validReason(input.Reason) || !safeOpaque(input.ClientSecret, maximumSecretBytes) {
		return Connector{}, fail("VALIDATION_FAILED")
	}
	if _, approved := s.approved[input.Origin]; !approved {
		return Connector{}, fail("SSO_PROVIDER_DESTINATION_REJECTED")
	}
	if s.keyringRecovery && s.keys == nil {
		return Connector{}, fail("SSO_KEYRING_INVALID")
	}
	if s.keys == nil || s.Identity == nil {
		return Connector{}, fail("SSO_CONFIGURATION_REQUIRED")
	}
	fingerprint, e := s.fingerprint(string(connectorCreate), "", input)
	if e != nil {
		return Connector{}, e
	}
	var result Connector
	e = s.connectorMutation(ctx, a, connectorCreate, input.ID, func(ctx context.Context, q transaction.Executor, tenant string) error {
		var found bool
		var e error
		result, found, e = replayConnectorIntent(ctx, q, tenant, a, key, connectorCreate, input.ID, fingerprint)
		if e != nil || found {
			return e
		}
		var count int
		if e = q.QueryRow(ctx, sourceSQL(q, `SELECT COUNT(*) FROM sso_connectors WHERE tenant_id=$1`, `SELECT COUNT(*) FROM sso_connectors WHERE tenant_id=?`, "SELECT COUNT(*) FROM SSO_CONNECTORS WHERE TENANT_ID=?", tenant)).Scan(&count); e != nil {
			return e
		}
		if count >= s.policy.MaximumConnectors {
			return fail("SSO_CONNECTOR_LIMIT")
		}
		var exists bool
		if e = q.QueryRow(ctx, sourceSQL(q, `SELECT EXISTS(SELECT 1 FROM sso_connectors WHERE id=$1)`, `SELECT EXISTS(SELECT 1 FROM sso_connectors WHERE id=?)`, "SELECT CASE WHEN EXISTS(SELECT 1 FROM SSO_CONNECTORS WHERE ID=?) THEN 1 ELSE 0 END FROM DUAL", input.ID)).Scan(&exists); e != nil {
			return e
		}
		if exists {
			return fail("SSO_CONNECTOR_ID_CONFLICT")
		}
		sealed, e := s.keys.seal(secretScope{tenant, input.ID, input.ID, clientSecretPurpose}, []byte(input.ClientSecret))
		if e != nil {
			return e
		}
		changed, e := q.Exec(ctx, sourceSQL(q,
			`INSERT INTO sso_connectors(id,tenant_id,name,provider,origin,client_id,secret_key_id,secret_nonce,secret_ciphertext,enabled,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			`INSERT INTO sso_connectors(id,tenant_id,name,provider,origin,client_id,secret_key_id,secret_nonce,secret_ciphertext,enabled,created_by) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, "INSERT INTO SSO_CONNECTORS(ID,TENANT_ID,NAME,PROVIDER,ORIGIN,CLIENT_ID,SECRET_KEY_ID,SECRET_NONCE,SECRET_CIPHERTEXT,ENABLED,CREATED_BY) VALUES(?,?,?,?,?,?,?,?,?,?,?)", input.ID, tenant, name, input.Provider, input.Origin, input.ClientID, sealed.keyID, sealed.nonce, sealed.ciphertext, input.Enabled, a.Me.User.ID))
		if e != nil {
			var storage *transaction.Error
			if errors.As(e, &storage) && storage.Kind == transaction.UniqueConflict {
				return fail("SSO_CONNECTOR_ID_CONFLICT")
			}
			return e
		}
		if changed != 1 {
			return fail("STORAGE_UNAVAILABLE")
		}
		result, e = selectConnectorWithin(ctx, q, tenant, input.ID)
		if e != nil {
			return e
		}
		if e = saveConnectorIntent(ctx, q, tenant, a, key, connectorCreate, fingerprint, result); e != nil {
			return e
		}
		return audit.WriteWithin(ctx, q, a.Me.User.ID, "", a.Me.Audience, "sso.connector_created", "success", "sso_connector", input.ID, input.Reason, requestID)
	})
	if e != nil {
		return Connector{}, e
	}
	return result, nil
}

func (s *Service) UpdateConnector(ctx context.Context, a identity.Auth, id string, input UpdateConnector, key, requestID string) (Connector, error) {
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return Connector{}, fail("ACCESS_DENIED")
	}
	name, e := security.Name(input.Name)
	if e != nil || !canonicalID(id) || !canonicalID(key) || !canonicalID(requestID) || input.Revision < 1 || input.Revision >= identity.MaximumAuthVersion || !validReason(input.Reason) || (input.ClientID == nil) != (input.ClientSecret == nil) || input.ClientID != nil && (!canonicalID(*input.ClientID) || !safeOpaque(*input.ClientSecret, maximumSecretBytes)) {
		return Connector{}, fail("VALIDATION_FAILED")
	}
	if s == nil || !s.enabled && (input.Enabled || input.ClientID != nil) {
		return Connector{}, fail("SSO_DISABLED")
	}
	if s.keyringRecovery && input.ClientID != nil && s.keys == nil {
		return Connector{}, fail("SSO_KEYRING_INVALID")
	}
	if s.Identity == nil || input.ClientID != nil && s.keys == nil {
		return Connector{}, fail("SSO_CONFIGURATION_REQUIRED")
	}
	fingerprint, e := s.fingerprint(string(connectorUpdate), id, input)
	if e != nil {
		return Connector{}, e
	}
	var result Connector
	e = s.connectorMutation(ctx, a, connectorUpdate, id, func(ctx context.Context, q transaction.Executor, tenant string) error {
		var found bool
		var e error
		result, found, e = replayConnectorIntent(ctx, q, tenant, a, key, connectorUpdate, id, fingerprint)
		if e != nil || found {
			return e
		}
		old, e := selectConnectorWithin(ctx, q, tenant, id)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("SSO_CONNECTOR_NOT_FOUND")
		}
		if e != nil {
			return e
		}
		if old.Revision != input.Revision || old.AuthVersion >= identity.MaximumAuthVersion {
			return fail("SSO_REVISION_CONFLICT")
		}
		if s.keyringRecovery && (input.Enabled || input.ClientID != nil) {
			if e := s.verifyConnectorCredentialWithin(ctx, q, tenant, id); e != nil {
				if !errors.Is(e, errEncryptedState) || input.ClientID == nil {
					return e
				}
				var local bool
				if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
					"SELECT sso_connector_id IS NULL FROM sessions WHERE id=$1 AND user_id=$2", a.SessionID, a.Me.User.ID)).Scan(&local); e != nil {
					return e
				}
				if !local {
					return fail("ACCESS_DENIED")
				}
			}
		}
		authorityChanged := old.Enabled != input.Enabled || input.ClientID != nil
		version := old.AuthVersion
		if authorityChanged {
			version++
		}
		pg := `UPDATE sso_connectors SET name=$3,enabled=$4,auth_version=$5,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND id=$2 AND revision=$6`
		my := `UPDATE sso_connectors SET name=?,enabled=?,auth_version=?,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND id=? AND revision=?`
		dm := "UPDATE SSO_CONNECTORS SET NAME=?,ENABLED=?,AUTH_VERSION=?,REVISION=REVISION+1,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE TENANT_ID=? AND ID=? AND REVISION=?"
		pgArgs := []any{tenant, id, name, input.Enabled, version, input.Revision}
		myArgs := []any{name, input.Enabled, version, tenant, id, input.Revision}
		if input.ClientID != nil {
			sealed, e := s.keys.seal(secretScope{tenant, id, id, clientSecretPurpose}, []byte(*input.ClientSecret))
			if e != nil {
				return e
			}
			pg = `UPDATE sso_connectors SET name=$3,enabled=$4,auth_version=$5,revision=revision+1,client_id=$7,secret_key_id=$8,secret_nonce=$9,secret_ciphertext=$10,updated_at=clock_timestamp() WHERE tenant_id=$1 AND id=$2 AND revision=$6`
			my = `UPDATE sso_connectors SET name=?,enabled=?,auth_version=?,revision=revision+1,client_id=?,secret_key_id=?,secret_nonce=?,secret_ciphertext=?,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND id=? AND revision=?`
			dm = "UPDATE SSO_CONNECTORS SET NAME=?,ENABLED=?,AUTH_VERSION=?,REVISION=REVISION+1,CLIENT_ID=?,SECRET_KEY_ID=?,SECRET_NONCE=?,SECRET_CIPHERTEXT=?,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE TENANT_ID=? AND ID=? AND REVISION=?"
			pgArgs = append(pgArgs, *input.ClientID, sealed.keyID, sealed.nonce, sealed.ciphertext)
			myArgs = []any{name, input.Enabled, version, *input.ClientID, sealed.keyID, sealed.nonce, sealed.ciphertext, tenant, id, input.Revision}
		}
		statement := sourceSQL(q, pg, my, dm, myArgs...)
		if q.Dialect() == transaction.PostgreSQL {
			statement = transaction.SQL(transaction.PostgreSQL, pg, pgArgs...)
		}
		changed, e := q.Exec(ctx, statement)
		if e != nil {
			return e
		}
		if changed != 1 {
			return fail("SSO_REVISION_CONFLICT")
		}
		result, e = selectConnectorWithin(ctx, q, tenant, id)
		if e != nil {
			return e
		}
		if _, e = q.Exec(ctx, sourceSQL(q,
			`UPDATE sso_flows SET state='failed',error_code='SSO_FLOW_UNUSABLE',finished_at=clock_timestamp(),verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE tenant_id=$1 AND connector_id=$2 AND state IN ('pending','claimed')`,
			`UPDATE sso_flows SET state='failed',error_code='SSO_FLOW_UNUSABLE',finished_at=UTC_TIMESTAMP(6),verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE tenant_id=? AND connector_id=? AND state IN ('pending','claimed')`, "UPDATE SSO_FLOWS SET STATE='failed',ERROR_CODE='SSO_FLOW_UNUSABLE',FINISHED_AT=CURRENT_TIMESTAMP(6),VERIFIER_KEY_ID=NULL,VERIFIER_NONCE=NULL,VERIFIER_CIPHERTEXT=NULL WHERE TENANT_ID=? AND CONNECTOR_ID=? AND STATE IN ('pending','claimed')", tenant, id)); e != nil {
			return e
		}
		if authorityChanged {
			if _, e = q.Exec(ctx, sourceSQL(q, `UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='sso_connector_changed' WHERE sso_tenant_id=$1 AND sso_connector_id=$2 AND revoked_at IS NULL`, `UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='sso_connector_changed' WHERE sso_tenant_id=? AND sso_connector_id=? AND revoked_at IS NULL`, "UPDATE SESSIONS SET REVOKED_AT=CURRENT_TIMESTAMP(6),REVOKE_REASON='sso_connector_changed' WHERE SSO_TENANT_ID=? AND SSO_CONNECTOR_ID=? AND REVOKED_AT IS NULL", tenant, id)); e != nil {
				return e
			}
		}
		if e = saveConnectorIntent(ctx, q, tenant, a, key, connectorUpdate, fingerprint, result); e != nil {
			return e
		}
		return audit.WriteWithin(ctx, q, a.Me.User.ID, "", a.Me.Audience, "sso.connector_updated", "success", "sso_connector", id, input.Reason, requestID)
	})
	if e != nil {
		return Connector{}, e
	}
	return result, nil
}
