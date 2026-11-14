//go:build repository_integration

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
	"github.com/jackc/pgx/v5"
)

func selectSAMLConnectorWithin(ctx context.Context, q transaction.Executor, tenant, id string) (Connector, error) {
	var c Connector
	var storedTenant string
	e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT id::text,tenant_id::text,provider,origin,enabled,revision,auth_version FROM sso_connectors WHERE tenant_id=$1 AND id=$2 AND provider='saml'`, tenant, id)).Scan(&c.ID, &storedTenant, &c.Provider, &c.Origin, &c.Enabled, &c.Revision, &c.AuthVersion)
	return c, e
}

// samlCompletionSources is an external candidate for startup's actual source
// composition. It never treats a failed SAML check as another protocol.
type samlCompletionSources struct {
	service *Service
	legacy  identity.SourceGuard
}

func (g *samlCompletionSources) CheckSessionSource(ctx context.Context, tx pgx.Tx, source identity.SessionSource, user string) error {
	if g == nil || g.service == nil || !sourceValid(source, user) {
		return fail("AUTH_SESSION_EXPIRED")
	}
	store, e := g.service.Identity.Repository()
	if e != nil {
		return e
	}
	if store.Dialect() != transaction.PostgreSQL {
		return fail("SSO_TRANSACTION_STORAGE_MISMATCH")
	}
	if tx != nil {
		return g.check(ctx, transaction.PostgreSQLTransaction(tx), true, source, user)
	}
	return g.check(ctx, store, false, source, user)
}
func (g *samlCompletionSources) CheckSessionSourceWithin(ctx context.Context, q transaction.Executor, source identity.SessionSource, user string) error {
	if g == nil || g.service == nil || q == nil || q.Dialect() != transaction.PostgreSQL || !sourceValid(source, user) {
		return fail("AUTH_SESSION_EXPIRED")
	}
	return g.check(ctx, q, true, source, user)
}
func (g *samlCompletionSources) check(ctx context.Context, q transaction.Executor, locked bool, source identity.SessionSource, user string) error {
	var saml, legacy bool
	e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT EXISTS(SELECT 1 FROM saml_claim_probe_flows WHERE tenant_id=$1 AND connector_id=$2 AND id=$3),EXISTS(SELECT 1 FROM sso_flows WHERE tenant_id=$1 AND connector_id=$2 AND id=$3)`, source.TenantID, source.ConnectorID, source.FlowID)).Scan(&saml, &legacy)
	if e != nil {
		return e
	}
	if saml && legacy {
		return fail("AUTH_SESSION_EXPIRED")
	}
	if !saml {
		if !legacy || g.legacy == nil {
			return fail("AUTH_SESSION_EXPIRED")
		}
		if locked {
			return g.legacy.CheckSessionSourceWithin(ctx, q, source, user)
		}
		return g.legacy.CheckSessionSource(ctx, nil, source, user)
	}
	connectorLock, sourceLock := "", ""
	if locked {
		connectorLock = " FOR SHARE OF c"
		sourceLock = " FOR SHARE OF p,b,f,r"
	}
	var id string
	e = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT c.id::text FROM sso_connectors c JOIN installation_state i ON i.tenant_id=c.tenant_id JOIN tenants t ON t.id=c.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' AND c.tenant_id=$1 AND c.id=$2 AND c.provider='saml' AND c.enabled AND c.auth_version=$3`+connectorLock, source.TenantID, source.ConnectorID, source.AuthVersion)).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return fail("AUTH_SESSION_EXPIRED")
	}
	if e != nil {
		return e
	}
	e = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT b.id::text FROM saml_identity_contexts p JOIN saml_persistent_identities b ON b.tenant_id=p.tenant_id AND b.context_id=p.id JOIN saml_probe_verified_results r ON r.tenant_id=b.tenant_id AND r.context_id=b.context_id AND r.identity_id=b.id JOIN saml_claim_probe_flows f ON f.tenant_id=r.tenant_id AND f.id=r.flow_id AND f.connector_id=p.connector_id WHERE p.tenant_id=$1 AND p.connector_id=$2 AND b.id=$3 AND f.id=$4 AND f.authenticated_user_id=$5 AND b.bound_user_id=$5 AND b.active AND b.binding_version=r.completed_binding_version AND p.enabled AND p.revision=f.context_revision AND p.metadata_digest=f.metadata_digest AND f.auth_version=$6 AND f.state='succeeded' AND r.source_until>clock_timestamp() AND p.trust_until>clock_timestamp()`+sourceLock, source.TenantID, source.ConnectorID, source.BindingID, source.FlowID, user, source.AuthVersion)).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return fail("AUTH_SESSION_EXPIRED")
	}
	return e
}

func (p samlClaimProbe) complete(ctx context.Context, connector, audience, flow, continuation, previous, requestID string) (identity.IssuedSession, error) {
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
	initial, e := scanSAMLProbe(store.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT `+samlProbeColumns+` FROM saml_claim_probe_flows WHERE tenant_id=$1 AND connector_id=$2 AND id=$3 AND audience=$4`, tenant, connector, flow, audience)))
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
		c, e := selectSAMLConnectorWithin(ctx, q, tenant, connector)
		if e != nil {
			return e
		}
		if !c.Enabled || c.Revision != initial.revision || c.AuthVersion != initial.version {
			return fail("SSO_FLOW_UNUSABLE")
		}
		authority, e := (identityStore{tenant: tenant}).loadSource(ctx, samlIdentityQuery{q}, connector)
		if e != nil {
			return e
		}
		if authority.contextID != initial.contextID || authority.revision != initial.contextRevision || !hmac.Equal(authority.digest[:], initial.metadataDigest) {
			return fail("SSO_FLOW_UNUSABLE")
		}
		if e = p.service.Identity.RecheckCredentialVersionWithin(ctx, q, actor, initial.credential); e != nil {
			return e
		}
		locked, e := scanSAMLProbe(q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT `+samlProbeColumns+` FROM saml_claim_probe_flows WHERE tenant_id=$1 AND connector_id=$2 AND id=$3 FOR UPDATE`, tenant, connector, flow)))
		if e != nil {
			return e
		}
		if locked.state != "verified" || locked.request != initial.request || locked.callback != callback || locked.user != actor.Me.User.ID || locked.session != actor.SessionID || locked.epoch != actor.Epoch || locked.credential != initial.credential || locked.contextID != authority.contextID || locked.contextRevision != authority.revision || !hmac.Equal(locked.metadataDigest, authority.digest[:]) || !hmac.Equal(locked.previous, actor.TokenHash) {
			return fail("SSO_FLOW_UNUSABLE")
		}
		var external, contextID, verifiedRequest string
		var sourceUntil time.Time
		e = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT identity_id::text,context_id::text,request_id,source_until FROM saml_probe_verified_results WHERE tenant_id=$1 AND flow_id=$2 AND continuation_hash=$3 AND expires_at>clock_timestamp() AND source_until>clock_timestamp() AND completed_binding_version IS NULL FOR UPDATE`, tenant, flow, security.Digest(continuation))).Scan(&external, &contextID, &verifiedRequest, &sourceUntil)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("SSO_FLOW_UNUSABLE")
		}
		if e != nil {
			return e
		}
		if contextID != authority.contextID || verifiedRequest != locked.request {
			return fail("SSO_FLOW_UNUSABLE")
		}
		var bound sql.NullString
		var active bool
		var bindingVersion int64
		e = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT bound_user_id::text,active,binding_version FROM saml_persistent_identities WHERE tenant_id=$1 AND context_id=$2 AND id=$3 FOR UPDATE`, tenant, contextID, external)).Scan(&bound, &active, &bindingVersion)
		if e != nil {
			return e
		}
		if bound.Valid && bound.String != actor.Me.User.ID {
			return fail("SSO_BINDING_CONFLICT")
		}
		if !active {
			if bindingVersion >= identity.MaximumAuthVersion {
				return fail("SSO_FLOW_UNUSABLE")
			}
			n, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE saml_persistent_identities SET bound_user_id=$1,active=true,binding_version=binding_version+1 WHERE tenant_id=$2 AND context_id=$3 AND id=$4 AND binding_version=$5 AND NOT active`, actor.Me.User.ID, tenant, contextID, external, bindingVersion))
			if e != nil {
				return e
			}
			if n != 1 {
				return fail("SSO_FLOW_UNUSABLE")
			}
			bindingVersion++
			if e = audit.WriteWithin(ctx, q, actor.Me.User.ID, actor.Me.User.ID, audience, "synthetic.saml_binding_linked", "success", "sso_binding", external, "", requestID); e != nil {
				return e
			}
		}
		n, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE saml_probe_verified_results SET completed_binding_version=$1 WHERE tenant_id=$2 AND flow_id=$3 AND completed_binding_version IS NULL`, bindingVersion, tenant, flow))
		if e != nil {
			return e
		}
		if n != 1 {
			return fail("SSO_FLOW_UNUSABLE")
		}
		n, e = q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE saml_claim_probe_flows SET state='succeeded',authenticated_user_id=$1,finished_at=clock_timestamp() WHERE tenant_id=$2 AND id=$3 AND state='verified'`, actor.Me.User.ID, tenant, flow))
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
