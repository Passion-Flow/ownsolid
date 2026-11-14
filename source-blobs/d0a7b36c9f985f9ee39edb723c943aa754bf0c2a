package sso

import (
	"context"
	"errors"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const maximumStartupSecretRecords = config.SSOMaximumConnectors + config.SSOMaximumPendingFlows

type Service struct {
	Identity *identity.Service
	keys     *Keyring
	policy   config.SSOPolicy
	enabled  bool
	approved map[string]config.SSOProvider
	// origins lists the approved provider origins in configuration order.
	origins []string
	slots    chan struct{}
	mu       sync.Mutex
	clients  map[string]*giteaProvider
}

func fail(code string) error { return errors.New(code) }

// New validates enabled-mode private assets before exposing any SSO capability.
// Disabled recovery mode deliberately opens neither private files nor provider sockets.
func New(accounts *identity.Service) (*Service, error) {
	if accounts == nil {
		return nil, fail("SSO_CONFIGURATION_REQUIRED")
	}
	c := accounts.Config.SSO
	policy := accounts.Config.Policy().SSO
	if e := c.Validate(); e != nil {
		return nil, e
	}
	if e := policy.Validate(); e != nil {
		return nil, e
	}
	s := &Service{Identity: accounts, enabled: c.Enabled, policy: policy, approved: map[string]config.SSOProvider{}, clients: map[string]*giteaProvider{}, slots: make(chan struct{}, policy.MaxConcurrentExchanges)}
	if !c.Enabled {
		return s, nil
	}
	if !config.SSOOrigin(accounts.Config.Server.PortalOrigin) || !config.SSOOrigin(accounts.Config.Server.AdminOrigin) {
		return nil, fail("SSO_PROVIDER_CONFIGURATION_INVALID")
	}
	keys, e := LoadKeyring(c.KeyringFile)
	if e != nil {
		return nil, e
	}
	s.keys = keys
	for _, provider := range c.Providers {
		// Validate CA/transport construction at startup even before a connector is created.
		client, e := newGiteaProvider(provider, policy, []string{accounts.Config.Server.PortalOrigin}, s.slots)
		if e != nil {
			s.Close()
			return nil, e
		}
		client.transport.CloseIdleConnections()
		s.approved[provider.Origin] = provider
		s.origins = append(s.origins, provider.Origin)
	}
	startup, cancel := context.WithTimeout(context.Background(), config.Seconds(policy.OperationSeconds))
	defer cancel()
	if e = s.verifyStoredSecrets(startup); e != nil {
		s.Close()
		return nil, e
	}
	return s, nil
}

// VerifyStored checks encrypted current state without opening provider sockets.
func (s *Service) VerifyStored(ctx context.Context) error {
	if s == nil || s.Identity == nil {
		return fail("SERVICE_NOT_READY")
	}
	if !s.enabled {
		return nil
	}
	return s.verifyStoredSecrets(ctx)
}

func (s *Service) verifyStoredSecrets(ctx context.Context) error {
	store, e := s.Identity.Repository()
	if e != nil {
		return e
	}
	if store.Dialect() == transaction.Dameng {
		return s.verifyDamengStoredSecrets(ctx, store)
	}
	rows, e := store.Query(ctx, sourceSQL(store,
		`SELECT c.tenant_id::text,c.id::text,c.id::text,'connector_client_secret',c.secret_key_id,c.secret_nonce,c.secret_ciphertext FROM sso_connectors c JOIN installation_state i ON i.tenant_id=c.tenant_id WHERE i.id=1 AND i.bootstrap_complete UNION ALL SELECT f.tenant_id::text,f.connector_id::text,f.id::text,'flow_pkce_verifier',f.verifier_key_id,f.verifier_nonce,f.verifier_ciphertext FROM sso_flows f JOIN installation_state i ON i.tenant_id=f.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND f.state='pending' AND f.expires_at>clock_timestamp() LIMIT $1`,
		`SELECT c.tenant_id,c.id,c.id,'connector_client_secret',c.secret_key_id,c.secret_nonce,c.secret_ciphertext FROM sso_connectors c JOIN installation_state i ON i.tenant_id=c.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 UNION ALL SELECT f.tenant_id,f.connector_id,f.id,'flow_pkce_verifier',f.verifier_key_id,f.verifier_nonce,f.verifier_ciphertext FROM sso_flows f JOIN installation_state i ON i.tenant_id=f.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND f.state='pending' AND f.expires_at>UTC_TIMESTAMP(6) LIMIT ?`,
		"", maximumStartupSecretRecords+1))
	if e != nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		if count > maximumStartupSecretRecords {
			return fail("SSO_CAPACITY_EXCEEDED")
		}
		var scope secretScope
		var sealed sealedSecret
		if e = rows.Scan(&scope.tenant, &scope.connector, &scope.record, &scope.purpose, &sealed.keyID, &sealed.nonce, &sealed.ciphertext); e != nil {
			return fail("STORAGE_UNAVAILABLE")
		}
		if e := s.verifyStoredSecret(scope, sealed); e != nil {
			return e
		}
	}
	if rows.Err() != nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	return nil
}

func (s *Service) verifyStoredSecret(scope secretScope, sealed sealedSecret) error {
	plain, e := s.keys.open(scope, sealed)
	if e != nil {
		return e
	}
	defer clear(plain)
	if scope.purpose == flowVerifierPurpose && !security.ValidToken(string(plain)) || scope.purpose == clientSecretPurpose && !safeOpaque(string(plain), maximumSecretBytes) {
		return fail("SSO_ENCRYPTED_STATE_INVALID")
	}
	return nil
}

func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, client := range s.clients {
		client.transport.CloseIdleConnections()
	}
}

func (s *Service) callback(connector, audience string) (string, error) {
	origin := s.Identity.Config.Server.PortalOrigin
	if audience == "admin" {
		origin = s.Identity.Config.Server.AdminOrigin
	} else if audience != "portal" {
		return "", fail("SSO_AUTHORIZATION_INPUT_INVALID")
	}
	if !canonicalID(connector) || !config.SSOOrigin(origin) {
		return "", fail("SSO_AUTHORIZATION_INPUT_INVALID")
	}
	return origin + "/api/v1/auth/sso/" + connector + "/callback", nil
}

func (s *Service) provider(connector, origin string) (*giteaProvider, error) {
	if !s.enabled {
		return nil, fail("SSO_DISABLED")
	}
	c, ok := s.approved[origin]
	if !ok || !canonicalID(connector) {
		return nil, fail("SSO_PROVIDER_CONFIGURATION_INVALID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.clients[connector]; p != nil {
		if p.configuration.Origin != origin {
			return nil, fail("SSO_PROVIDER_CONFIGURATION_INVALID")
		}
		return p, nil
	}
	portal, e := s.callback(connector, "portal")
	if e != nil {
		return nil, e
	}
	admin, e := s.callback(connector, "admin")
	if e != nil {
		return nil, e
	}
	callbacks := []string{portal}
	if admin != portal {
		callbacks = append(callbacks, admin)
	}
	p, e := newGiteaProvider(c, s.policy, callbacks, s.slots)
	if e != nil {
		return nil, e
	}
	s.clients[connector] = p
	return p, nil
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func tenant(ctx context.Context, q queryer) (string, error) {
	var id string
	e := q.QueryRow(ctx, `SELECT i.tenant_id::text FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND t.status='active'`).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		return "", fail("SSO_DISABLED")
	}
	return id, e
}

func (s *Service) transaction(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, e := s.Identity.DB.Begin(ctx)
	if e != nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	defer tx.Rollback(ctx)
	if e = fn(tx); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	return nil
}
