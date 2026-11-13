package sso

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"net/url"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// SAMLCompleteInput contains server-resolved entrance and protected browser
// cookie values. It is not an HTTP request body or a provider claim envelope.
type SAMLCompleteInput struct {
	ConnectorID     string `json:"-"`
	Audience        string `json:"-"`
	FlowID          string `json:"-"`
	Continuation    string `json:"-"`
	PreviousSession string `json:"-"`
}

// String redacts protected browser values in ordinary diagnostics.
func (SAMLCompleteInput) String() string { return "SAML_COMPLETION_INPUT_REDACTED" }

// GoString redacts protected browser values in detailed diagnostics.
func (SAMLCompleteInput) GoString() string { return "SAML_COMPLETION_INPUT_REDACTED" }

// NewSAMLCompletion validates the selected PostgreSQL projection without
// changing source composition, registering routes or opening a provider socket.
// The startup owner must select its returned SourceGuard exactly once after
// admitting the SAML source constraints, producer and protocol dependencies.
func NewSAMLCompletion(ctx context.Context, base *Service) (*SAMLCompletion, error) {
	owner := &SAMLCompletion{service: base, legacy: base}
	if ctx == nil {
		return nil, fail("SSO_SAML_CONFIGURATION_REQUIRED")
	}
	if e := owner.ready(); e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(base.policy.OperationSeconds))
	defer cancel()
	store, e := base.catalogRepository()
	if e != nil {
		return nil, e
	}
	for _, statement := range []string{
		`SELECT id,tenant_id,connector_id,revision,metadata_digest,enabled,trust_until,maximum_session_seconds FROM sso_saml_profiles LIMIT 0`,
		`SELECT tenant_id,context_id,id,name_id_value,bound_user_id,binding_version,active FROM sso_saml_identities LIMIT 0`,
		`SELECT ` + samlCompletionRequestColumns + `,authenticated_user_id,issued_session_id,finished_at FROM sso_saml_requests LIMIT 0`,
		`SELECT tenant_id,flow_id,context_id,identity_id,request_id,continuation_hash,expires_at,source_until,completed_binding_version FROM sso_saml_results LIMIT 0`,
	} {
		rows, e := store.Query(ctx, transaction.SQL(transaction.PostgreSQL, statement))
		if e != nil {
			return nil, fail("SSO_SAML_SCHEMA_REQUIRED")
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return nil, fail("SSO_SAML_SCHEMA_REQUIRED")
		}
	}
	return owner, nil
}
func (p *SAMLCompletion) ready() error {
	if p == nil || p.service == nil || p.service.Identity == nil {
		return fail("SSO_SAML_CONFIGURATION_REQUIRED")
	}
	if e := p.service.policy.Validate(); e != nil {
		return e
	}
	store, e := p.service.catalogRepository()
	if e != nil {
		return e
	}
	if store.Dialect() != transaction.PostgreSQL {
		return fail("SSO_TRANSACTION_STORAGE_MISMATCH")
	}
	return nil
}
func (p *SAMLCompletion) callback(connector, audience string) (string, error) {
	if !canonicalID(connector) || (audience != "portal" && audience != "admin") {
		return "", fail("SSO_FLOW_INVALID")
	}
	origin := p.service.Identity.Config.Server.PortalOrigin
	if audience == "admin" {
		origin = p.service.Identity.Config.Server.AdminOrigin
	}
	if !config.SSOOrigin(origin) {
		return "", fail("SSO_SAML_CONFIGURATION_REQUIRED")
	}
	return origin + "/api/v1/sso/saml/connectors/" + url.PathEscape(connector) + "/acs", nil
}

type samlCompletionRequest struct {
	id, tenant, connector, audience, callback, request, user, session, state, reason string
	previous, metadataDigest                                                         []byte
	epoch, credential, revision, version, contextRevision                            int64
	contextID                                                                        string
	expires                                                                          time.Time
}

const samlCompletionRequestColumns = `id::text,tenant_id::text,connector_id::text,audience,callback,request_id,user_id::text,session_id::text,state,previous_hash,epoch,credential_version,connector_revision,auth_version,expires_at,context_id::text,context_revision,metadata_digest,reason`

func scanSAMLCompletionRequest(row transaction.Row) (samlCompletionRequest, error) {
	var r samlCompletionRequest
	e := row.Scan(&r.id, &r.tenant, &r.connector, &r.audience, &r.callback, &r.request, &r.user, &r.session, &r.state, &r.previous, &r.epoch, &r.credential, &r.revision, &r.version, &r.expires, &r.contextID, &r.contextRevision, &r.metadataDigest, &r.reason)
	if e != nil {
		return samlCompletionRequest{}, e
	}
	r.expires = r.expires.UTC()
	if !canonicalID(r.id) || !canonicalID(r.tenant) || !canonicalID(r.connector) || !canonicalID(r.user) || !canonicalID(r.session) || !canonicalID(r.contextID) || len(r.previous) != sha256.Size || len(r.metadataDigest) != sha256.Size || !validReason(r.reason) || !samlCompletionVersion(r.epoch) || !samlCompletionVersion(r.credential) || !samlCompletionVersion(r.revision) || !samlCompletionVersion(r.version) || !samlCompletionVersion(r.contextRevision) {
		return samlCompletionRequest{}, fail("STORAGE_UNAVAILABLE")
	}
	return r, nil
}

type samlCompletionProfile struct {
	contextID string
	revision  int64
	digest    []byte
}

func loadSAMLCompletionProfile(ctx context.Context, q transaction.Executor, tenant, connector string) (samlCompletionProfile, error) {
	var p samlCompletionProfile
	e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT id::text,revision,metadata_digest FROM sso_saml_profiles WHERE tenant_id=$1 AND connector_id=$2 AND enabled AND trust_until>clock_timestamp() FOR SHARE`, tenant, connector)).Scan(&p.contextID, &p.revision, &p.digest)
	if errors.Is(e, sql.ErrNoRows) {
		return samlCompletionProfile{}, fail("SSO_FLOW_UNUSABLE")
	}
	if e != nil {
		return samlCompletionProfile{}, e
	}
	if !canonicalID(p.contextID) || !samlCompletionVersion(p.revision) || len(p.digest) != sha256.Size {
		return samlCompletionProfile{}, fail("STORAGE_UNAVAILABLE")
	}
	return p, nil
}
func selectSAMLCompletionConnector(ctx context.Context, q transaction.Executor, tenant, id string) (Connector, error) {
	var c Connector
	e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT id::text,provider,origin,enabled,revision,auth_version FROM sso_connectors WHERE tenant_id=$1 AND id=$2 AND provider='saml'`, tenant, id)).Scan(&c.ID, &c.Provider, &c.Origin, &c.Enabled, &c.Revision, &c.AuthVersion)
	if errors.Is(e, sql.ErrNoRows) {
		return Connector{}, fail("SSO_FLOW_UNUSABLE")
	}
	if e != nil {
		return Connector{}, e
	}
	if !canonicalID(c.ID) || !samlCompletionVersion(c.Revision) || !samlCompletionVersion(c.AuthVersion) {
		return Connector{}, fail("STORAGE_UNAVAILABLE")
	}
	return c, nil
}

func samlCompletionVersion(value int64) bool {
	return value >= 1 && value <= identity.MaximumAuthVersion
}
