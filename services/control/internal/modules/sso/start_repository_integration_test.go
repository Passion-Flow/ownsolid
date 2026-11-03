//go:build repository_integration

package sso

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func startActor(t *testing.T, f sourceRepositoryFixture, password, audience string, sourced bool) (identity.Auth, string) {
	t.Helper()
	var token string
	if sourced {
		source := f.source
		if audience == "portal" {
			source = connectorFlow(t, f, source.ConnectorID, audience, "succeeded")
			source.BindingID = f.source.BindingID
		}
		var issued identity.IssuedSession
		if e := f.f.Store.Within(f.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var e error
			issued, e = f.accounts.IssueSessionWithin(ctx, q, f.f.User, 1, audience, "", uuid.NewString(), &source)
			return e
		}); e != nil {
			t.Fatal("actual source issuance", e)
		}
		token = issued.Token
	} else {
		_, value, _, e := f.accounts.Login(f.ctx, "synthetic+"+f.f.User+"@example.invalid", password, audience, "", uuid.NewString())
		if e != nil {
			t.Fatal("actual local start actor", e)
		}
		token = value
	}
	a, e := f.accounts.Authenticate(f.ctx, token, audience)
	if e != nil {
		t.Fatal("actual current start authority", e)
	}
	return a, token
}

func startCounts(t *testing.T, f sourceRepositoryFixture) [2]int {
	t.Helper()
	var out [2]int
	for i, statement := range []transaction.Statement{
		f.f.Statement(`SELECT COUNT(*) FROM sso_flows WHERE tenant_id=$1`, `SELECT COUNT(*) FROM sso_flows WHERE tenant_id=?`, f.f.Tenant),
		f.f.Statement(`SELECT COUNT(*) FROM audit_events WHERE tenant_id=$1 AND action='sso.flow_started' AND outcome='success'`, `SELECT COUNT(*) FROM audit_events WHERE tenant_id=? AND action='sso.flow_started' AND outcome='success'`, f.f.Tenant),
	} {
		if e := f.f.Store.QueryRow(f.ctx, statement).Scan(&out[i]); e != nil {
			t.Fatal("actual scoped flow/audit inventory", e)
		}
	}
	return out
}

func TestActualSSOStartPersistedPKCEAndCurrentAccount(t *testing.T) {
	for _, scenario := range []struct{ name, audience, mode, previous string }{
		{"anonymous_portal", "portal", ModeLogin, ""}, {"anonymous_admin", "admin", ModeLogin, ""}, {"expired_previous_portal", "portal", ModeLogin, "expired"},
		{"local_portal_login", "portal", ModeLogin, "local"}, {"local_admin_login", "admin", ModeLogin, "local"},
		{"local_portal_link", "portal", ModeLink, "local"}, {"local_admin_link", "admin", ModeLink, "local"},
		{"sourced_portal_login", "portal", ModeLogin, "source"}, {"sourced_admin_link", "admin", ModeLink, "source"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f, password := bindingRepositoryFixture(t)
			input := StartInput{Mode: scenario.mode}
			reason := "Explicit synthetic account link"
			var actor *identity.Auth
			previous := ""
			if scenario.previous == "expired" {
				previous = security.Token()
			} else if scenario.previous != "" {
				a, token := startActor(t, f, password, scenario.audience, scenario.previous == "source")
				actor, previous = &a, token
			}
			if scenario.mode == ModeLink {
				input.Password, input.Reason = &password, &reason
			}
			before := startCounts(t, f)
			result, e := f.s.Start(f.ctx, f.source.ConnectorID, scenario.audience, previous, actor, input, uuid.NewString())
			if e != nil || !security.ValidToken(result.BrowserToken) || !connectorTimestamp(result.ExpiresAt) {
				t.Fatal("actual paired start", e)
			}
			authorization, e := url.Parse(result.AuthorizationURL)
			if e != nil {
				t.Fatal("actual authorization URL shape")
			}
			query := authorization.Query()
			state := query.Get("state")
			callback, e := f.s.callback(f.source.ConnectorID, scenario.audience)
			if e != nil {
				t.Fatal(e)
			}
			if authorization.Scheme+"://"+authorization.Host != f.accounts.Config.SSO.Providers[0].Origin || authorization.Path != "/login/oauth/authorize" || !security.ValidToken(state) || query.Get("redirect_uri") != callback || query.Get("response_type") != "code" || query.Get("scope") != GiteaScope || query.Get("code_challenge_method") != "S256" {
				t.Fatal("actual approved callback/PKCE authorization contract")
			}
			var id, mode, audience, status string
			var sealed sealedSecret
			var stateHash, browserHash, previousHash []byte
			var previousSession, user, session, storedReason sql.NullString
			var epoch, credential sql.NullInt64
			var revision, version int64
			var created, expires time.Time
			if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT id::text,mode,audience,state,connector_revision,auth_version,state_hash,browser_hash,previous_cookie_hash,previous_session_id::text,initiating_user_id::text,initiating_session_id::text,initiating_epoch,credential_version,reason,verifier_key_id,verifier_nonce,verifier_ciphertext,created_at,expires_at FROM sso_flows WHERE tenant_id=$1 AND connector_id=$2 AND state_hash=$3`, `SELECT id,mode,audience,state,connector_revision,auth_version,state_hash,browser_hash,previous_cookie_hash,previous_session_id,initiating_user_id,initiating_session_id,initiating_epoch,credential_version,reason,verifier_key_id,verifier_nonce,verifier_ciphertext,created_at,expires_at FROM sso_flows WHERE tenant_id=? AND connector_id=? AND state_hash=?`, f.f.Tenant, f.source.ConnectorID, security.Digest(state))).Scan(&id, &mode, &audience, &status, &revision, &version, &stateHash, &browserHash, &previousHash, &previousSession, &user, &session, &epoch, &credential, &storedReason, &sealed.keyID, &sealed.nonce, &sealed.ciphertext, &created, &expires); e != nil {
				t.Fatal("independent actual pending row", e)
			}
			if mode != scenario.mode || audience != scenario.audience || status != "pending" || revision != 1 || version != 1 || !bytes.Equal(stateHash, security.Digest(state)) || !bytes.Equal(browserHash, f.s.browserProof(f.source.ConnectorID, scenario.audience, result.BrowserToken)) || !bytes.Equal(previousHash, security.Digest(security.Binding(previous))) || !result.ExpiresAt.Equal(expires) || expires.Sub(created) < time.Duration(f.s.policy.FlowSeconds)*time.Second || expires.Sub(created) > time.Duration(f.s.policy.FlowSeconds+2)*time.Second {
				t.Fatal("persisted state/browser/expiry/current connector mismatch")
			}
			plain, e := f.s.keys.open(secretScope{f.f.Tenant, f.source.ConnectorID, id, flowVerifierPurpose}, sealed)
			if e != nil || !security.ValidToken(string(plain)) || bytes.Contains(sealed.ciphertext, plain) {
				t.Fatal("real encrypted PKCE verifier", e)
			}
			sum := sha256.Sum256(plain)
			if query.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(sum[:]) {
				t.Fatal("S256 challenge does not match actual encrypted verifier")
			}
			if _, e := f.s.keys.open(secretScope{uuid.NewString(), f.source.ConnectorID, id, flowVerifierPurpose}, sealed); e == nil {
				t.Fatal("verifier AAD accepted foreign tenant")
			}
			if actor != nil {
				if !previousSession.Valid || previousSession.String != actor.SessionID {
					t.Fatal("current previous browser session not captured")
				}
			} else if previousSession.Valid {
				t.Fatal("anonymous start invented previous session")
			}
			if scenario.mode == ModeLink {
				if !user.Valid || user.String != f.f.User || !session.Valid || session.String != actor.SessionID || !epoch.Valid || epoch.Int64 != actor.Epoch || !credential.Valid || credential.Int64 != 1 || !storedReason.Valid || storedReason.String != reason {
					t.Fatal("current link proof fields mismatch")
				}
			} else if user.Valid || session.Valid || epoch.Valid || credential.Valid || storedReason.Valid {
				t.Fatal("login invented link confirmation")
			}
			if after := startCounts(t, f); after != ([2]int{before[0] + 1, before[1] + 1}) {
				t.Fatal("flow/audit did not share one actual acceptance")
			}
		})
	}
}
