package sso

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

// Start persists one diagnostic intent. Exact pending retries return the original
// random start; no credential, session or account binding is created or rotated.
func (t *ConnectorSignInTests) Start(ctx context.Context, a identity.Auth, connector string, input ConnectorSignInTestInput, key, requestID string) (ConnectorSignInTestStart, error) {
	if err := t.ready(a); err != nil {
		return ConnectorSignInTestStart{}, err
	}
	if ctx == nil || !canonicalID(connector) || !canonicalID(key) || !canonicalID(requestID) || input.Revision < 1 || input.Revision > identity.MaximumAuthVersion || !validReason(input.Reason) {
		return ConnectorSignInTestStart{}, fail("VALIDATION_FAILED")
	}
	s := t.service
	if !s.enabled {
		return ConnectorSignInTestStart{}, fail("SSO_DISABLED")
	}
	if s.keys == nil {
		return ConnectorSignInTestStart{}, fail("SSO_KEYRING_INVALID")
	}
	if err := s.Identity.RequireNewBusiness(ctx); err != nil {
		return ConnectorSignInTestStart{}, err
	}
	fingerprint, err := s.fingerprint("connector_sign_in_test", connector, struct {
		Input   ConnectorSignInTestInput
		Session string
		Epoch   int64
	}{input, a.SessionID, a.Epoch})
	if err != nil {
		return ConnectorSignInTestStart{}, err
	}
	var out ConnectorSignInTestStart
	err = t.within(ctx, a, connector, connectorCreateFence, func(ctx context.Context, q transaction.Executor, tenant string) error {
		c, err := selectConnectorWithin(ctx, q, tenant, connector)
		if errors.Is(err, sql.ErrNoRows) {
			return fail("SSO_CONNECTOR_NOT_FOUND")
		}
		if err != nil {
			return err
		}
		if c.Revision != input.Revision {
			return fail("SSO_REVISION_CONFLICT")
		}
		p, err := t.provider(connector, c.Origin)
		if err != nil {
			return err
		}
		row, err := scanConnectorTest(q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT "+connectorTestColumns+" FROM sso_connector_sign_in_tests WHERE tenant_id=$1 AND actor_user_id=$2 AND idempotency_key=$3 FOR UPDATE", tenant, a.Me.User.ID, key)))
		var bundle [3]string
		if err == nil {
			if row.ConnectorID != connector || !hmac.Equal(row.fingerprint, fingerprint) {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			if row.State != "pending" || row.version != c.AuthVersion {
				return fail("SSO_FLOW_UNUSABLE")
			}
			var fresh bool
			if err = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT expires_at>clock_timestamp() FROM sso_connector_sign_in_tests WHERE tenant_id=$1 AND id=$2", tenant, row.ID)).Scan(&fresh); err != nil {
				return err
			}
			if !fresh {
				return fail("SSO_FLOW_EXPIRED")
			}
			bundle, err = t.open(row)
			if err != nil {
				return err
			}
		} else if errors.Is(err, sql.ErrNoRows) {
			if err = s.verifyConnectorCredentialWithin(ctx, q, tenant, connector); err != nil {
				return err
			}
			var count int
			if err = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT count(*) FROM sso_connector_sign_in_tests WHERE tenant_id=$1 AND state IN ('pending','claimed') AND expires_at>clock_timestamp()", tenant)).Scan(&count); err != nil {
				return err
			}
			if count >= s.policy.MaximumPendingFlows {
				return fail("SSO_CAPACITY_EXCEEDED")
			}
			row = connectorTestRow{ConnectorSignInTestResult: ConnectorSignInTestResult{ID: uuid.NewString(), ConnectorID: connector}, tenant: tenant, user: a.Me.User.ID, session: a.SessionID}
			bundle = [3]string{security.Token(), security.Token(), security.Token()}
			raw, err := json.Marshal(bundle)
			if err != nil {
				return err
			}
			sealed, err := s.keys.seal(secretScope{tenant, connector, row.ID, connectorTestPurpose}, raw)
			clear(raw)
			if err != nil {
				return err
			}
			changed, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `INSERT INTO sso_connector_sign_in_tests(id,tenant_id,connector_id,connector_revision,auth_version,actor_user_id,actor_session_id,actor_epoch,idempotency_key,fingerprint,reason,state_hash,browser_hash,secret_key_id,secret_nonce,secret_ciphertext,expires_at)
    VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,clock_timestamp()+$17::bigint*interval '1 microsecond')`, row.ID, tenant, connector, c.Revision, c.AuthVersion, a.Me.User.ID, a.SessionID, a.Epoch, key, fingerprint, input.Reason, security.Digest(bundle[0]), t.proof(row, bundle[1]), sealed.keyID, sealed.nonce, sealed.ciphertext, config.Seconds(s.policy.FlowSeconds).Microseconds()))
			if err != nil {
				return err
			}
			if changed != 1 {
				return fail("STORAGE_UNAVAILABLE")
			}
			if err = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT expires_at FROM sso_connector_sign_in_tests WHERE tenant_id=$1 AND id=$2", tenant, row.ID)).Scan(&row.ExpiresAt); err != nil {
				return err
			}
			if err = audit.WriteWithin(ctx, q, a.Me.User.ID, "", "admin", "sso.connector_tested", "accepted", "sso_connector", connector, "interactive_start", requestID); err != nil {
				return err
			}
		} else {
			return err
		}
		callback, err := t.callback(connector)
		if err != nil {
			return err
		}
		authorization, err := p.authorizationURL(c.ClientID, callback, bundle[0], bundle[2])
		if err != nil {
			return err
		}
		out = ConnectorSignInTestStart{ID: row.ID, AuthorizationURL: authorization, ExpiresAt: row.ExpiresAt.UTC(), BrowserToken: bundle[1]}
		return nil
	})
	if err != nil {
		return ConnectorSignInTestStart{}, err
	}
	return out, nil
}

// Get returns diagnostic state only to the exact current initiating Admin session.
func (t *ConnectorSignInTests) Get(ctx context.Context, a identity.Auth, connector, id string) (ConnectorSignInTestResult, error) {
	if err := t.ready(a); err != nil {
		return ConnectorSignInTestResult{}, err
	}
	if ctx == nil || !canonicalID(connector) || !canonicalID(id) {
		return ConnectorSignInTestResult{}, fail("VALIDATION_FAILED")
	}
	var out ConnectorSignInTestResult
	err := t.within(ctx, a, connector, connectorReadFence, func(ctx context.Context, q transaction.Executor, tenant string) error {
		row, err := scanConnectorTest(q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT "+connectorTestColumns+" FROM sso_connector_sign_in_tests WHERE tenant_id=$1 AND connector_id=$2 AND id=$3 AND actor_user_id=$4 AND actor_session_id=$5 AND actor_epoch=$6", tenant, connector, id, a.Me.User.ID, a.SessionID, a.Epoch)))
		if errors.Is(err, sql.ErrNoRows) {
			return fail("SSO_FLOW_INVALID")
		}
		if err != nil {
			return err
		}
		c, e := selectConnectorWithin(ctx, q, tenant, connector)
		if e != nil {
			return e
		}
		if c.Revision != row.revision || c.AuthVersion != row.version {
			return fail("SSO_REVISION_CONFLICT")
		}
		out = row.ConnectorSignInTestResult
		return nil
	})
	if err != nil {
		return ConnectorSignInTestResult{}, err
	}
	return out, nil
}
