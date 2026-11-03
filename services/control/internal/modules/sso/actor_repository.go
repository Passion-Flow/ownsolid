package sso

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type connectorLockMode uint8

const (
	connectorShared connectorLockMode = iota
	connectorExclusive
)

type actorTargets func(context.Context, transaction.Executor, string) ([]connectorLock, error)

func validSSOActor(a identity.Auth) bool {
	return canonicalID(a.Me.User.ID) && canonicalID(a.SessionID) && len(a.TokenHash) == sha256.Size && a.Epoch >= 1 && (a.Me.Audience == "portal" || a.Me.Audience == "admin")
}

// actorTransaction orders only this SSO operation's actor,source and resolved
// target connectors. It never locks installation/session rows before the user
// or a session before its source connector's bulk-revocation fence.
func (s *Service) actorTransaction(ctx context.Context, a identity.Auth, fence connectorFence, strength connectorLockMode, targets actorTargets, operation func(context.Context, transaction.Executor, string) error) error {
	store, e := s.catalogRepository()
	if e != nil {
		return e
	}
	if !validSSOActor(a) {
		return fail("AUTH_SESSION_EXPIRED")
	}
	if operation == nil || fence != connectorReadFence && fence != connectorCreateFence || strength != connectorShared && strength != connectorExclusive {
		return fail("SSO_TRANSACTION_STORAGE_MISMATCH")
	}
	if e := s.policy.Validate(); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy.OperationSeconds))
	defer cancel()
	return sourceOutcome(store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := s.Identity.LockSessionUsersWithin(ctx, q, a.Me.User.ID, "", a.Me.Audience); e != nil {
			return e
		}
		// Plain source lookup is intentional. Holding this session row while
		// waiting for its connector can cycle against bulk session revocation.
		var sourceTenant, sourceConnector sql.NullString
		e := q.QueryRow(ctx, sourceSQL(q,
			`SELECT sso_tenant_id::text,sso_connector_id::text FROM sessions WHERE id=$1 AND user_id=$2 AND token_hash=$3 AND audience=$4 AND revoked_at IS NULL`,
			`SELECT sso_tenant_id,sso_connector_id FROM sessions WHERE id=? AND user_id=? AND token_hash=? AND audience=? AND revoked_at IS NULL`, "SELECT SSO_TENANT_ID,SSO_CONNECTOR_ID FROM SESSIONS WHERE ID=? AND USER_ID=? AND TOKEN_HASH=? AND AUDIENCE=? AND REVOKED_AT IS NULL", a.SessionID, a.Me.User.ID, a.TokenHash, a.Me.Audience)).Scan(&sourceTenant, &sourceConnector)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("AUTH_SESSION_EXPIRED")
		}
		if e != nil {
			return e
		}
		if sourceTenant.Valid != sourceConnector.Valid || sourceTenant.Valid && (!canonicalID(sourceTenant.String) || !canonicalID(sourceConnector.String)) {
			return fail("AUTH_SESSION_EXPIRED")
		}
		tenant, e := connectorTenant(ctx, q, connectorPeek)
		if e != nil {
			return e
		}
		locks := []connectorLock{}
		if targets != nil {
			locks, e = targets(ctx, q, tenant)
			if e != nil {
				return e
			}
		}
		if sourceTenant.Valid {
			locks = append(locks, connectorLock{sourceTenant.String, sourceConnector.String})
		}
		suffix := " FOR SHARE"
		if strength == connectorExclusive {
			suffix = " FOR UPDATE"
		}
		for _, value := range connectorLocks(locks) {
			if !canonicalID(value.tenant) || !canonicalID(value.id) {
				return fail("STORAGE_UNAVAILABLE")
			}
			var found string
			e := q.QueryRow(ctx, sourceSQL(q, `SELECT id::text FROM sso_connectors WHERE tenant_id=$1 AND id=$2`+suffix, `SELECT id FROM sso_connectors WHERE tenant_id=? AND id=?`+suffix, "SELECT ID FROM SSO_CONNECTORS WHERE TENANT_ID=? AND ID=? FOR UPDATE OF ID", value.tenant, value.id)).Scan(&found)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
		}
		if e := s.Identity.RecheckWithin(ctx, q, a); e != nil {
			return e
		}
		current, e := connectorTenant(ctx, q, fence)
		if e != nil {
			return e
		}
		if current != tenant {
			return fail("SSO_REVISION_CONFLICT")
		}
		return operation(ctx, q, current)
	}))
}
