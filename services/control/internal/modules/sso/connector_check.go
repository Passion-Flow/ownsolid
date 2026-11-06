package sso

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// ApprovedProvider is a provider the installation's configuration lets connectors use.
type ApprovedProvider struct {
	Provider string `json:"provider"`
	Origin   string `json:"origin"`
}

// Configuration is what the installation lets single sign-on use: whether it is on, the
// provider origins a connector may name, and the origins whose callback URLs a provider must
// accept, `{origin}/api/v1/auth/sso/{connector_id}/callback`.
type Configuration struct {
	Enabled         bool               `json:"enabled"`
	Providers       []ApprovedProvider `json:"providers"`
	CallbackOrigins []string           `json:"callback_origins"`
}

// ConnectorCheck is the result of checking one stored connector against its provider.
type ConnectorCheck struct {
	ConnectorID string `json:"connector_id"`
	Outcome     string `json:"outcome"`
	// CallbackURL is the callback the provider refused, for callback_not_registered.
	CallbackURL *string `json:"callback_url"`
	// ProviderError is the provider's OAuth error code, for unexpected_answer.
	ProviderError *string   `json:"provider_error"`
	CheckedAt     time.Time `json:"checked_at"`
}

func platformAdministrator(a identity.Auth) bool {
	return a.Me.Audience == "admin" && a.Me.User.IsPlatformAdmin
}

// Configuration reports what the installation lets single sign-on use. It reads no
// stored state and opens no provider connection.
func (s *Service) Configuration(a identity.Auth) (Configuration, error) {
	if !platformAdministrator(a) {
		return Configuration{}, fail("ACCESS_DENIED")
	}
	if s == nil || s.Identity == nil {
		return Configuration{}, fail("SERVICE_NOT_READY")
	}
	result := Configuration{Enabled: s.enabled, Providers: []ApprovedProvider{}, CallbackOrigins: []string{}}
	for _, origin := range s.origins {
		result.Providers = append(result.Providers, ApprovedProvider{Provider: ProviderGitea, Origin: origin})
	}
	server := s.Identity.Config.Server
	for _, origin := range []string{server.PortalOrigin, server.AdminOrigin} {
		if !config.SSOOrigin(origin) {
			result.CallbackOrigins = []string{}
			break
		}
		if len(result.CallbackOrigins) == 0 || result.CallbackOrigins[0] != origin {
			result.CallbackOrigins = append(result.CallbackOrigins, origin)
		}
	}
	return result, nil
}

// storedConnector is a connector with its client secret opened, for one check.
type storedConnector struct {
	Connector
	secret string
}

func (s *Service) readStoredConnector(ctx context.Context, q transaction.Executor, tenant, id string) (storedConnector, error) {
	c, e := selectConnectorWithin(ctx, q, tenant, id)
	if errors.Is(e, sql.ErrNoRows) {
		return storedConnector{}, fail("SSO_CONNECTOR_NOT_FOUND")
	}
	if e != nil {
		return storedConnector{}, e
	}
	var sealed sealedSecret
	if e = q.QueryRow(ctx, sourceSQL(q, `SELECT secret_key_id,secret_nonce,secret_ciphertext FROM sso_connectors WHERE tenant_id=$1 AND id=$2`, `SELECT secret_key_id,secret_nonce,secret_ciphertext FROM sso_connectors WHERE tenant_id=? AND id=?`, "SELECT SECRET_KEY_ID,SECRET_NONCE,SECRET_CIPHERTEXT FROM SSO_CONNECTORS WHERE TENANT_ID=? AND ID=?", tenant, id)).Scan(&sealed.keyID, &sealed.nonce, &sealed.ciphertext); e != nil {
		return storedConnector{}, e
	}
	secret, e := s.keys.open(secretScope{tenant, id, id, clientSecretPurpose}, sealed)
	if e != nil {
		return storedConnector{}, e
	}
	if !safeOpaque(string(secret), maximumSecretBytes) {
		return storedConnector{}, fail("SSO_ENCRYPTED_STATE_INVALID")
	}
	return storedConnector{Connector: c, secret: string(secret)}, nil
}

// TestConnector checks a stored connector the way a sign-in would use it: its origin is still
// approved, the provider answers over the configured transport, and it accepts the client's
// credentials and every callback URL (docs/research/sso-page.md). It works on a disabled
// connector and changes no connector, flow or session; the check and its outcome are audited.
func (s *Service) TestConnector(ctx context.Context, a identity.Auth, id, requestID string) (ConnectorCheck, error) {
	if !platformAdministrator(a) {
		return ConnectorCheck{}, fail("ACCESS_DENIED")
	}
	if !canonicalID(id) || !canonicalID(requestID) {
		return ConnectorCheck{}, fail("VALIDATION_FAILED")
	}
	if s == nil || !s.enabled {
		return ConnectorCheck{}, fail("SSO_DISABLED")
	}
	if s.keys == nil || s.Identity == nil {
		return ConnectorCheck{}, fail("SSO_CONFIGURATION_REQUIRED")
	}
	var stored storedConnector
	e := s.actorTransaction(ctx, a, connectorReadFence, connectorShared, nil, func(ctx context.Context, q transaction.Executor, tenant string) error {
		var e error
		stored, e = s.readStoredConnector(ctx, q, tenant, id)
		return e
	})
	if e != nil {
		return ConnectorCheck{}, e
	}
	result, e := s.checkStored(ctx, stored)
	if e != nil {
		return ConnectorCheck{}, e
	}
	outcome := "failure"
	if result.Outcome == CheckPassed {
		outcome = "success"
	}
	// The provider was asked outside the transaction; only the record of it is written here.
	e = s.actorTransaction(ctx, a, connectorReadFence, connectorShared, nil, func(ctx context.Context, q transaction.Executor, _ string) error {
		return audit.WriteWithin(ctx, q, a.Me.User.ID, "", a.Me.Audience, "sso.connector_tested", outcome, "sso_connector", id, result.Outcome, requestID)
	})
	if e != nil {
		return ConnectorCheck{}, e
	}
	result.CheckedAt = time.Now().UTC()
	return result, nil
}

// checkStored runs the provider check for every callback URL, stopping at the first that fails.
func (s *Service) checkStored(ctx context.Context, c storedConnector) (ConnectorCheck, error) {
	result := ConnectorCheck{ConnectorID: c.ID, Outcome: CheckNotConfigured}
	if _, approved := s.approved[c.Origin]; !approved {
		return result, nil
	}
	p, e := s.provider(c.ID, c.Origin)
	if e != nil {
		return ConnectorCheck{}, e
	}
	callbacks := []string{}
	for _, audience := range []string{"portal", "admin"} {
		callback, e := s.callback(c.ID, audience)
		if e != nil {
			return ConnectorCheck{}, e
		}
		if len(callbacks) == 0 || callbacks[0] != callback {
			callbacks = append(callbacks, callback)
		}
	}
	for _, callback := range callbacks {
		check, e := p.checkClient(ctx, c.ClientID, c.secret, callback)
		if e != nil {
			return ConnectorCheck{}, e
		}
		result.Outcome = check.outcome
		if check.outcome == CheckCallbackNotRegistered {
			result.CallbackURL = &callback
		}
		if check.providerError != "" {
			result.ProviderError = &check.providerError
		}
		if check.outcome != CheckPassed {
			break
		}
	}
	return result, nil
}
