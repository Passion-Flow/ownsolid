package distribution

import (
	"context"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"time"
)

// WithTransientKey commits one private accepted-operation key before trusted IO,
// then independently reduces only exact proven possession. Never expose its secret.
func (s *Service) WithTransientKey(ctx context.Context, a identity.Auth, input KeyRequest, cleanup time.Duration, request string, use func(string) error) (result error) {
	if use == nil || cleanup < time.Second || cleanup > 30*time.Second {
		return fail("VALIDATION_FAILED")
	}
	if _, e := s.repository(); e != nil {
		return distributionError(e)
	}
	if e := s.Identity.RequireBusinessIntegrity(ctx); e != nil {
		return e
	}
	minted, e := s.mintKey(ctx, a, input, uuid.NewString(), request, true)
	if e != nil {
		return e
	}
	if !minted.Created || !minted.SecretAvailable || minted.Secret == nil {
		return fail("GATEWAY_CREDENTIAL_INVALID")
	}
	raw := *minted.Secret
	defer func() {
		independent, cancel := context.WithTimeout(context.Background(), cleanup)
		defer cancel()
		if s.revokeTransient(independent, a, minted.Key, raw, request) != nil {
			result = fail("TRANSIENT_KEY_CLEANUP_FAILED")
		}
	}()
	return use(raw)
}
func (s *Service) revokeTransient(ctx context.Context, a identity.Auth, key APIKey, raw, request string) error {
	id, token, e := parseCredential(apiKeyPrefix, raw)
	if e != nil || id != key.ID || key.CreatedBy != a.Me.User.ID {
		return fail("GATEWAY_CREDENTIAL_INVALID")
	}
	return s.storageTransaction(ctx, func(ctx context.Context, q transaction.Executor) error {
		var owner string
		if e := q.QueryRow(ctx, distributionNativeSQL(q, `SELECT ID FROM USERS WHERE ID=? FOR UPDATE`, "SELECT id::text FROM users WHERE id=$1 FOR UPDATE", "SELECT id FROM users WHERE id=? FOR UPDATE", key.CreatedBy)).Scan(&owner); e != nil {
			return e
		}
		w := workspaces.Workspace{ID: key.WorkspaceID}
		if e := q.QueryRow(ctx, distributionNativeSQL(q, `SELECT TENANT_ID FROM WORKSPACES WHERE ID=? FOR UPDATE`, "SELECT tenant_id::text FROM workspaces WHERE id=$1 FOR NO KEY UPDATE", "SELECT tenant_id FROM workspaces WHERE id=? FOR UPDATE", w.ID)).Scan(&w.TenantID); e != nil {
			return e
		}
		var revoked bool
		e := q.QueryRow(ctx, distributionNativeSQL(q, `SELECT CASE WHEN REVOKED_AT IS NOT NULL THEN 1 ELSE 0 END FROM API_KEYS WHERE ID=? AND TENANT_ID=? AND WORKSPACE_ID=? AND CREATED_BY=? AND SECRET_HASH=? FOR UPDATE`, "SELECT revoked_at IS NOT NULL FROM api_keys WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3 AND created_by=$4 AND secret_hash=$5 FOR UPDATE", "SELECT revoked_at IS NOT NULL FROM api_keys WHERE id=? AND tenant_id=? AND workspace_id=? AND created_by=? AND secret_hash=? FOR UPDATE", key.ID, w.TenantID, w.ID, key.CreatedBy, s.digest(operationKeyPurpose, id, token))).Scan(&revoked)
		if e != nil {
			return e
		}
		if !revoked {
			if _, e = q.Exec(ctx, distributionNativeStatement(q, `UPDATE API_KEYS SET REVOKED_AT=CURRENT_TIMESTAMP(6),REVOKE_REASON=? WHERE ID=?`, []any{"Transient application operation completed", key.ID}, "UPDATE api_keys SET revoked_at=clock_timestamp(),revoke_reason=$2 WHERE id=$1", "UPDATE api_keys SET revoked_at=UTC_TIMESTAMP(6),revoke_reason=? WHERE id=?", []any{key.ID, "Transient application operation completed"}, []any{"Transient application operation completed", key.ID})); e != nil {
				return e
			}
			return s.audit(ctx, q, a, w, "gateway.api_key_revoked", "api_key", key.ID, "Transient application operation completed", request)
		}
		return nil
	})
}
