package distribution

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"strings"
)

const apiKeyPrefix = "pk1"
// gatewayCredentialPrefix starts a gateway's credential, whether control or the node's agent
// generated it.
const gatewayCredentialPrefix = agentwire.GatewayCredentialPrefix
const ordinaryKeyPurpose = "api-key"
const operationKeyPurpose = "accepted-operation-key"
const maximumKeyTTLSeconds = 90 * 24 * 60 * 60
const keyFields = `k.id::text,k.workspace_id::text,k.alias_id::text,a.name,k.label,k.created_by::text,CASE WHEN k.revoked_at IS NOT NULL THEN 'revoked' WHEN k.expires_at<=clock_timestamp() THEN 'expired' WHEN k.requested_epoch<>u.auth_epoch OR u.status<>'active' OR NOT EXISTS(SELECT 1 FROM workspace_memberships m WHERE m.tenant_id=k.tenant_id AND m.workspace_id=k.workspace_id AND m.user_id=k.created_by AND m.authority_id=k.membership_authority) THEN 'invalidated' WHEN w.status<>'active' THEN 'unavailable' ELSE 'active' END,k.created_at,k.expires_at,k.last_used_at,k.revoked_at,k.rotated_from::text,(SELECT s.id::text FROM api_keys s WHERE s.rotated_from=k.id)`
const keyJoins = ` FROM api_keys k JOIN model_aliases a ON a.id=k.alias_id AND a.tenant_id=k.tenant_id AND a.workspace_id=k.workspace_id JOIN users u ON u.id=k.created_by JOIN workspaces w ON w.id=k.workspace_id AND w.tenant_id=k.tenant_id`

func scanKey(row transaction.Row) (APIKey, error) {
	var out APIKey
	e := row.Scan(&out.ID, &out.WorkspaceID, &out.AliasID, &out.AliasName, &out.Label, &out.CreatedBy, &out.State, &out.CreatedAt, &out.ExpiresAt, &out.LastUsedAt, &out.RevokedAt, &out.RotatedFrom, &out.ReplacedBy)
	if errors.Is(e, sql.ErrNoRows) {
		return APIKey{}, fail("RESOURCE_NOT_FOUND")
	}
	out.Prefix = apiKeyPrefix + "." + out.ID
	if e != nil {
		return APIKey{}, e
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.ExpiresAt = out.ExpiresAt.UTC()
	out.LastUsedAt = distributionUTCPointer(out.LastUsedAt)
	out.RevokedAt = distributionUTCPointer(out.RevokedAt)
	return out, nil
}

func parseCredential(prefix, value string) (string, string, error) {
	pieces := strings.Split(value, ".")
	if len(pieces) != 3 || pieces[0] != prefix || !wire.ValidID(pieces[1]) || !security.ValidToken(pieces[2]) {
		return "", "", fail("GATEWAY_CREDENTIAL_INVALID")
	}
	return pieces[1], pieces[2], nil
}

func (s *Service) MintKey(ctx context.Context, a identity.Auth, input KeyRequest, key, request string) (MintedKey, error) {
	return s.mintKey(ctx, a, input, key, request, false)
}

// Only trusted application continuation code selects the private MAC domain.
// No public DTO, label, credential prefix or caller-provided ID selects it.
func (s *Service) mintKey(ctx context.Context, a identity.Auth, input KeyRequest, key, request string, operation bool) (MintedKey, error) {
	if !wire.ValidID(input.AliasID) || !wire.ValidID(key) || !reason(input.Reason) || input.TTLSeconds < 0 || input.TTLSeconds > maximumKeyTTLSeconds {
		return MintedKey{}, fail("VALIDATION_FAILED")
	}
	label, e := security.Name(input.Label)
	if e != nil {
		return MintedKey{}, e
	}
	input.Label = label
	var out MintedKey
	e = s.transaction(ctx, a, input.WorkspaceID, true, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if w.Role == nil {
			return fail("ACCESS_DENIED")
		}
		expected := fingerprint(a.Me.User.ID, input)
		var existing string
		var stored []byte
		e := tx.QueryRow(ctx, distributionNativeStatement(tx, `SELECT ID,FINGERPRINT FROM API_KEYS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND CREATED_BY=? AND IDEMPOTENCY_KEY=?`, []any{w.TenantID, w.ID, a.Me.User.ID, key}, "SELECT id::text,fingerprint FROM api_keys WHERE tenant_id=$1 AND workspace_id=$2 AND created_by=$3 AND idempotency_key=$4", `SELECT id,fingerprint FROM api_keys WHERE tenant_id=? AND workspace_id=? AND created_by=? AND idempotency_key=?`, []any{w.TenantID, w.ID, a.Me.User.ID, key}, []any{w.TenantID, w.ID, a.Me.User.ID, key})).Scan(&existing, &stored)
		if e == nil {
			if !same(stored, expected) {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			out.Key, e = scanKey(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeKeyFields+nativeKeyJoins+" WHERE K.ID=?", []any{existing}, "SELECT "+keyFields+keyJoins+" WHERE k.id=$1", `SELECT `+mysqlKeyFields+keyJoins+` WHERE k.id=?`, []any{existing}, []any{existing})))
			return e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		authority, e := s.Workspaces.MembershipAuthorityWithin(ctx, tx, w.TenantID, w.ID, a.Me.User.ID)
		if e != nil {
			return e
		}
		policy := s.Identity.Config.Policy().Distribution
		ttl := input.TTLSeconds
		if ttl == 0 {
			ttl = policy.KeyTTLSeconds
		}
		if ttl < 300 || ttl > policy.KeyMaxTTLSeconds {
			return fail("VALIDATION_FAILED")
		}
		alias, e := scanAlias(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeAliasFields+" FROM MODEL_ALIASES WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=? FOR UPDATE", []any{w.TenantID, w.ID, input.AliasID}, "SELECT "+aliasFields+" FROM model_aliases WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE", `SELECT `+mysqlAliasFields+` FROM model_aliases WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE`, []any{w.TenantID, w.ID, input.AliasID}, []any{w.TenantID, w.ID, input.AliasID})))
		if e != nil {
			return e
		}
		if e = s.aliasAvailability(ctx, tx, w, &alias); e != nil {
			return e
		}
		if !alias.Available {
			return fail("MODEL_ALIAS_UNAVAILABLE")
		}
		var count int
		if e = tx.QueryRow(ctx, distributionNativeStatement(tx, `SELECT COUNT(*) FROM API_KEYS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND CREATED_BY=? AND REQUESTED_EPOCH=? AND MEMBERSHIP_AUTHORITY=? AND REVOKED_AT IS NULL AND EXPIRES_AT>CURRENT_TIMESTAMP(6)`, []any{w.TenantID, w.ID, a.Me.User.ID, a.Epoch, authority}, "SELECT count(*) FROM api_keys WHERE tenant_id=$1 AND workspace_id=$2 AND created_by=$3 AND requested_epoch=$4 AND membership_authority=$5 AND revoked_at IS NULL AND expires_at>clock_timestamp()", `SELECT count(*) FROM api_keys WHERE tenant_id=? AND workspace_id=? AND created_by=? AND requested_epoch=? AND membership_authority=? AND revoked_at IS NULL AND expires_at>UTC_TIMESTAMP(6)`, []any{w.TenantID, w.ID, a.Me.User.ID, a.Epoch, authority}, []any{w.TenantID, w.ID, a.Me.User.ID, a.Epoch, authority})).Scan(&count); e != nil {
			return e
		}
		if count >= policy.KeyMaxActive {
			return fail("RESOURCE_LIMIT_REACHED")
		}
		id := uuid.NewString()
		token := security.Token()
		raw := apiKeyPrefix + "." + id + "." + token
		purpose := ordinaryKeyPurpose
		if operation {
			purpose = operationKeyPurpose
		}
		_, e = distributionExecOne(ctx, tx, distributionNativeStatement(tx, `INSERT INTO API_KEYS(ID,TENANT_ID,WORKSPACE_ID,ALIAS_ID,LABEL,SECRET_HASH,CREATED_BY,REQUESTED_EPOCH,IDEMPOTENCY_KEY,FINGERPRINT,EXPIRES_AT,MEMBERSHIP_AUTHORITY) VALUES(?,?,?,?,?,?,?,?,?,?,TIMESTAMPADD(SECOND,?,CURRENT_TIMESTAMP(6)),?)`, []any{id, w.TenantID, w.ID, alias.ID, input.Label, s.digest(purpose, id, token), a.Me.User.ID, a.Epoch, key, expected, ttl, authority}, "INSERT INTO api_keys(id,tenant_id,workspace_id,alias_id,label,secret_hash,created_by,requested_epoch,idempotency_key,fingerprint,expires_at,membership_authority) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,clock_timestamp()+make_interval(secs=>$11),$12)", `INSERT INTO api_keys(id,tenant_id,workspace_id,alias_id,label,secret_hash,created_by,requested_epoch,idempotency_key,fingerprint,expires_at,membership_authority) VALUES(?,?,?,?,?,?,?,?,?,?,TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),?)`, []any{id, w.TenantID, w.ID, alias.ID, input.Label, s.digest(purpose, id, token), a.Me.User.ID, a.Epoch, key, expected, ttl, authority}, []any{id, w.TenantID, w.ID, alias.ID, input.Label, s.digest(purpose, id, token), a.Me.User.ID, a.Epoch, key, expected, ttl, authority}))
		if e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "gateway.api_key_created", "api_key", id, input.Reason, request); e != nil {
			return e
		}
		out.Key, e = scanKey(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeKeyFields+nativeKeyJoins+" WHERE K.ID=?", []any{id}, "SELECT "+keyFields+keyJoins+" WHERE k.id=$1", `SELECT `+mysqlKeyFields+keyJoins+` WHERE k.id=?`, []any{id}, []any{id})))
		if e != nil {
			return e
		}
		out.Secret = &raw
		out.SecretAvailable = true
		out.Created = true
		return nil
	})
	if e != nil {
		return MintedKey{}, e
	}
	return out, nil
}

func (s *Service) Keys(ctx context.Context, a identity.Auth, workspace string) ([]APIKey, error) {
	out := []APIKey{}
	e := s.transaction(ctx, a, workspace, false, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		rows, e := tx.Query(ctx, distributionPageStatement(tx, "SELECT "+nativeKeyFields+nativeKeyJoins+" WHERE K.TENANT_ID=? AND K.WORKSPACE_ID=? AND (?=1 OR K.CREATED_BY=?) ORDER BY K.CREATED_AT DESC,K.ID DESC", []any{w.TenantID, w.ID, admin(a), a.Me.User.ID, s.Identity.Config.Policy().UI.LookupPageSize}, "SELECT "+keyFields+keyJoins+" WHERE k.tenant_id=$1 AND k.workspace_id=$2 AND ($3 OR k.created_by=$4) ORDER BY k.created_at DESC,k.id DESC LIMIT $5", `SELECT `+mysqlKeyFields+keyJoins+` WHERE k.tenant_id=? AND k.workspace_id=? AND (? OR k.created_by=?) ORDER BY k.created_at DESC,k.id DESC LIMIT ?`, []any{w.TenantID, w.ID, admin(a), a.Me.User.ID, s.Identity.Config.Policy().UI.LookupPageSize}, []any{w.TenantID, w.ID, admin(a), a.Me.User.ID, s.Identity.Config.Policy().UI.LookupPageSize}))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			outKey, e := scanKey(rows)
			if e != nil {
				return e
			}
			out = append(out, outKey)
		}
		return rows.Err()
	})
	if e != nil {
		return []APIKey{}, e
	}
	return out, nil
}

func (s *Service) RevokeKey(ctx context.Context, a identity.Auth, workspace, id, explanation, request string) (APIKey, error) {
	if !wire.ValidID(id) || !wire.ValidID(workspace) || !reason(explanation) {
		return APIKey{}, fail("VALIDATION_FAILED")
	}
	if _, e := s.Workspaces.Get(ctx, a, workspace, false); e != nil {
		return APIKey{}, e
	}
	owner, e := s.keyOwner(ctx, a, workspace, id)
	if e != nil {
		return APIKey{}, e
	}
	var out APIKey
	e = s.transaction(ctx, a, workspace, false, []string{owner}, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		current, e := scanKey(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeKeyFields+nativeKeyJoins+" WHERE K.TENANT_ID=? AND K.WORKSPACE_ID=? AND K.ID=? AND (?=1 OR K.CREATED_BY=?) FOR UPDATE", []any{w.TenantID, w.ID, id, admin(a), a.Me.User.ID}, "SELECT "+keyFields+keyJoins+" WHERE k.tenant_id=$1 AND k.workspace_id=$2 AND k.id=$3 AND ($4 OR k.created_by=$5) FOR UPDATE OF k", `SELECT `+mysqlKeyFields+keyJoins+` WHERE k.tenant_id=? AND k.workspace_id=? AND k.id=? AND (? OR k.created_by=?) FOR UPDATE`, []any{w.TenantID, w.ID, id, admin(a), a.Me.User.ID}, []any{w.TenantID, w.ID, id, admin(a), a.Me.User.ID})))
		if e != nil {
			return e
		}
		if current.RevokedAt != nil {
			out = current
			return nil
		}
		if _, e = distributionExecOne(ctx, tx, distributionNativeStatement(tx, `UPDATE API_KEYS SET REVOKED_AT=CURRENT_TIMESTAMP(6),REVOKE_REASON=? WHERE ID=?`, []any{explanation, id}, "UPDATE api_keys SET revoked_at=clock_timestamp(),revoke_reason=$2 WHERE id=$1", `UPDATE api_keys SET revoked_at=UTC_TIMESTAMP(6),revoke_reason=? WHERE id=?`, []any{id, explanation}, []any{explanation, id})); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "gateway.api_key_revoked", "api_key", id, explanation, request); e != nil {
			return e
		}
		out, e = scanKey(tx.QueryRow(ctx, distributionNativeStatement(tx, "SELECT "+nativeKeyFields+nativeKeyJoins+" WHERE K.ID=?", []any{id}, "SELECT "+keyFields+keyJoins+" WHERE k.id=$1", `SELECT `+mysqlKeyFields+keyJoins+` WHERE k.id=?`, []any{id}, []any{id})))
		return e
	})
	if e != nil {
		return APIKey{}, e
	}
	return out, nil
}
