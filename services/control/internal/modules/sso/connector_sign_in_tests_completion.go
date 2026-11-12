package sso

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

type claimedConnectorTest struct {
	row              connectorTestRow
	connector        Connector
	verifier, secret string
}

func (t *ConnectorSignInTests) claim(ctx context.Context, a identity.Auth, connector, state, browser, requestID string) (claimedConnectorTest, error) {
	var result claimedConnectorTest
	var refusal error
	err := t.within(ctx, a, connector, connectorReadFence, func(ctx context.Context, q transaction.Executor, tenant string) error {
		row, err := scanConnectorTest(q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT "+connectorTestColumns+" FROM sso_connector_sign_in_tests WHERE tenant_id=$1 AND connector_id=$2 AND state_hash=$3 AND actor_user_id=$4 AND actor_session_id=$5 AND actor_epoch=$6 FOR UPDATE", tenant, connector, security.Digest(state), a.Me.User.ID, a.SessionID, a.Epoch)))
		if errors.Is(err, sql.ErrNoRows) {
			return fail("SSO_FLOW_INVALID")
		}
		if err != nil {
			return err
		}
		if !hmac.Equal(row.browserHash, t.proof(row, browser)) {
			return fail("SSO_BROWSER_CHANGED")
		}
		if row.State != "pending" {
			return fail("SSO_FLOW_UNUSABLE")
		}
		c, err := selectConnectorWithin(ctx, q, tenant, connector)
		if err != nil {
			return err
		}
		var fresh bool
		if err = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT expires_at>clock_timestamp() FROM sso_connector_sign_in_tests WHERE tenant_id=$1 AND id=$2", tenant, row.ID)).Scan(&fresh); err != nil {
			return err
		}
		if !fresh {
			refusal = fail("SSO_FLOW_EXPIRED")
		} else if c.Revision != row.revision || c.AuthVersion != row.version {
			refusal = fail("SSO_FLOW_UNUSABLE")
		}
		bundle, openErr := t.open(row)
		if refusal == nil && openErr != nil {
			refusal = openErr
		}
		var stored storedConnector
		if refusal == nil {
			stored, refusal = t.service.readStoredConnector(ctx, q, tenant, connector)
		}
		if refusal != nil {
			code := refusal.Error()
			if code != "SSO_FLOW_EXPIRED" && code != "SSO_ENCRYPTED_STATE_INVALID" {
				code = "SSO_FLOW_UNUSABLE"
			}
			if err = t.failWithin(ctx, q, row, code, requestID); err != nil {
				return err
			}
			result.row = row
			result.row.State = "failed"
			result.row.ErrorCode = &code
			return nil
		}
		claim := uuid.NewString()
		changed, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, "UPDATE sso_connector_sign_in_tests SET state='claimed',claim_id=$3,secret_key_id=NULL,secret_nonce=NULL,secret_ciphertext=NULL WHERE tenant_id=$1 AND id=$2 AND state='pending'", tenant, row.ID, claim))
		if err != nil {
			return err
		}
		if changed != 1 {
			return fail("SSO_FLOW_UNUSABLE")
		}
		row.State = "claimed"
		row.claim = sql.NullString{String: claim, Valid: true}
		result = claimedConnectorTest{row: row, connector: c, verifier: bundle[2], secret: stored.secret}
		return nil
	})
	if err != nil {
		return claimedConnectorTest{}, err
	}
	return result, refusal
}

func (t *ConnectorSignInTests) failWithin(ctx context.Context, q transaction.Executor, row connectorTestRow, code, requestID string) error {
	changed, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_connector_sign_in_tests SET state='failed',error_code=$3,finished_at=clock_timestamp(),secret_key_id=NULL,secret_nonce=NULL,secret_ciphertext=NULL WHERE tenant_id=$1 AND id=$2 AND state IN ('pending','claimed')`, row.tenant, row.ID, code))
	if err != nil {
		return err
	}
	if changed != 1 {
		return fail("SSO_FLOW_UNUSABLE")
	}
	return audit.WriteWithin(ctx, q, "", "", "admin", "sso.connector_tested", "failure", "sso_connector", row.ConnectorID, code, requestID)
}

func (t *ConnectorSignInTests) finish(ctx context.Context, a identity.Auth, f claimedConnectorTest, identity providerIdentity, requestID string) (ConnectorSignInTestResult, error) {
	var out ConnectorSignInTestResult
	err := t.within(ctx, a, f.row.ConnectorID, connectorReadFence, func(ctx context.Context, q transaction.Executor, tenant string) error {
		c, err := selectConnectorWithin(ctx, q, tenant, f.row.ConnectorID)
		if err != nil {
			return err
		}
		if c.Revision != f.row.revision || c.AuthVersion != f.row.version {
			return fail("SSO_FLOW_UNUSABLE")
		}
		changed, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_connector_sign_in_tests SET state='succeeded',subject=$4,login=NULLIF($5,''),display_name=NULLIF($6,''),finished_at=clock_timestamp() WHERE tenant_id=$1 AND id=$2 AND claim_id=$3 AND state='claimed' AND expires_at>clock_timestamp()`, tenant, f.row.ID, f.row.claim.String, identity.subject, identity.login, identity.name))
		if err != nil {
			return err
		}
		if changed != 1 {
			return fail("SSO_FLOW_UNUSABLE")
		}
		if err = audit.WriteWithin(ctx, q, a.Me.User.ID, "", "admin", "sso.connector_tested", "success", "sso_connector", f.row.ConnectorID, "interactive_sign_in", requestID); err != nil {
			return err
		}
		row, err := scanConnectorTest(q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT "+connectorTestColumns+" FROM sso_connector_sign_in_tests WHERE tenant_id=$1 AND id=$2", tenant, f.row.ID)))
		out = row.ConnectorSignInTestResult
		return err
	})
	if err != nil {
		return ConnectorSignInTestResult{}, err
	}
	return out, nil
}

// Complete consumes a test's proof once before provider I/O. It returns a
// minimized provider identity and never calls account binding or session issuance.
func (t *ConnectorSignInTests) Complete(ctx context.Context, a identity.Auth, connector, state, browser, code string, denied bool, requestID string) (ConnectorSignInTestResult, error) {
	if err := t.ready(a); err != nil {
		return ConnectorSignInTestResult{}, err
	}
	if ctx == nil || !canonicalID(connector) || !canonicalID(requestID) || !security.ValidToken(state) || !security.ValidToken(browser) || denied && code != "" || !denied && !safeOpaque(code, maximumAuthorizationCodeBytes) {
		return ConnectorSignInTestResult{}, fail("SSO_FLOW_INVALID")
	}
	if !t.service.enabled {
		return ConnectorSignInTestResult{}, fail("SSO_DISABLED")
	}
	if err := t.service.Identity.RequireBusinessIntegrity(ctx); err != nil {
		return ConnectorSignInTestResult{}, err
	}
	f, err := t.claim(ctx, a, connector, state, browser, requestID)
	if err != nil {
		return ConnectorSignInTestResult{ID: f.row.ID}, err
	}
	var out ConnectorSignInTestResult
	if denied {
		err = fail("SSO_PROVIDER_DENIED")
	} else {
		var p *giteaProvider
		p, err = t.provider(connector, f.connector.Origin)
		if err == nil {
			callback, _ := t.callback(connector)
			var subject providerIdentity
			subject, err = p.exchange(ctx, f.connector.ClientID, f.secret, callback, code, f.verifier)
			f.secret, f.verifier = "", ""
			if err == nil {
				out, err = t.finish(ctx, a, f, subject, requestID)
			}
		}
	}
	f.secret, f.verifier = "", ""
	if err == nil {
		return out, nil
	}
	if outcome, found := transaction.CommitOutcome(err); found && outcome != transaction.CommitRolledBack {
		return ConnectorSignInTestResult{}, fail(string(outcome))
	}
	err = publicFlowError(err)
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.Seconds(t.service.policy.CleanupTimeoutSeconds))
	defer cancel()
	store, storageErr := t.service.catalogRepository()
	if storageErr != nil {
		return ConnectorSignInTestResult{}, storageErr
	}
	cleanupErr := store.Within(cleanup, false, func(ctx context.Context, q transaction.Executor) error {
		var id string
		if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT id::text FROM sso_connectors WHERE tenant_id=$1 AND id=$2 FOR SHARE", f.row.tenant, f.row.ConnectorID)).Scan(&id); e != nil {
			return e
		}
		tenant, e := connectorTenant(ctx, q, connectorReadFence)
		if e != nil {
			return e
		}
		if tenant != f.row.tenant {
			return fail("SSO_FLOW_UNUSABLE")
		}
		return t.failWithin(ctx, q, f.row, err.Error(), requestID)
	})
	if cleanupErr != nil {
		return ConnectorSignInTestResult{}, sourceOutcome(cleanupErr)
	}
	return ConnectorSignInTestResult{ID: f.row.ID}, err
}
