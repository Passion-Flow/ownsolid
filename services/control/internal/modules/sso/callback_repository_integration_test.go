//go:build repository_integration

package sso

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
)

func callbackStart(t *testing.T, f sourceRepositoryFixture, password, audience, mode, kind string) (StartResult, string) {
	t.Helper()
	input := StartInput{Mode: mode}
	previous := ""
	var actor *identity.Auth
	if kind != "anonymous" {
		a, token := startActor(t, f, password, audience, kind == "source")
		actor, previous = &a, token
	}
	if mode == ModeLink {
		reason := "Explicit synthetic callback link"
		input.Password, input.Reason = &password, &reason
	}
	start, e := f.s.Start(f.ctx, f.source.ConnectorID, audience, previous, actor, input, uuid.NewString())
	if e != nil {
		t.Fatal("actual callback start", e)
	}
	return start, previous
}

func callbackClaim(t *testing.T, f sourceRepositoryFixture, start StartResult, audience, previous string) claimedFlow {
	t.Helper()
	authorization, e := url.Parse(start.AuthorizationURL)
	if e != nil {
		t.Fatal("actual callback authorization shape")
	}
	claimed, e := f.s.claim(f.ctx, f.source.ConnectorID, audience, authorization.Query().Get("state"), start.BrowserToken, previous)
	if e != nil {
		t.Fatal("actual single claim", e)
	}
	return claimed
}

func callbackAudits(t *testing.T, f sourceRepositoryFixture) [3]int {
	t.Helper()
	var result [3]int
	for i, action := range []string{"auth.sso_login_succeeded", "sso.binding_linked", "sso.flow_failed"} {
		if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT COUNT(*) FROM audit_events WHERE tenant_id=$1 AND action=$2`, `SELECT COUNT(*) FROM audit_events WHERE tenant_id=? AND action=?`, f.f.Tenant, action)).Scan(&result[i]); e != nil {
			t.Fatal("actual scoped callback audits", e)
		}
	}
	return result
}

func callbackStoredState(t *testing.T, f sourceRepositoryFixture, id string) (string, sql.NullString, sql.NullString) {
	t.Helper()
	var state string
	var user, session sql.NullString
	var erased bool
	if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT state,authenticated_user_id::text,issued_session_id::text,(verifier_key_id IS NULL AND verifier_nonce IS NULL AND verifier_ciphertext IS NULL) FROM sso_flows WHERE tenant_id=$1 AND id=$2`, `SELECT state,authenticated_user_id,issued_session_id,(verifier_key_id IS NULL AND verifier_nonce IS NULL AND verifier_ciphertext IS NULL) FROM sso_flows WHERE tenant_id=? AND id=?`, f.f.Tenant, id)).Scan(&state, &user, &session, &erased); e != nil || !erased {
		t.Fatal("actual consumed verifier/state readback", e)
	}
	return state, user, session
}

func TestActualSSOCallbackSingleClaimAndConsumption(t *testing.T) {
	for _, audience := range []string{"portal", "admin"} {
		for _, mode := range []string{ModeLogin, ModeLink} {
			for _, kind := range []string{"anonymous", "local", "source"} {
				if mode == ModeLink && kind == "anonymous" {
					continue
				}
				t.Run(audience+"_"+mode+"_"+kind, func(t *testing.T) {
					f, password := bindingRepositoryFixture(t)
					start, previous := callbackStart(t, f, password, audience, mode, kind)
					authorization, _ := url.Parse(start.AuthorizationURL)
					state := authorization.Query().Get("state")
					before := callbackAudits(t, f)
					if out, e := f.s.claim(f.ctx, f.source.ConnectorID, audience, state, security.Token(), previous); e == nil || e.Error() != "SSO_BROWSER_CHANGED" || !reflect.DeepEqual(out, claimedFlow{}) {
						t.Fatal("wrong browser consumed flow or leaked credentials", e)
					}
					if out, e := f.s.claim(f.ctx, f.source.ConnectorID, audience, state, start.BrowserToken, security.Token()); e == nil || e.Error() != "SSO_BROWSER_CHANGED" || !reflect.DeepEqual(out, claimedFlow{}) {
						t.Fatal("changed previous browser consumed flow", e)
					}
					claimed := callbackClaim(t, f, start, audience, previous)
					if !validClaimedFlow(claimed) || !security.ValidToken(claimed.verifierText) || !safeOpaque(claimed.clientSecret, maximumSecretBytes) || claimed.connectorState.ID != f.source.ConnectorID {
						t.Fatal("committed claim did not return exact private authority")
					}
					sum := sha256.Sum256([]byte(claimed.verifierText))
					if authorization.Query().Get("code_challenge") != base64.RawURLEncoding.EncodeToString(sum[:]) {
						t.Fatal("consumed verifier differs from actual S256 start")
					}
					sealed := connectorCipher(t, f, f.source.ConnectorID)
					secret, e := f.s.keys.open(secretScope{f.f.Tenant, f.source.ConnectorID, f.source.ConnectorID, clientSecretPurpose}, sealed)
					if e != nil || string(secret) != claimed.clientSecret {
						t.Fatal("claim did not decrypt actual scoped client credential", e)
					}
					stored, user, session := callbackStoredState(t, f, claimed.id)
					if stored != flowClaimed || user.Valid || session.Valid || callbackAudits(t, f) != before {
						t.Fatal("claim fabricated issuance/audit")
					}
					if out, e := f.s.claim(f.ctx, f.source.ConnectorID, audience, state, start.BrowserToken, previous); e == nil || e.Error() != "SSO_FLOW_UNUSABLE" || !reflect.DeepEqual(out, claimedFlow{}) {
						t.Fatal("single-use verifier could be claimed twice", e)
					}
				})
			}
		}
	}
}

// Subjects below are explicit synthetic verified-identity inputs to the private
// persistence boundary. Separate TLS/provider tests qualify the actual exchange.
func TestActualSSOCallbackCompletionOwnershipIssuanceAndRecovery(t *testing.T) {
	for _, scenario := range []string{"login_portal", "login_admin", "login_missing", "login_inactive", "link_new_portal", "link_new_admin", "link_active", "link_reactivate", "link_foreign_inactive", "link_capacity", "link_revision_exhaustion"} {
		t.Run(scenario, func(t *testing.T) {
			f, password := bindingRepositoryFixture(t)
			audience := "portal"
			if strings.HasSuffix(scenario, "admin") {
				audience = "admin"
			}
			mode, kind := ModeLogin, "anonymous"
			subject := "9007199254740993"
			wantRevision := int64(1)
			code := ""
			if strings.HasPrefix(scenario, "link_") {
				mode, kind = ModeLink, "local"
				subject = "9223372036854775807"
			}
			switch scenario {
			case "login_missing":
				subject = "9007199254741003"
				code = "SSO_BINDING_REQUIRED"
			case "login_inactive", "link_reactivate":
				a, _ := startActor(t, f, password, "portal", false)
				if _, e := f.s.Unlink(f.ctx, a, f.source.BindingID, UnlinkBinding{Revision: 1, Password: password, Reason: "Synthetic retained inactive callback binding"}, uuid.NewString(), uuid.NewString()); e != nil {
					t.Fatal(e)
				}
				if scenario == "login_inactive" {
					code = "SSO_BINDING_REQUIRED"
				} else {
					subject = "9007199254740993"
					wantRevision = 3
				}
			case "link_active":
				subject = "9007199254740993"
			case "link_foreign_inactive":
				bindingSeed(t, f, f.f.Tenant, f.source.ConnectorID, bindingOtherUser(t, f), subject, false, time.Now().UTC().Truncate(time.Microsecond))
				code = "SSO_BINDING_CONFLICT"
			case "link_capacity":
				policy := f.accounts.Config.Policy()
				policy.SSO.MaximumBindingsPerUser = 1
				f.accounts.Config.Runtime = &policy
				service, e := New(f.accounts)
				if e != nil {
					t.Fatal(e)
				}
				t.Cleanup(service.Close)
				f.s, f.accounts.Sources = service, service
				code = "SSO_BINDING_LIMIT"
			case "link_revision_exhaustion":
				f.exec(t, f.f.Statement(`INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id,active,revision) VALUES($1,$2,$3,$4,$5,false,$6)`, `INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id,active,revision) VALUES(?,?,?,?,?,false,?)`, uuid.NewString(), f.f.Tenant, f.source.ConnectorID, subject, f.f.User, identity.MaximumAuthVersion))
				code = "SSO_FLOW_UNUSABLE"
			}
			start, previous := callbackStart(t, f, password, audience, mode, kind)
			claimed := callbackClaim(t, f, start, audience, previous)
			before := callbackAudits(t, f)
			issued, e := f.s.commitFlow(f.ctx, claimed, subject, previous, uuid.NewString())
			if code != "" {
				if e == nil || e.Error() != code || issued != (identity.IssuedSession{}) || callbackAudits(t, f) != before {
					t.Fatal("refused completion leaked session/audit", e)
				}
				state, user, session := callbackStoredState(t, f, claimed.id)
				if state != flowClaimed || user.Valid || session.Valid {
					t.Fatal("refused completion changed claimed outcome")
				}
				if e := f.s.failFlow(f.ctx, claimed, code, uuid.NewString()); e != nil {
					t.Fatal("actual failure recovery", e)
				}
				state, user, session = callbackStoredState(t, f, claimed.id)
				after := before
				after[2]++
				if state != "failed" || user.Valid || session.Valid || callbackAudits(t, f) != after {
					t.Fatal("failure/audit did not share acceptance")
				}
				if e := f.s.failFlow(f.ctx, claimed, code, uuid.NewString()); e != nil || callbackAudits(t, f) != after {
					t.Fatal("failure retry duplicated audit", e)
				}
				return
			}
			if e != nil || issued.Token == "" || issued.SessionID == "" || issued.Me.User.ID != f.f.User || issued.Me.Audience != audience {
				t.Fatal("actual atomic sourced issuance", e)
			}
			auth, e := f.accounts.Authenticate(f.ctx, issued.Token, audience)
			if e != nil || auth.SessionID != issued.SessionID {
				t.Fatal("actual issued source authority", e)
			}
			state, user, session := callbackStoredState(t, f, claimed.id)
			if state != "succeeded" || !user.Valid || user.String != f.f.User || !session.Valid || session.String != issued.SessionID {
				t.Fatal("flow/issued session outcome mismatch")
			}
			var binding, owner string
			var active bool
			var revision int64
			if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT id::text,user_id::text,active,revision FROM sso_bindings WHERE tenant_id=$1 AND connector_id=$2 AND subject=$3`, `SELECT id,user_id,active,revision FROM sso_bindings WHERE tenant_id=? AND connector_id=? AND subject=?`, f.f.Tenant, f.source.ConnectorID, subject)).Scan(&binding, &owner, &active, &revision); e != nil || !active || owner != f.f.User || revision != wantRevision {
				t.Fatal("actual created/retained/reactivated binding", e)
			}
			after := before
			after[0]++
			if mode == ModeLink {
				after[1]++
				if _, e := f.accounts.Authenticate(f.ctx, previous, audience); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
					t.Fatal("previous link session must rotate", e)
				}
			}
			if callbackAudits(t, f) != after {
				t.Fatal("issuance/link audit atomicity")
			}
			if out, e := f.s.commitFlow(f.ctx, claimed, subject, previous, uuid.NewString()); e == nil || out != (identity.IssuedSession{}) || callbackAudits(t, f) != after {
				t.Fatal("claimed completion replayed issuance", e)
			}
			if e := f.s.failFlow(f.ctx, claimed, "SSO_PROVIDER_REJECTED", uuid.NewString()); e != nil || callbackAudits(t, f) != after {
				t.Fatal("late failure rewrote succeeded outcome", e)
			}
			if _, e := f.accounts.Authenticate(f.ctx, issued.Token, audience); e != nil {
				t.Fatal("late cleanup revoked actual success", e)
			}
		})
	}
}
