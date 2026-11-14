package sso

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Complete consumes one verified Portal result, explicitly links its exact
// identity and issues one source-bound session in the same transaction.
// The HTTP owner must enforce its entrance, ordinary Origin/CSRF and cookie rules.
func (p *SAMLCompletion) Complete(ctx context.Context, input SAMLCompleteInput, requestID string) (identity.IssuedSession, error) {
	connector, audience, flow, continuation, previous := input.ConnectorID, input.Audience, input.FlowID, input.Continuation, input.PreviousSession
	if ctx == nil {
		return identity.IssuedSession{}, fail("SSO_SAML_CONFIGURATION_REQUIRED")
	}
	if audience != "portal" {
		return identity.IssuedSession{}, fail("SSO_SAML_ENTRANCE_UNAVAILABLE")
	}
	if e := p.ready(); e != nil {
		return identity.IssuedSession{}, e
	}
	if !canonicalID(flow) || !canonicalID(requestID) || !security.ValidToken(continuation) || !security.ValidToken(previous) {
		return identity.IssuedSession{}, fail("SSO_FLOW_INVALID")
	}
	callback, e := p.callback(connector, audience)
	if e != nil {
		return identity.IssuedSession{}, e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(p.service.policy.OperationSeconds))
	defer cancel()
	store, e := p.service.catalogRepository()
	if e != nil {
		return identity.IssuedSession{}, e
	}
	tenant, e := connectorTenant(ctx, store, connectorPeek)
	if e != nil {
		return identity.IssuedSession{}, e
	}
	initial, e := scanSAMLCompletionRequest(store.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT `+samlCompletionRequestColumns+` FROM sso_saml_requests WHERE tenant_id=$1 AND connector_id=$2 AND id=$3 AND audience=$4`, tenant, connector, flow, audience)))
	if errors.Is(e, sql.ErrNoRows) {
		return identity.IssuedSession{}, fail("SSO_FLOW_INVALID")
	}
	if e != nil {
		return identity.IssuedSession{}, sourceOutcome(e)
	}
	if initial.callback != callback || !hmac.Equal(initial.previous, security.Digest(previous)) {
		return identity.IssuedSession{}, fail("SSO_BROWSER_CHANGED")
	}
	actor, e := p.service.Identity.Authenticate(identity.WithoutSessionTouch(ctx), previous, audience)
	if e != nil {
		return identity.IssuedSession{}, e
	}
	if actor.Me.User.ID != initial.user || actor.SessionID != initial.session || actor.Epoch != initial.epoch || !hmac.Equal(actor.TokenHash, initial.previous) {
		return identity.IssuedSession{}, fail("SSO_BROWSER_CHANGED")
	}
	var issued identity.IssuedSession
	e = p.service.actorTransaction(ctx, actor, connectorReadFence, connectorShared, func(_ context.Context, _ transaction.Executor, current string) ([]connectorLock, error) {
		return []connectorLock{{current, connector}}, nil
	}, func(ctx context.Context, q transaction.Executor, current string) error {
		if current != tenant {
			return fail("SSO_FLOW_UNUSABLE")
		}
		c, e := selectSAMLCompletionConnector(ctx, q, tenant, connector)
		if e != nil {
			return e
		}
		if !c.Enabled || c.Revision != initial.revision || c.AuthVersion != initial.version {
			return fail("SSO_FLOW_UNUSABLE")
		}
		authority, e := loadSAMLCompletionProfile(ctx, q, tenant, connector)
		if e != nil {
			return e
		}
		if authority.contextID != initial.contextID || authority.revision != initial.contextRevision || !hmac.Equal(authority.digest[:], initial.metadataDigest) {
			return fail("SSO_FLOW_UNUSABLE")
		}
		if e = p.service.Identity.RecheckCredentialVersionWithin(ctx, q, actor, initial.credential); e != nil {
			return e
		}
		locked, e := scanSAMLCompletionRequest(q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT `+samlCompletionRequestColumns+` FROM sso_saml_requests WHERE tenant_id=$1 AND connector_id=$2 AND id=$3 FOR UPDATE`, tenant, connector, flow)))
		if errors.Is(e, sql.ErrNoRows) {
			return fail("SSO_FLOW_UNUSABLE")
		}
		if e != nil {
			return e
		}
		if locked.state != "verified" || locked.request != initial.request || locked.callback != callback || locked.user != actor.Me.User.ID || locked.session != actor.SessionID || locked.epoch != actor.Epoch || locked.credential != initial.credential || locked.contextID != authority.contextID || locked.contextRevision != authority.revision || !hmac.Equal(locked.metadataDigest, authority.digest[:]) || !hmac.Equal(locked.previous, actor.TokenHash) {
			return fail("SSO_FLOW_UNUSABLE")
		}
		var external, contextID, verifiedRequest string
		var sourceUntil, resultUntil time.Time
		e = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT identity_id::text,context_id::text,request_id,source_until,expires_at FROM sso_saml_results WHERE tenant_id=$1 AND flow_id=$2 AND continuation_hash=$3 AND expires_at>clock_timestamp() AND source_until>clock_timestamp() AND completed_binding_version IS NULL FOR UPDATE`, tenant, flow, security.Digest(continuation))).Scan(&external, &contextID, &verifiedRequest, &sourceUntil, &resultUntil)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("SSO_FLOW_UNUSABLE")
		}
		if e != nil {
			return e
		}
		sourceUntil, resultUntil = sourceUntil.UTC(), resultUntil.UTC()
		if contextID != authority.contextID || verifiedRequest != locked.request {
			return fail("SSO_FLOW_UNUSABLE")
		}
		var bound sql.NullString
		var active bool
		var bindingVersion int64
		e = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT bound_user_id::text,active,binding_version FROM sso_saml_identities WHERE tenant_id=$1 AND context_id=$2 AND id=$3 FOR UPDATE`, tenant, contextID, external)).Scan(&bound, &active, &bindingVersion)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("SSO_FLOW_UNUSABLE")
		}
		if e != nil {
			return e
		}
		var fresh bool
		if e = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT clock_timestamp()<LEAST($1::timestamptz,$2::timestamptz,$3::timestamptz)`, resultUntil, sourceUntil, locked.expires)).Scan(&fresh); e != nil {
			return e
		}
		if !fresh {
			return fail("SSO_FLOW_EXPIRED")
		}
		if bound.Valid && bound.String != actor.Me.User.ID {
			return fail("SSO_BINDING_CONFLICT")
		}
		if !active {
			if bindingVersion >= identity.MaximumAuthVersion {
				return fail("SSO_FLOW_UNUSABLE")
			}
			n, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_saml_identities SET bound_user_id=$1,active=true,binding_version=binding_version+1 WHERE tenant_id=$2 AND context_id=$3 AND id=$4 AND binding_version=$5 AND NOT active`, actor.Me.User.ID, tenant, contextID, external, bindingVersion))
			if e != nil {
				return e
			}
			if n != 1 {
				return fail("SSO_FLOW_UNUSABLE")
			}
			bindingVersion++
			if e = audit.WriteWithin(ctx, q, actor.Me.User.ID, actor.Me.User.ID, audience, "sso.saml_binding_linked", "success", "sso_binding", external, locked.reason, requestID); e != nil {
				return e
			}
		}
		n, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_saml_results SET completed_binding_version=$1 WHERE tenant_id=$2 AND flow_id=$3 AND completed_binding_version IS NULL`, bindingVersion, tenant, flow))
		if e != nil {
			return e
		}
		if n != 1 {
			return fail("SSO_FLOW_UNUSABLE")
		}
		n, e = q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_saml_requests SET state='succeeded',authenticated_user_id=$1,finished_at=clock_timestamp() WHERE tenant_id=$2 AND id=$3 AND state='verified'`, actor.Me.User.ID, tenant, flow))
		if e != nil {
			return e
		}
		if n != 1 {
			return fail("SSO_FLOW_UNUSABLE")
		}
		source := identity.SessionSource{TenantID: tenant, ConnectorID: connector, BindingID: external, FlowID: flow, AuthVersion: locked.version}
		issued, e = p.service.Identity.IssueSessionUntilWithin(ctx, q, actor.Me.User.ID, actor.Epoch, audience, previous, requestID, source, sourceUntil)
		return e
	})
	if e != nil {
		return identity.IssuedSession{}, e
	}
	return issued, nil
}
