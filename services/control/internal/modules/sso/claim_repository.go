package sso

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

// claimPreviousSession never acquires a user row lock behind connector/flow.
// User predicates are plain EXISTS reads;the outer shared lock targets se only.
func (s *Service) claimPreviousSession(ctx context.Context, q transaction.Executor, a identity.Auth) error {
	var id string
	e := q.QueryRow(ctx, sourceSQL(q,
		`SELECT se.id::text FROM sessions se WHERE se.id=$1 AND se.user_id=$2 AND se.token_hash=$3 AND se.audience=$4 AND se.auth_epoch=$5 AND se.revoked_at IS NULL AND se.absolute_expires_at>clock_timestamp() AND se.last_seen_at>clock_timestamp()-$6::bigint*interval '1 microsecond' AND EXISTS(SELECT 1 FROM users u WHERE u.id=se.user_id AND u.status='active' AND u.auth_epoch=se.auth_epoch AND ($4='portal' OR u.is_platform_admin)) FOR SHARE OF se`,
		`SELECT se.id FROM sessions se WHERE se.id=? AND se.user_id=? AND se.token_hash=? AND se.audience=? AND se.auth_epoch=? AND se.revoked_at IS NULL AND se.absolute_expires_at>UTC_TIMESTAMP(6) AND se.last_seen_at>TIMESTAMPADD(MICROSECOND,-?,UTC_TIMESTAMP(6)) AND EXISTS(SELECT 1 FROM users u WHERE u.id=se.user_id AND u.status='active' AND u.auth_epoch=se.auth_epoch AND (se.audience='portal' OR u.is_platform_admin=1)) FOR SHARE`, "SELECT SE.ID FROM SESSIONS SE WHERE SE.ID=? AND SE.USER_ID=? AND SE.TOKEN_HASH=? AND SE.AUDIENCE=? AND SE.AUTH_EPOCH=? AND SE.REVOKED_AT IS NULL AND SE.ABSOLUTE_EXPIRES_AT>CURRENT_TIMESTAMP(6) AND SE.LAST_SEEN_AT>CURRENT_TIMESTAMP(6)-NUMTODSINTERVAL(?/1000000.0,'SECOND') AND EXISTS(SELECT 1 FROM USERS U WHERE U.ID=SE.USER_ID AND U.STATUS='active' AND U.AUTH_EPOCH=SE.AUTH_EPOCH AND (SE.AUDIENCE='portal' OR U.IS_PLATFORM_ADMIN=1)) FOR UPDATE OF SE.ID", a.SessionID, a.Me.User.ID, a.TokenHash, a.Me.Audience, a.Epoch, s.Identity.Idle(a.Me.Audience).Microseconds())).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return fail("SSO_BROWSER_CHANGED")
	}
	return e
}

func (s *Service) claim(ctx context.Context, connector, audience, state, browser, previous string) (claimedFlow, error) {
	if s == nil || s.Identity == nil {
		return claimedFlow{}, fail("SSO_CONFIGURATION_REQUIRED")
	}
	if !s.enabled {
		return claimedFlow{}, fail("SSO_DISABLED")
	}
	if !canonicalID(connector) || (audience != "portal" && audience != "admin") || !security.ValidToken(state) || !security.ValidToken(browser) {
		return claimedFlow{}, fail("SSO_FLOW_INVALID")
	}
	store, e := s.catalogRepository()
	if e != nil {
		return claimedFlow{}, sourceOutcome(e)
	}
	if e := s.policy.Validate(); e != nil {
		return claimedFlow{}, sourceOutcome(e)
	}
	if s.keys == nil && !s.keyringRecovery {
		return claimedFlow{}, fail("SSO_CONFIGURATION_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy.OperationSeconds))
	defer cancel()
	f, e := scanFlow(store.QueryRow(ctx, sourceSQL(store, `SELECT `+flowColumns+` FROM sso_flows WHERE connector_id=$1 AND audience=$2 AND state_hash=$3`, `SELECT `+mysqlFlowColumns+` FROM sso_flows WHERE connector_id=? AND audience=? AND state_hash=?`, "SELECT "+damengFlowColumns+" FROM SSO_FLOWS WHERE CONNECTOR_ID=? AND AUDIENCE=? AND STATE_HASH=?", connector, audience, security.Digest(state))))
	if errors.Is(e, sql.ErrNoRows) {
		return claimedFlow{}, fail("SSO_FLOW_INVALID")
	}
	if e != nil {
		return claimedFlow{}, sourceOutcome(e)
	}
	if !validPrivateFlow(f) {
		return claimedFlow{}, fail("STORAGE_UNAVAILABLE")
	}
	if !hmac.Equal(f.browserHash, s.browserProof(connector, audience, browser)) || !hmac.Equal(f.previousHash, security.Digest(security.Binding(previous))) {
		return claimedFlow{}, fail("SSO_BROWSER_CHANGED")
	}
	var previousActor *identity.Auth
	if f.previousSession != nil {
		a, e := s.Identity.Authenticate(ctx, previous, audience)
		if e != nil || a.SessionID != *f.previousSession {
			if e != nil && e.Error() != "AUTH_REQUIRED" && e.Error() != "AUTH_SESSION_EXPIRED" {
				return claimedFlow{}, sourceOutcome(e)
			}
			return claimedFlow{}, fail("SSO_BROWSER_CHANGED")
		}
		previousActor = &a
	}
	var result claimedFlow
	var unreadable bool
	operation := func(ctx context.Context, q transaction.Executor, current string) error {
		c, e := selectConnectorWithin(ctx, q, f.tenant, connector)
		if e != nil {
			return e
		}
		if current != f.tenant || !c.Enabled || c.Revision != f.connectorRevision || c.AuthVersion != f.authVersion {
			return fail("SSO_FLOW_UNUSABLE")
		}
		if _, e := s.provider(connector, c.Origin); e != nil {
			return e
		}
		locked, e := scanFlow(q.QueryRow(ctx, sourceSQL(q, `SELECT `+flowColumns+` FROM sso_flows WHERE id=$1 AND tenant_id=$2 AND connector_id=$3 FOR UPDATE`, `SELECT `+mysqlFlowColumns+` FROM sso_flows WHERE id=? AND tenant_id=? AND connector_id=? FOR UPDATE`, "SELECT "+damengFlowColumns+" FROM SSO_FLOWS WHERE ID=? AND TENANT_ID=? AND CONNECTOR_ID=? FOR UPDATE OF ID", f.id, f.tenant, connector)))
		if e != nil {
			return e
		}
		if !validPrivateFlow(locked) {
			return fail("STORAGE_UNAVAILABLE")
		}
		if locked.state != flowPending {
			return fail("SSO_FLOW_UNUSABLE")
		}
		var fresh bool
		if e := q.QueryRow(ctx, sourceSQL(q, `SELECT expires_at>clock_timestamp() FROM sso_flows WHERE tenant_id=$1 AND id=$2`, `SELECT expires_at>UTC_TIMESTAMP(6) FROM sso_flows WHERE tenant_id=? AND id=?`, "SELECT CASE WHEN EXPIRES_AT>CURRENT_TIMESTAMP(6) THEN 1 ELSE 0 END FROM SSO_FLOWS WHERE TENANT_ID=? AND ID=?", f.tenant, f.id)).Scan(&fresh); e != nil {
			return e
		}
		if !fresh {
			return fail("SSO_FLOW_EXPIRED")
		}
		if previousActor != nil {
			if e := s.claimPreviousSession(ctx, q, *previousActor); e != nil {
				return e
			}
		}
		if locked.mode == ModeLink {
			if previousActor == nil || previousActor.SessionID != *locked.session || previousActor.Me.User.ID != *locked.user || previousActor.Epoch != *locked.epoch {
				return fail("SSO_BROWSER_CHANGED")
			}
			if e := s.Identity.RecheckCredentialVersionWithin(ctx, q, *previousActor, *locked.credential); e != nil {
				return e
			}
		}
		refuseCredential := func(e error) error {
			if !s.keyringRecovery || !errors.Is(e, errEncryptedState) {
				return e
			}
			changed, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_flows
				SET state='failed',error_code='SSO_FLOW_UNUSABLE',finished_at=clock_timestamp(),
				verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL
				WHERE tenant_id=$1 AND id=$2 AND state='pending'`, f.tenant, f.id))
			if e != nil {
				return e
			}
			if changed != 1 {
				return fail("SSO_FLOW_UNUSABLE")
			}
			unreadable = true
			return nil
		}
		var encrypted sealedSecret
		if e := q.QueryRow(ctx, sourceSQL(q, `SELECT verifier_key_id,verifier_nonce,verifier_ciphertext FROM sso_flows WHERE tenant_id=$1 AND id=$2`, `SELECT verifier_key_id,verifier_nonce,verifier_ciphertext FROM sso_flows WHERE tenant_id=? AND id=?`, "SELECT VERIFIER_KEY_ID,VERIFIER_NONCE,VERIFIER_CIPHERTEXT FROM SSO_FLOWS WHERE TENANT_ID=? AND ID=?", f.tenant, f.id)).Scan(&encrypted.keyID, &encrypted.nonce, &encrypted.ciphertext); e != nil {
			return e
		}
		verifier, e := s.openStoredSecret(secretScope{f.tenant, connector, f.id, flowVerifierPurpose}, encrypted)
		if e != nil {
			return refuseCredential(e)
		}
		defer clear(verifier)
		if e := q.QueryRow(ctx, sourceSQL(q, `SELECT secret_key_id,secret_nonce,secret_ciphertext FROM sso_connectors WHERE tenant_id=$1 AND id=$2`, `SELECT secret_key_id,secret_nonce,secret_ciphertext FROM sso_connectors WHERE tenant_id=? AND id=?`, "SELECT SECRET_KEY_ID,SECRET_NONCE,SECRET_CIPHERTEXT FROM SSO_CONNECTORS WHERE TENANT_ID=? AND ID=?", f.tenant, connector)).Scan(&encrypted.keyID, &encrypted.nonce, &encrypted.ciphertext); e != nil {
			return e
		}
		secret, e := s.openStoredSecret(secretScope{f.tenant, connector, connector, clientSecretPurpose}, encrypted)
		if e != nil {
			return refuseCredential(e)
		}
		defer clear(secret)
		claimID := uuid.NewString()
		changed, e := q.Exec(ctx, sourceSQL(q, `UPDATE sso_flows SET state='claimed',claim_id=$1,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE tenant_id=$2 AND id=$3 AND state='pending'`, `UPDATE sso_flows SET state='claimed',claim_id=?,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE tenant_id=? AND id=? AND state='pending'`, "UPDATE SSO_FLOWS SET STATE='claimed',CLAIM_ID=?,VERIFIER_KEY_ID=NULL,VERIFIER_NONCE=NULL,VERIFIER_CIPHERTEXT=NULL WHERE TENANT_ID=? AND ID=? AND STATE='pending'", claimID, f.tenant, f.id))
		if e != nil {
			return e
		}
		if changed != 1 {
			return fail("SSO_FLOW_UNUSABLE")
		}
		locked.state, locked.claim = flowClaimed, &claimID
		result = claimedFlow{flow: locked, connectorState: c, verifierText: string(verifier), clientSecret: string(secret), previousActor: previousActor}
		return nil
	}
	if previousActor != nil {
		// Reuse the same user -> sorted source/target connectors -> session ->
		// installation order as start/unlink/admin mutation. In particular, a
		// different source connector must precede this browser's session lock.
		e = s.actorTransaction(ctx, *previousActor, connectorReadFence, connectorShared,
			func(context.Context, transaction.Executor, string) ([]connectorLock, error) {
				return []connectorLock{{f.tenant, connector}}, nil
			}, operation)
	} else {
		e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var id string
			if e := q.QueryRow(ctx, sourceSQL(q, `SELECT id::text FROM sso_connectors WHERE tenant_id=$1 AND id=$2 FOR SHARE`, `SELECT id FROM sso_connectors WHERE tenant_id=? AND id=? FOR SHARE`, "SELECT ID FROM SSO_CONNECTORS WHERE TENANT_ID=? AND ID=? FOR UPDATE OF ID", f.tenant, connector)).Scan(&id); e != nil {
				if errors.Is(e, sql.ErrNoRows) {
					return fail("SSO_FLOW_UNUSABLE")
				}
				return e
			}
			current, e := connectorTenant(ctx, q, connectorReadFence)
			if e != nil {
				return e
			}
			return operation(ctx, q, current)
		})
	}
	if e != nil {
		e = sourceOutcome(e)
		// Retain claim's public browser-change result after an in-transaction
		// actor/source revocation. Commit uncertainty has already taken priority.
		if previousActor != nil && e.Error() == "AUTH_SESSION_EXPIRED" {
			e = fail("SSO_BROWSER_CHANGED")
		}
		return claimedFlow{}, e
	}
	if unreadable {
		return claimedFlow{}, fail("SSO_FLOW_UNUSABLE")
	}
	return result, nil
}
