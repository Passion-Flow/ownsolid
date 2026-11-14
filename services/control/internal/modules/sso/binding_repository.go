package sso

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

const mysqlBindingColumns = `b.id,b.connector_id,c.name,c.provider,b.subject,b.active,b.revision,b.created_at,b.updated_at`
const unlinkBindingOperation = "unlink_binding"
const maximumBindingIntentBytes = 8192

func validBinding(value Binding) bool {
	name, e := security.Name(value.Name)
	return e == nil && name == value.Name && canonicalID(value.ID) && canonicalID(value.ConnectorID) && value.Provider == ProviderGitea && canonicalSubject(value.Subject) && value.Revision >= 1 && value.Revision <= identity.MaximumAuthVersion && connectorTimestamp(value.CreatedAt) && connectorTimestamp(value.UpdatedAt) && !value.UpdatedAt.Before(value.CreatedAt)
}

func decodeBindingIntent(raw []byte, target string) (Binding, error) {
	var result Binding
	invalid := fail("STORAGE_UNAVAILABLE")
	if len(raw) == 0 || len(raw) > maximumBindingIntentBytes {
		return result, invalid
	}
	var fields map[string]json.RawMessage
	if strict.DecodeStrict(raw, &fields) != nil {
		return result, invalid
	}
	typ := reflect.TypeOf(result)
	if len(fields) != typ.NumField() {
		return result, invalid
	}
	for i := range typ.NumField() {
		if _, present := fields[strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]]; !present {
			return result, invalid
		}
	}
	if json.Unmarshal(raw, &result) != nil {
		return Binding{}, invalid
	}
	result.CreatedAt, result.UpdatedAt = result.CreatedAt.UTC(), result.UpdatedAt.UTC()
	if result.ID != target || result.Active || !validBinding(result) {
		return Binding{}, invalid
	}
	return result, nil
}

func replayBindingIntent(ctx context.Context, q transaction.Executor, tenant string, a identity.Auth, key, target string, fingerprint []byte) (Binding, bool, error) {
	var operation, storedTarget string
	var storedFingerprint, raw []byte
	e := q.QueryRow(ctx, sourceSQL(q, `SELECT operation,target_id::text,fingerprint,result FROM sso_mutation_intents WHERE tenant_id=$1 AND actor_user_id=$2 AND idempotency_key=$3`, `SELECT operation,target_id,fingerprint,result FROM sso_mutation_intents WHERE tenant_id=? AND actor_user_id=? AND idempotency_key=?`, "SELECT OPERATION,TARGET_ID,FINGERPRINT,RESULT FROM SSO_MUTATION_INTENTS WHERE TENANT_ID=? AND ACTOR_USER_ID=? AND IDEMPOTENCY_KEY=?", tenant, a.Me.User.ID, key)).Scan(&operation, &storedTarget, &storedFingerprint, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		return Binding{}, false, nil
	}
	if e != nil {
		return Binding{}, false, e
	}
	if operation != unlinkBindingOperation || storedTarget != target || !hmac.Equal(storedFingerprint, fingerprint) {
		return Binding{}, false, fail("IDEMPOTENCY_CONFLICT")
	}
	result, e := decodeBindingIntent(raw, target)
	return result, e == nil, e
}

func saveBindingIntent(ctx context.Context, q transaction.Executor, tenant string, a identity.Auth, key string, fingerprint []byte, result Binding) error {
	raw, e := json.Marshal(result)
	if e != nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	if _, e = decodeBindingIntent(raw, result.ID); e != nil {
		return e
	}
	changed, e := q.Exec(ctx, sourceSQL(q, `INSERT INTO sso_mutation_intents(tenant_id,actor_user_id,idempotency_key,operation,target_id,fingerprint,result) VALUES($1,$2,$3,$4,$5,$6,$7)`, `INSERT INTO sso_mutation_intents(tenant_id,actor_user_id,idempotency_key,operation,target_id,fingerprint,result) VALUES(?,?,?,?,?,?,?)`, "INSERT INTO SSO_MUTATION_INTENTS(TENANT_ID,ACTOR_USER_ID,IDEMPOTENCY_KEY,OPERATION,TARGET_ID,FINGERPRINT,RESULT) VALUES(?,?,?,?,?,?,?)", tenant, a.Me.User.ID, key, unlinkBindingOperation, result.ID, fingerprint, string(raw)))
	if e == nil && changed != 1 {
		return fail("STORAGE_UNAVAILABLE")
	}
	return e
}

func selectBindingWithin(ctx context.Context, q transaction.Executor, tenant, user, id string) (Binding, error) {
	return scanBinding(q.QueryRow(ctx, sourceSQL(q,
		`SELECT `+bindingColumns+` FROM sso_bindings b JOIN sso_connectors c ON c.tenant_id=b.tenant_id AND c.id=b.connector_id WHERE b.tenant_id=$1 AND b.user_id=$2 AND b.id=$3`,
		`SELECT `+mysqlBindingColumns+` FROM sso_bindings b JOIN sso_connectors c ON c.tenant_id=b.tenant_id AND c.id=b.connector_id WHERE b.tenant_id=? AND b.user_id=? AND b.id=?`, "SELECT "+damengBindingColumns+" FROM SSO_BINDINGS B JOIN SSO_CONNECTORS C ON C.TENANT_ID=B.TENANT_ID AND C.ID=B.CONNECTOR_ID WHERE B.TENANT_ID=? AND B.USER_ID=? AND B.ID=?", tenant, user, id)))
}

func (s *Service) ListBindings(ctx context.Context, a identity.Auth) ([]Binding, error) {
	empty := []Binding{}
	result := empty
	e := s.actorTransaction(ctx, a, connectorReadFence, connectorShared, nil, func(ctx context.Context, q transaction.Executor, tenant string) error {
		rows, e := q.Query(ctx, sourceBoundedSQL(q,
			`SELECT `+bindingColumns+` FROM sso_bindings b JOIN sso_connectors c ON c.tenant_id=b.tenant_id AND c.id=b.connector_id WHERE b.tenant_id=$1 AND b.user_id=$2 ORDER BY b.created_at,b.id LIMIT $3`,
			`SELECT `+mysqlBindingColumns+` FROM sso_bindings b JOIN sso_connectors c ON c.tenant_id=b.tenant_id AND c.id=b.connector_id WHERE b.tenant_id=? AND b.user_id=? ORDER BY b.created_at,b.id LIMIT ?`,
			"SELECT B.ID,B.CONNECTOR_ID,C.NAME,C.PROVIDER,B.SUBJECT,B.ACTIVE,B.REVISION,B.CREATED_AT,B.UPDATED_AT FROM SSO_BINDINGS B JOIN SSO_CONNECTORS C ON C.TENANT_ID=B.TENANT_ID AND C.ID=B.CONNECTOR_ID WHERE B.TENANT_ID=? AND B.USER_ID=? ORDER BY B.CREATED_AT,B.ID LIMIT ",
			"",
			maximumBindingInventory+1,
			maximumBindingInventory+1,
			tenant,
			a.Me.User.ID))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			item, e := scanBinding(rows)
			if e != nil {
				return e
			}
			if !validBinding(item) || len(result) >= maximumBindingInventory {
				return fail("STORAGE_UNAVAILABLE")
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	if e != nil {
		return empty, e
	}
	return result, nil
}

func (s *Service) Unlink(ctx context.Context, a identity.Auth, id string, input UnlinkBinding, key, requestID string) (Binding, error) {
	if !canonicalID(id) || !canonicalID(key) || !canonicalID(requestID) || input.Revision < 1 || input.Revision >= identity.MaximumAuthVersion || !validReason(input.Reason) {
		return Binding{}, fail("VALIDATION_FAILED")
	}
	if !validSSOActor(a) {
		return Binding{}, fail("AUTH_SESSION_EXPIRED")
	}
	if _, e := s.catalogRepository(); e != nil {
		return Binding{}, e
	}
	if e := s.policy.Validate(); e != nil {
		return Binding{}, e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy.OperationSeconds))
	defer cancel()
	proof, e := s.Identity.ConfirmPassword(ctx, a, input.Password)
	if e != nil {
		return Binding{}, e
	}
	fingerprint, e := s.fingerprint(unlinkBindingOperation, id, input)
	if e != nil {
		return Binding{}, e
	}
	resolve := func(ctx context.Context, q transaction.Executor, tenant string) ([]connectorLock, error) {
		var connector string
		e := q.QueryRow(ctx, sourceSQL(q, `SELECT connector_id::text FROM sso_bindings WHERE tenant_id=$1 AND id=$2 AND user_id=$3`, `SELECT connector_id FROM sso_bindings WHERE tenant_id=? AND id=? AND user_id=?`, "SELECT CONNECTOR_ID FROM SSO_BINDINGS WHERE TENANT_ID=? AND ID=? AND USER_ID=?", tenant, id, a.Me.User.ID)).Scan(&connector)
		if errors.Is(e, sql.ErrNoRows) {
			// Current authority is rechecked before the operation discloses the
			// actor-scoped absence. Binding ownership/connector are immutable.
			return nil, nil
		}
		return []connectorLock{{tenant, connector}}, e
	}
	var result Binding
	e = s.actorTransaction(ctx, a, connectorReadFence, connectorShared, resolve, func(ctx context.Context, q transaction.Executor, tenant string) error {
		if e := s.Identity.RecheckConfirmationWithin(ctx, q, a, proof); e != nil {
			return e
		}
		var replayed bool
		var e error
		result, replayed, e = replayBindingIntent(ctx, q, tenant, a, key, id, fingerprint)
		if e != nil || replayed {
			return e
		}
		var connector string
		var active bool
		var revision int64
		e = q.QueryRow(ctx, sourceSQL(q, `SELECT connector_id::text,active,revision FROM sso_bindings WHERE tenant_id=$1 AND id=$2 AND user_id=$3 FOR UPDATE`, `SELECT connector_id,active,revision FROM sso_bindings WHERE tenant_id=? AND id=? AND user_id=? FOR UPDATE`, "SELECT CONNECTOR_ID,ACTIVE,REVISION FROM SSO_BINDINGS WHERE TENANT_ID=? AND ID=? AND USER_ID=? FOR UPDATE OF ID", tenant, id, a.Me.User.ID)).Scan(&connector, &active, &revision)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("SSO_BINDING_NOT_FOUND")
		}
		if e != nil {
			return e
		}
		if !active || revision != input.Revision || revision >= identity.MaximumAuthVersion {
			return fail("SSO_REVISION_CONFLICT")
		}
		changed, e := q.Exec(ctx, sourceSQL(q, `UPDATE sso_bindings SET active=false,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND id=$2 AND user_id=$3 AND revision=$4 AND active`, `UPDATE sso_bindings SET active=false,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND id=? AND user_id=? AND revision=? AND active=1`, "UPDATE SSO_BINDINGS SET ACTIVE=0,REVISION=REVISION+1,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE TENANT_ID=? AND ID=? AND USER_ID=? AND REVISION=? AND ACTIVE=1", tenant, id, a.Me.User.ID, input.Revision))
		if e != nil {
			return e
		}
		if changed != 1 {
			return fail("SSO_REVISION_CONFLICT")
		}
		if _, e = q.Exec(ctx, sourceSQL(q, `UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='sso_binding_unlinked' WHERE sso_tenant_id=$1 AND sso_binding_id=$2 AND user_id=$3 AND revoked_at IS NULL`, `UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='sso_binding_unlinked' WHERE sso_tenant_id=? AND sso_binding_id=? AND user_id=? AND revoked_at IS NULL`, "UPDATE SESSIONS SET REVOKED_AT=CURRENT_TIMESTAMP(6),REVOKE_REASON='sso_binding_unlinked' WHERE SSO_TENANT_ID=? AND SSO_BINDING_ID=? AND USER_ID=? AND REVOKED_AT IS NULL", tenant, id, a.Me.User.ID)); e != nil {
			return e
		}
		if _, e = q.Exec(ctx, sourceSQL(q, `UPDATE sso_flows SET state='failed',error_code='SSO_FLOW_UNUSABLE',finished_at=clock_timestamp(),verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE tenant_id=$1 AND connector_id=$2 AND initiating_user_id=$3 AND state IN('pending','claimed')`, `UPDATE sso_flows SET state='failed',error_code='SSO_FLOW_UNUSABLE',finished_at=UTC_TIMESTAMP(6),verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE tenant_id=? AND connector_id=? AND initiating_user_id=? AND state IN('pending','claimed')`, "UPDATE SSO_FLOWS SET STATE='failed',ERROR_CODE='SSO_FLOW_UNUSABLE',FINISHED_AT=CURRENT_TIMESTAMP(6),VERIFIER_KEY_ID=NULL,VERIFIER_NONCE=NULL,VERIFIER_CIPHERTEXT=NULL WHERE TENANT_ID=? AND CONNECTOR_ID=? AND INITIATING_USER_ID=? AND STATE IN('pending','claimed')", tenant, connector, a.Me.User.ID)); e != nil {
			return e
		}
		result, e = selectBindingWithin(ctx, q, tenant, a.Me.User.ID, id)
		if e != nil {
			return e
		}
		if e = saveBindingIntent(ctx, q, tenant, a, key, fingerprint, result); e != nil {
			return e
		}
		return audit.WriteWithin(ctx, q, a.Me.User.ID, a.Me.User.ID, a.Me.Audience, "sso.binding_unlinked", "success", "sso_binding", id, input.Reason, requestID)
	})
	if e != nil {
		return Binding{}, e
	}
	return result, nil
}
