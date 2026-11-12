package sso

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// ConnectorSignInTests owns diagnostic authentication without local login authority.
// Its explicit composition requires the separately admitted PostgreSQL test schema.
type ConnectorSignInTests struct{ service *Service }

// ConnectorSignInTestInput binds a start to the connector the Admin inspected.
type ConnectorSignInTestInput struct {
	Revision int64  `json:"revision"`
	Reason   string `json:"reason"`
}

// ConnectorSignInTestStart contains the exact pending start and its private cookie proof.
type ConnectorSignInTestStart struct {
	ID               string    `json:"id"`
	AuthorizationURL string    `json:"authorization_url"`
	ExpiresAt        time.Time `json:"expires_at"`
	BrowserToken     string    `json:"-"`
}

// ConnectorSignInTestResult is a minimized diagnostic result, never a local identity.
type ConnectorSignInTestResult struct {
	ID          string     `json:"id"`
	ConnectorID string     `json:"connector_id"`
	State       string     `json:"state"`
	Subject     *string    `json:"subject"`
	Login       *string    `json:"login"`
	DisplayName *string    `json:"display_name"`
	ErrorCode   *string    `json:"error_code"`
	ExpiresAt   time.Time  `json:"expires_at"`
	FinishedAt  *time.Time `json:"finished_at"`
}

type connectorTestRow struct {
	ConnectorSignInTestResult
	tenant, user, session               string
	epoch, revision, version            int64
	fingerprint, browserHash, stateHash []byte
	sealed                              sealedSecret
	claim                               sql.NullString
}

const connectorTestColumns = `id::text,connector_id::text,state,subject,login,display_name,error_code,expires_at,finished_at,tenant_id::text,actor_user_id::text,actor_session_id::text,actor_epoch,connector_revision,auth_version,fingerprint,browser_hash,secret_key_id,secret_nonce,secret_ciphertext,claim_id::text,state_hash`

func scanConnectorTest(row transaction.Row) (connectorTestRow, error) {
	var r connectorTestRow
	var key sql.NullString
	err := row.Scan(&r.ID, &r.ConnectorID, &r.State, &r.Subject, &r.Login, &r.DisplayName, &r.ErrorCode, &r.ExpiresAt, &r.FinishedAt, &r.tenant, &r.user, &r.session, &r.epoch, &r.revision, &r.version, &r.fingerprint, &r.browserHash, &key, &r.sealed.nonce, &r.sealed.ciphertext, &r.claim, &r.stateHash)
	r.sealed.keyID = key.String
	r.ExpiresAt = r.ExpiresAt.UTC()
	return r, err
}

// NewConnectorSignInTests checks the required PostgreSQL diagnostic projection.
// Formal catalog, grant and migration admission belongs to the composition owner.
// No worker, private key or provider connection is created here.
func NewConnectorSignInTests(ctx context.Context, s *Service) (*ConnectorSignInTests, error) {
	if ctx == nil || s == nil || s.Identity == nil {
		return nil, fail("SSO_CONFIGURATION_REQUIRED")
	}
	q, err := s.catalogRepository()
	if err != nil {
		return nil, err
	}
	if q.Dialect() != transaction.PostgreSQL {
		return nil, fail("SSO_CONFIGURATION_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy.OperationSeconds))
	defer cancel()
	rows, err := q.Query(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT "+connectorTestColumns+" FROM sso_connector_sign_in_tests WHERE false"))
	if err != nil {
		return nil, fail("SERVICE_NOT_READY")
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, fail("SERVICE_NOT_READY")
	}
	return &ConnectorSignInTests{service: s}, nil
}

func (t *ConnectorSignInTests) ready(a identity.Auth) error {
	if !platformAdministrator(a) {
		return fail("ACCESS_DENIED")
	}
	if t == nil || t.service == nil || t.service.Identity == nil {
		return fail("SERVICE_NOT_READY")
	}
	return nil
}

func (t *ConnectorSignInTests) callback(id string) (string, error) {
	s := t.service
	if !canonicalID(id) || !config.SSOOrigin(s.Identity.Config.Server.AdminOrigin) {
		return "", fail("SSO_AUTHORIZATION_INPUT_INVALID")
	}
	return s.Identity.Config.Server.AdminOrigin + "/api/v1/admin/sso/connectors/" + id + "/sign-in-tests/callback", nil
}

func (t *ConnectorSignInTests) provider(id, origin string) (*giteaProvider, error) {
	s := t.service
	c, ok := s.approved[origin]
	if !s.enabled {
		return nil, fail("SSO_DISABLED")
	}
	if !ok {
		return nil, fail("SSO_PROVIDER_DESTINATION_REJECTED")
	}
	callback, err := t.callback(id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := "connector-test/" + id
	if p := s.clients[key]; p != nil {
		if p.configuration.Origin != origin {
			return nil, fail("SSO_PROVIDER_CONFIGURATION_INVALID")
		}
		return p, nil
	}
	p, err := newGiteaProvider(c, s.policy, []string{callback}, s.slots)
	if err != nil {
		return nil, err
	}
	s.clients[key] = p
	return p, nil
}

func (t *ConnectorSignInTests) proof(row connectorTestRow, browser string) []byte {
	callback, err := t.callback(row.ConnectorID)
	if err != nil {
		return nil
	}
	return security.MAC(t.service.Identity.HMACKey, "sso-connector-test-browser-v1", callback, row.tenant, row.user, row.session, row.ID, browser)
}

func (t *ConnectorSignInTests) open(row connectorTestRow) ([3]string, error) {
	var bundle [3]string
	raw, err := t.service.keys.open(secretScope{row.tenant, row.ConnectorID, row.ID, connectorTestPurpose}, row.sealed)
	if err != nil {
		return bundle, err
	}
	defer clear(raw)
	var fields []string
	if json.Unmarshal(raw, &fields) != nil || len(fields) != len(bundle) {
		return [3]string{}, errEncryptedState
	}
	copy(bundle[:], fields)
	for _, v := range bundle {
		if !security.ValidToken(v) {
			return [3]string{}, errEncryptedState
		}
	}
	if !hmac.Equal(row.stateHash, security.Digest(bundle[0])) || !hmac.Equal(row.browserHash, t.proof(row, bundle[1])) {
		return [3]string{}, errEncryptedState
	}
	return bundle, nil
}

func (t *ConnectorSignInTests) within(ctx context.Context, a identity.Auth, connector string, fence connectorFence, fn func(context.Context, transaction.Executor, string) error) error {
	return t.service.actorTransaction(ctx, a, fence, connectorShared, func(_ context.Context, _ transaction.Executor, tenant string) ([]connectorLock, error) {
		return []connectorLock{{tenant, connector}}, nil
	}, fn)
}
