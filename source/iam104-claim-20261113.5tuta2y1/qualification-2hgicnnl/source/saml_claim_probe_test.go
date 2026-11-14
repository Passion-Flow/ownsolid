//go:build repository_integration

package sso

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

// This external test-only candidate claims browser requests. It verifies no
// assertion and has no binding, authentication-result or session issuance path.
type samlClaimProbe struct {
	service *Service
	ttl     time.Duration
}
type samlProbeStart struct {
	id, request, browser, relay string
	expires                     time.Time
}
type samlProbeClaim struct {
	id, request, claim, tenant, connector, callback string
	actor                                           identity.Auth
	credential, revision, version                   int64
	expires                                         time.Time
}
type samlProbeRow struct {
	id, tenant, connector, audience, callback, request, user, session, state string
	browser, relay, previous                                                 []byte
	epoch, credential, revision, version                                     int64
	expires                                                                  time.Time
}

const samlProbeColumns = `id::text,tenant_id::text,connector_id::text,audience,callback,request_id,user_id::text,session_id::text,state,browser_hash,relay_hash,previous_hash,epoch,credential_version,connector_revision,auth_version,expires_at`

func scanSAMLProbe(row transaction.Row) (samlProbeRow, error) {
	var r samlProbeRow
	e := row.Scan(&r.id, &r.tenant, &r.connector, &r.audience, &r.callback, &r.request, &r.user, &r.session, &r.state, &r.browser, &r.relay, &r.previous, &r.epoch, &r.credential, &r.revision, &r.version, &r.expires)
	return r, e
}
func (p samlClaimProbe) ready() error {
	if p.service == nil || p.service.Identity == nil || !p.service.enabled || p.ttl <= 0 || p.ttl > time.Duration(p.service.policy.FlowSeconds)*time.Second {
		return fail("SSO_CONFIGURATION_REQUIRED")
	}
	store, e := p.service.catalogRepository()
	if e != nil {
		return e
	}
	if store.Dialect() != transaction.PostgreSQL {
		return fail("SSO_TRANSACTION_STORAGE_MISMATCH")
	}
	return p.service.policy.Validate()
}
func (p samlClaimProbe) callback(connector, audience string) (string, error) {
	if !canonicalID(connector) || (audience != "admin" && audience != "portal") {
		return "", fail("SSO_FLOW_INVALID")
	}
	// The ordinary callback validator checks the configured entrance authority.
	if _, e := p.service.callback(connector, audience); e != nil {
		return "", e
	}
	base := p.service.Identity.Config.Server.PortalOrigin
	if audience == "admin" {
		base = p.service.Identity.Config.Server.AdminOrigin
	}
	return base + "/api/v1/private-saml-probe/" + connector + "/acs", nil
}
func samlProbeBrowser(tenant, connector, audience, callback, token string) []byte {
	encoded, _ := json.Marshal([6]string{"ownsolid-private-saml-request-v1", tenant, connector, audience, callback, token})
	sum := sha256.Sum256(encoded)
	return sum[:]
}
func samlProbeActor(r samlProbeRow) identity.Auth {
	return identity.Auth{Me: identity.Me{User: identity.User{ID: r.user}, Audience: r.audience}, SessionID: r.session, TokenHash: append([]byte(nil), r.previous...), Epoch: r.epoch}
}
func (p samlClaimProbe) start(ctx context.Context, connector, audience, previous, password, requestID string) (samlProbeStart, error) {
	if e := p.ready(); e != nil {
		return samlProbeStart{}, e
	}
	callback, e := p.callback(connector, audience)
	if e != nil {
		return samlProbeStart{}, e
	}
	if !canonicalID(requestID) || !security.ValidToken(previous) {
		return samlProbeStart{}, fail("AUTH_REQUIRED")
	}
	a, e := p.service.Identity.Authenticate(ctx, previous, audience)
	if e != nil {
		return samlProbeStart{}, e
	}
	confirmation, e := p.service.Identity.ConfirmPassword(ctx, a, password)
	if e != nil {
		return samlProbeStart{}, e
	}
	out := samlProbeStart{id: uuid.NewString(), request: "_" + uuid.NewString(), browser: security.Token(), relay: security.Token()}
	// Targets are resolved from server installation authority, never Auth.Me or input.
	e = p.service.actorTransaction(ctx, a, connectorCreateFence, connectorShared, func(_ context.Context, _ transaction.Executor, tenant string) ([]connectorLock, error) {
		return []connectorLock{{tenant, connector}}, nil
	}, func(ctx context.Context, q transaction.Executor, tenant string) error {
		if e := p.service.Identity.RecheckConfirmationWithin(ctx, q, a, confirmation); e != nil {
			return e
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
		var count int
		if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT count(*) FROM saml_claim_probe_flows WHERE tenant_id=$1 AND expires_at>clock_timestamp()`, tenant)).Scan(&count); e != nil {
			return e
		}
		if count >= p.service.policy.MaximumPendingFlows {
			return fail("SSO_CAPACITY_EXCEEDED")
		}
		e = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `INSERT INTO saml_claim_probe_flows(id,tenant_id,connector_id,audience,callback,request_id,user_id,session_id,epoch,credential_version,connector_revision,auth_version,browser_hash,relay_hash,previous_hash,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,clock_timestamp()+$16::bigint*interval '1 microsecond') RETURNING expires_at`, out.id, tenant, connector, audience, callback, out.request, a.Me.User.ID, a.SessionID, a.Epoch, confirmation.Version(), c.Revision, c.AuthVersion, samlProbeBrowser(tenant, connector, audience, callback, out.browser), security.Digest(out.relay), a.TokenHash, p.ttl.Microseconds())).Scan(&out.expires)
		if e != nil {
			return e
		}
		return audit.WriteWithin(ctx, q, a.Me.User.ID, a.Me.User.ID, audience, "synthetic.saml_claim_started", "success", "sso_flow", out.id, "", requestID)
	})
	if e != nil {
		return samlProbeStart{}, e
	}
	return out, nil
}
func (p samlClaimProbe) claim(ctx context.Context, connector, audience, relay, browser, requestID string) (samlProbeClaim, error) {
	if e := p.ready(); e != nil {
		return samlProbeClaim{}, e
	}
	callback, e := p.callback(connector, audience)
	if e != nil {
		return samlProbeClaim{}, e
	}
	if !canonicalID(requestID) || !security.ValidToken(relay) || !security.ValidToken(browser) {
		return samlProbeClaim{}, fail("SSO_FLOW_INVALID")
	}
	store, e := p.service.catalogRepository()
	if e != nil {
		return samlProbeClaim{}, e
	}
	tenant, e := connectorTenant(ctx, store, connectorPeek)
	if e != nil {
		return samlProbeClaim{}, e
	}
	initial, e := scanSAMLProbe(store.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT `+samlProbeColumns+` FROM saml_claim_probe_flows WHERE tenant_id=$1 AND connector_id=$2 AND audience=$3 AND relay_hash=$4`, tenant, connector, audience, security.Digest(relay))))
	if errors.Is(e, sql.ErrNoRows) {
		return samlProbeClaim{}, fail("SSO_FLOW_INVALID")
	}
	if e != nil {
		return samlProbeClaim{}, sourceOutcome(e)
	}
	if initial.callback != callback || !hmac.Equal(initial.browser, samlProbeBrowser(tenant, connector, audience, callback, browser)) {
		return samlProbeClaim{}, fail("SSO_FLOW_INVALID")
	}
	a := samlProbeActor(initial)
	var out samlProbeClaim
	e = p.service.actorTransaction(ctx, a, connectorReadFence, connectorShared, func(_ context.Context, _ transaction.Executor, current string) ([]connectorLock, error) {
		return []connectorLock{{current, connector}}, nil
	}, func(ctx context.Context, q transaction.Executor, current string) error {
		if current != tenant {
			return fail("SSO_FLOW_INVALID")
		}
		c, e := selectConnectorWithin(ctx, q, current, connector)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("SSO_FLOW_UNUSABLE")
		}
		if e != nil {
			return e
		}
		if !c.Enabled || c.Revision != initial.revision || c.AuthVersion != initial.version {
			return fail("SSO_FLOW_UNUSABLE")
		}
		if e := p.service.Identity.RecheckCredentialVersionWithin(ctx, q, a, initial.credential); e != nil {
			return e
		}
		r, e := scanSAMLProbe(q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT `+samlProbeColumns+` FROM saml_claim_probe_flows WHERE tenant_id=$1 AND connector_id=$2 AND id=$3 FOR UPDATE`, tenant, connector, initial.id)))
		if e != nil {
			return e
		}
		// Immutable columns are guarded by SQL too; repeat every request/actor predicate.
		if r.state != "pending" || r.request != initial.request || r.audience != audience || r.callback != callback || r.user != a.Me.User.ID || r.session != a.SessionID || r.epoch != a.Epoch || r.credential != initial.credential || r.revision != c.Revision || r.version != c.AuthVersion || !hmac.Equal(r.previous, a.TokenHash) || !hmac.Equal(r.relay, security.Digest(relay)) || !hmac.Equal(r.browser, samlProbeBrowser(tenant, connector, audience, callback, browser)) {
			return fail("SSO_FLOW_UNUSABLE")
		}
		claim := uuid.NewString()
		n, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE saml_claim_probe_flows SET state='claimed',claim_id=$1 WHERE tenant_id=$2 AND id=$3 AND state='pending' AND expires_at>clock_timestamp()`, claim, tenant, r.id))
		if e != nil {
			return e
		}
		if n != 1 {
			return fail("SSO_FLOW_UNUSABLE")
		}
		if e := audit.WriteWithin(ctx, q, a.Me.User.ID, a.Me.User.ID, audience, "synthetic.saml_request_claimed", "success", "sso_flow", r.id, "", requestID); e != nil {
			return e
		}
		out = samlProbeClaim{id: r.id, request: r.request, claim: claim, tenant: tenant, connector: connector, callback: callback, actor: a, credential: r.credential, revision: r.revision, version: r.version, expires: r.expires}
		return nil
	})
	if e != nil {
		return samlProbeClaim{}, e
	}
	return out, nil
}
