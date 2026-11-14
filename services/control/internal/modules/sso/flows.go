package sso

import (
	"context"
	"strconv"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type flow struct {
	id, tenant, connector, audience, mode, state string
	connectorRevision, authVersion               int64
	browserHash, previousHash                    []byte
	previousSession, user, session               *string
	epoch, credential                            *int64
	reason, claim                                *string
	expires                                      time.Time
}

func canonicalSubject(value string) bool {
	id, e := strconv.ParseInt(value, 10, 64)
	return e == nil && id > 0 && strconv.FormatInt(id, 10) == value
}

const flowColumns = `id::text,tenant_id::text,connector_id::text,audience,mode,state,connector_revision,auth_version,browser_hash,previous_cookie_hash,previous_session_id::text,initiating_user_id::text,initiating_session_id::text,initiating_epoch,credential_version,reason,claim_id::text,expires_at`

func scanFlow(row transaction.Row) (flow, error) {
	var f flow
	e := row.Scan(&f.id, &f.tenant, &f.connector, &f.audience, &f.mode, &f.state, &f.connectorRevision, &f.authVersion, &f.browserHash, &f.previousHash, &f.previousSession, &f.user, &f.session, &f.epoch, &f.credential, &f.reason, &f.claim, &f.expires)
	f.expires = f.expires.UTC()
	return f, e
}

type claimedFlow struct {
	flow
	connectorState             Connector
	verifierText, clientSecret string
	previousActor              *identity.Auth
}

// Complete consumes a local flow before any provider request. A failed or
// interrupted exchange requires a new Start, even when the provider retained its code.
func (s *Service) Complete(ctx context.Context, connector, audience, state, browser, previous, code string, denied bool, requestID string) (identity.IssuedSession, string, error) {
	var issued identity.IssuedSession
	if !canonicalID(requestID) || denied && code != "" || !denied && !safeOpaque(code, maximumAuthorizationCodeBytes) {
		return issued, "", fail("SSO_FLOW_INVALID")
	}
	f, e := s.claim(ctx, connector, audience, state, browser, previous)
	if e != nil {
		return issued, "", e
	}
	if denied {
		e = fail("SSO_PROVIDER_DENIED")
	} else {
		callback, ce := s.callback(connector, audience)
		if ce != nil {
			e = ce
		} else {
			p, pe := s.provider(connector, f.connectorState.Origin)
			if pe != nil {
				e = pe
			} else {
				var subject providerIdentity
				subject, e = p.exchange(ctx, f.connectorState.ClientID, f.clientSecret, callback, code, f.verifierText)
				if e == nil {
					issued, e = s.commitFlow(ctx, f, subject.subject, previous, requestID)
				}
			}
		}
	}
	// Discard bounded in-memory credentials at this boundary; no token/code is persisted.
	f.verifierText, f.clientSecret = "", ""
	if e != nil {
		if outcome, found := transaction.CommitOutcome(e); found && outcome != transaction.CommitRolledBack {
			return identity.IssuedSession{}, f.mode, fail(string(outcome))
		}
		e = publicFlowError(e)
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.Seconds(s.policy.CleanupTimeoutSeconds))
		defer cancel()
		if fe := s.failFlow(cleanupCtx, f, e.Error(), requestID); fe != nil {
			if outcome, found := transaction.CommitOutcome(fe); found {
				return identity.IssuedSession{}, f.mode, fail(string(outcome))
			}
			return identity.IssuedSession{}, f.mode, fail("STORAGE_UNAVAILABLE")
		}
		return identity.IssuedSession{}, f.mode, e
	}
	return issued, f.mode, nil
}

func publicFlowError(e error) error {
	if e == nil {
		return nil
	}
	if outcome, found := transaction.CommitOutcome(e); found {
		return fail(string(outcome))
	}
	switch e.Error() {
	case string(transaction.CommitUnknown), "SSO_PROVIDER_DENIED", "SSO_PROVIDER_UNAVAILABLE", "SSO_PROVIDER_REJECTED", "SSO_PROVIDER_RESPONSE_INVALID", "SSO_SCOPE_REJECTED", "SSO_IDENTITY_REJECTED", "SSO_CAPACITY_EXCEEDED", "SSO_BINDING_REQUIRED", "SSO_BINDING_CONFLICT", "SSO_BINDING_LIMIT", "SSO_BROWSER_CHANGED", "SSO_PASSWORD_CHANGED", "SSO_FLOW_UNUSABLE", "SSO_FLOW_EXPIRED", "AUTH_SESSION_EXPIRED", "AUTH_INVALID_CREDENTIALS":
		return e
	default:
		return fail("STORAGE_UNAVAILABLE")
	}
}
