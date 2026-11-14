//go:build repository_integration

package sso

import (
	"context"
	"crypto/hmac"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// A staged result is private pending work. It is not an account binding, login
// session, bearer identity or a permission grant.
type samlStagedProbe struct {
	flow, continuation, identity string
	expires                      time.Time
}

func verifySAMLProbeClaim(claim samlProbeClaim, c configuration, encoded string) (identityProof, error) {
	if claim.callback != c.ACS || !xmlID.MatchString(claim.request) || !canonicalID(claim.claim) {
		return identityProof{}, identityScopeRefused
	}
	v, e := newIdentityVerifier(claim.source, c)
	if e != nil {
		return identityProof{}, e
	}
	return v.verify(encoded, claim.request)
}
func (p samlClaimProbe) stage(ctx context.Context, f samlProbeClaim, verified identityProof, requestID string) (samlStagedProbe, error) {
	if e := p.ready(); e != nil {
		return samlStagedProbe{}, e
	}
	if !canonicalID(requestID) || !canonicalID(f.id) || !canonicalID(f.claim) || verified.source != f.source || verified.RequestID != f.request || !xmlID.MatchString(verified.AssertionID) {
		return samlStagedProbe{}, identityScopeRefused
	}
	callback, e := p.callback(f.connector, f.actor.Me.Audience)
	if e != nil {
		return samlStagedProbe{}, e
	}
	if callback != f.callback {
		return samlStagedProbe{}, identityScopeRefused
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(p.service.policy.OperationSeconds))
	defer cancel()
	var out samlStagedProbe
	e = p.service.actorTransaction(ctx, f.actor, connectorReadFence, connectorShared, func(_ context.Context, _ transaction.Executor, tenant string) ([]connectorLock, error) {
		return []connectorLock{{tenant, f.connector}}, nil
	}, func(ctx context.Context, q transaction.Executor, tenant string) error {
		if tenant != f.tenant || f.source.tenant != tenant || f.source.connector != f.connector {
			return identityScopeRefused
		}
		c, e := selectSAMLConnectorWithin(ctx, q, tenant, f.connector)
		if e != nil {
			return e
		}
		if !c.Enabled || c.Revision != f.revision || c.AuthVersion != f.version {
			return identityScopeRefused
		}
		authority, e := (identityStore{tenant: tenant}).loadSource(ctx, samlIdentityQuery{q}, f.connector)
		if e != nil {
			return e
		}
		if authority != f.source {
			return identityScopeRefused
		}
		if e := p.service.Identity.RecheckCredentialVersionWithin(ctx, q, f.actor, f.credential); e != nil {
			return e
		}
		r, e := scanSAMLProbe(q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT `+samlProbeColumns+` FROM sso_saml_requests WHERE tenant_id=$1 AND connector_id=$2 AND id=$3 FOR UPDATE`, tenant, f.connector, f.id)))
		if e != nil {
			return e
		}
		if r.state != "claimed" || !r.claim.Valid || r.claim.String != f.claim || r.request != verified.RequestID || r.callback != callback || r.audience != f.actor.Me.Audience || r.user != f.actor.Me.User.ID || r.session != f.actor.SessionID || r.epoch != f.actor.Epoch || r.credential != f.credential || !hmac.Equal(r.previous, f.actor.TokenHash) || r.revision != f.revision || r.version != f.version || r.contextID != authority.contextID || r.contextRevision != authority.revision || !hmac.Equal(r.metadataDigest, authority.digest[:]) {
			return identityScopeRefused
		}
		identity, e := (identityStore{tenant: tenant}).resolve(ctx, samlIdentityQuery{q}, f.connector, verified)
		if e != nil {
			return e
		}
		var sourceUntil time.Time
		var sessionEnd any
		if !verified.SessionEnd.IsZero() {
			sessionEnd = verified.SessionEnd
		}
		if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT LEAST(clock_timestamp()+maximum_session_seconds::bigint*interval '1 second',trust_until,COALESCE($3::timestamptz,'infinity'::timestamptz)) FROM sso_saml_profiles WHERE tenant_id=$1 AND connector_id=$2 AND enabled`, tenant, f.connector, sessionEnd)).Scan(&sourceUntil); e != nil {
			return e
		}
		continuation := security.Token()
		var expiry time.Time
		e = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `INSERT INTO sso_saml_results(tenant_id,flow_id,context_id,identity_id,assertion_id,request_id,continuation_hash,authn_at,expires_at,source_until) SELECT $1,$2,$3,$4,$5,$6,$7,$8,LEAST(expires_at,$9),$10 FROM sso_saml_requests WHERE tenant_id=$1 AND id=$2 AND clock_timestamp()<LEAST(expires_at,$9) RETURNING expires_at`, tenant, r.id, authority.contextID, identity.ID, verified.AssertionID, verified.RequestID, security.Digest(continuation), verified.AuthnAt, verified.ExpiresAt, sourceUntil)).Scan(&expiry)
		if e != nil {
			return e
		}
		n, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_saml_requests SET state='verified' WHERE tenant_id=$1 AND id=$2 AND claim_id=$3 AND state='claimed'`, tenant, r.id, f.claim))
		if e != nil {
			return e
		}
		if n != 1 {
			return identityScopeRefused
		}
		if e := audit.WriteWithin(ctx, q, f.actor.Me.User.ID, f.actor.Me.User.ID, r.audience, "synthetic.saml_result_staged", "success", "sso_flow", r.id, "", requestID); e != nil {
			return e
		}
		out = samlStagedProbe{flow: r.id, continuation: continuation, identity: identity.ID, expires: expiry}
		return nil
	})
	if e != nil {
		return samlStagedProbe{}, e
	}
	return out, nil
}
