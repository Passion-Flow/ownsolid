package sso

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// anonymousStartTransaction takes the target connector before the shared late
// admission fence. Authenticated starts use actorTransaction's user/source order.
func (s *Service) anonymousStartTransaction(ctx context.Context, connector string, operation func(context.Context, transaction.Executor, string) error) error {
	store, e := s.catalogRepository()
	if e != nil {
		return e
	}
	return sourceOutcome(store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		tenant, e := connectorTenant(ctx, q, connectorPeek)
		if e != nil {
			return e
		}
		var id string
		e = q.QueryRow(ctx, sourceSQL(q, `SELECT id::text FROM sso_connectors WHERE tenant_id=$1 AND id=$2 FOR SHARE`, `SELECT id FROM sso_connectors WHERE tenant_id=? AND id=? FOR SHARE`, "SELECT ID FROM SSO_CONNECTORS WHERE TENANT_ID=? AND ID=? FOR UPDATE OF ID", tenant, connector)).Scan(&id)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		current, e := connectorTenant(ctx, q, connectorCreateFence)
		if e != nil {
			return e
		}
		if current != tenant {
			return fail("SSO_REVISION_CONFLICT")
		}
		return operation(ctx, q, tenant)
	}))
}

func (s *Service) Start(ctx context.Context, connector, audience, previous string, actor *identity.Auth, input StartInput, requestID string) (StartResult, error) {
	if s == nil || s.Identity == nil {
		return StartResult{}, fail("SSO_CONFIGURATION_REQUIRED")
	}
	if !s.enabled {
		return StartResult{}, fail("SSO_DISABLED")
	}
	callback, e := s.callback(connector, audience)
	if e != nil {
		return StartResult{}, e
	}
	if !canonicalID(requestID) || input.Mode != ModeLogin && input.Mode != ModeLink || input.Mode == ModeLogin && (input.Password != nil || input.Reason != nil) || input.Mode == ModeLink && (input.Password == nil || input.Reason == nil || !validReason(*input.Reason)) {
		return StartResult{}, fail("VALIDATION_FAILED")
	}
	if input.Mode == ModeLink && (actor == nil || !validSSOActor(*actor) || actor.Me.Audience != audience) {
		return StartResult{}, fail("AUTH_REQUIRED")
	}
	if _, e := s.catalogRepository(); e != nil {
		return StartResult{}, e
	}
	if e := s.policy.Validate(); e != nil {
		return StartResult{}, e
	}
	if s.keys == nil {
		return StartResult{}, fail("SSO_CONFIGURATION_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy.OperationSeconds))
	defer cancel()
	// Capture a live previous account for login as well as link. An expired
	// previous login cookie can start anonymously,but never authorize a link.
	var current *identity.Auth
	if security.ValidToken(previous) {
		a, ae := s.Identity.Authenticate(ctx, previous, audience)
		if ae == nil {
			current = &a
		} else if ae.Error() != "AUTH_SESSION_EXPIRED" && ae.Error() != "AUTH_REQUIRED" {
			return StartResult{}, ae
		}
	}
	var proof identity.PasswordConfirmation
	if input.Mode == ModeLink {
		if current == nil || actor.SessionID != current.SessionID || actor.Me.User.ID != current.Me.User.ID || actor.Epoch != current.Epoch || !hmac.Equal(actor.TokenHash, current.TokenHash) {
			return StartResult{}, fail("AUTH_REQUIRED")
		}
		proof, e = s.Identity.ConfirmPassword(ctx, *current, *input.Password)
		if e != nil {
			return StartResult{}, e
		}
	}
	state, browser, verifier, id := security.Token(), security.Token(), security.Token(), uuid.NewString()
	var result StartResult
	operation := func(ctx context.Context, q transaction.Executor, tenant string) error {
		if input.Mode == ModeLink {
			if e := s.Identity.RecheckConfirmationWithin(ctx, q, *current, proof); e != nil {
				return e
			}
		}
		c, e := selectConnectorWithin(ctx, q, tenant, connector)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("SSO_CONNECTOR_NOT_FOUND")
		}
		if e != nil {
			return e
		}
		if !c.Enabled {
			return fail("SSO_CONNECTOR_DISABLED")
		}
		provider, e := s.provider(connector, c.Origin)
		if e != nil {
			return e
		}
		var count int
		if e := q.QueryRow(ctx, sourceSQL(q, `SELECT COUNT(*) FROM sso_flows WHERE tenant_id=$1 AND state IN('pending','claimed') AND expires_at>clock_timestamp()`, `SELECT COUNT(*) FROM sso_flows WHERE tenant_id=? AND state IN('pending','claimed') AND expires_at>UTC_TIMESTAMP(6)`, "SELECT COUNT(*) FROM SSO_FLOWS WHERE TENANT_ID=? AND STATE IN('pending','claimed') AND EXPIRES_AT>CURRENT_TIMESTAMP(6)", tenant)).Scan(&count); e != nil {
			return e
		}
		if count >= s.policy.MaximumPendingFlows {
			return fail("SSO_CAPACITY_EXCEEDED")
		}
		sealed, e := s.keys.seal(secretScope{tenant, connector, id, flowVerifierPurpose}, []byte(verifier))
		if e != nil {
			return e
		}
		var previousSession, user, session, epoch, credential, reason any
		if current != nil {
			previousSession = current.SessionID
		}
		if input.Mode == ModeLink {
			user, session, epoch, credential, reason = current.Me.User.ID, current.SessionID, current.Epoch, proof.Version(), *input.Reason
		}
		changed, e := q.Exec(ctx, sourceSQL(q,
			`INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,previous_session_id,initiating_user_id,initiating_session_id,initiating_epoch,credential_version,reason,verifier_key_id,verifier_nonce,verifier_ciphertext,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,clock_timestamp()+$20::bigint*interval '1 microsecond')`,
			`INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,previous_session_id,initiating_user_id,initiating_session_id,initiating_epoch,credential_version,reason,verifier_key_id,verifier_nonce,verifier_ciphertext,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,TIMESTAMPADD(MICROSECOND,?,UTC_TIMESTAMP(6)))`, "INSERT INTO SSO_FLOWS(ID,TENANT_ID,CONNECTOR_ID,CONNECTOR_REVISION,AUTH_VERSION,AUDIENCE,MODE,STATE_HASH,BROWSER_HASH,PREVIOUS_COOKIE_HASH,PREVIOUS_SESSION_ID,INITIATING_USER_ID,INITIATING_SESSION_ID,INITIATING_EPOCH,CREDENTIAL_VERSION,REASON,VERIFIER_KEY_ID,VERIFIER_NONCE,VERIFIER_CIPHERTEXT,EXPIRES_AT) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP(6)+NUMTODSINTERVAL(?/1000000.0,'SECOND'))", id, tenant, connector, c.Revision, c.AuthVersion, audience, input.Mode, security.Digest(state), s.browserProof(connector, audience, browser), security.Digest(security.Binding(previous)), previousSession, user, session, epoch, credential, reason, sealed.keyID, sealed.nonce, sealed.ciphertext, config.Seconds(s.policy.FlowSeconds).Microseconds()))
		if e != nil {
			return e
		}
		if changed != 1 {
			return fail("STORAGE_UNAVAILABLE")
		}
		if e := q.QueryRow(ctx, sourceSQL(q, `SELECT expires_at FROM sso_flows WHERE tenant_id=$1 AND id=$2`, `SELECT expires_at FROM sso_flows WHERE tenant_id=? AND id=?`, "SELECT EXPIRES_AT FROM SSO_FLOWS WHERE TENANT_ID=? AND ID=?", tenant, id)).Scan(&result.ExpiresAt); e != nil {
			return e
		}
		result.ExpiresAt = result.ExpiresAt.UTC()
		if !connectorTimestamp(result.ExpiresAt) {
			return fail("STORAGE_UNAVAILABLE")
		}
		result.AuthorizationURL, e = provider.authorizationURL(c.ClientID, callback, state, verifier)
		if e != nil {
			return e
		}
		actorID := ""
		if current != nil {
			actorID = current.Me.User.ID
		}
		return audit.WriteWithin(ctx, q, actorID, actorID, audience, "sso.flow_started", "success", "sso_flow", id, "", requestID)
	}
	if current == nil {
		e = s.anonymousStartTransaction(ctx, connector, operation)
	} else {
		targets := func(_ context.Context, _ transaction.Executor, tenant string) ([]connectorLock, error) {
			return []connectorLock{{tenant, connector}}, nil
		}
		e = s.actorTransaction(ctx, *current, connectorCreateFence, connectorShared, targets, operation)
	}
	if e != nil {
		return StartResult{}, e
	}
	result.BrowserToken = browser
	return result, nil
}
