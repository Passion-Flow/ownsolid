//go:build repository_integration

package sso

import (
	"database/sql"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
)

func TestActualSSOCallbackCommitAcknowledgementLoss(t *testing.T) {
	for _, operation := range []string{"claim_login", "claim_link", "completion_login", "completion_link", "failure_link"} {
		t.Run(operation, func(t *testing.T) {
			for _, fault := range []struct {
				name      string
				mode      commitfault.Mode
				committed bool
			}{{"normal_acknowledged_commit", commitfault.Pass, true}, {"commit_not_forwarded", commitfault.DropBeforeCommit, false}, {"actual_server_ACK_suppressed", commitfault.DropCommitAcknowledgement, true}} {
				t.Run(fault.name, func(t *testing.T) {
					f, password := bindingRepositoryFixture(t)
					mode, kind := ModeLogin, "anonymous"
					if strings.HasSuffix(operation, "link") {
						mode, kind = ModeLink, "local"
					}
					start, previous := callbackStart(t, f, password, "portal", mode, kind)
					authorization, _ := url.Parse(start.AuthorizationURL)
					var claimed, claimResult claimedFlow
					var issued identity.IssuedSession
					var id string
					isClaim := strings.HasPrefix(operation, "claim_")
					isCompletion := strings.HasPrefix(operation, "completion_")
					if isClaim {
						if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT id::text FROM sso_flows WHERE tenant_id=$1 AND state_hash=$2`, `SELECT id FROM sso_flows WHERE tenant_id=? AND state_hash=?`, f.f.Tenant, security.Digest(authorization.Query().Get("state")))).Scan(&id); e != nil {
							t.Fatal(e)
						}
					} else {
						claimed = callbackClaim(t, f, start, "portal", previous)
						id = claimed.id
					}
					subject := "9007199254740993"
					if mode == ModeLink {
						subject = "9007199254741003"
					}
					before := callbackAudits(t, f)
					store, proxy := connectorFaultStore(t, f)
					originalStore, originalDB := f.accounts.Storage, f.accounts.DB
					defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
					f.accounts.Storage, f.accounts.DB = store, nil
					if fault.mode != commitfault.Pass {
						if e := proxy.Arm(fault.mode); e != nil {
							t.Fatal(e)
						}
					}
					var e error
					if isClaim {
						claimResult, e = f.s.claim(f.ctx, f.source.ConnectorID, "portal", authorization.Query().Get("state"), start.BrowserToken, previous)
					} else if isCompletion {
						issued, e = f.s.commitFlow(f.ctx, claimed, subject, previous, uuid.NewString())
					} else {
						e = f.s.failFlow(f.ctx, claimed, "SSO_PROVIDER_REJECTED", uuid.NewString())
					}
					f.accounts.Storage, f.accounts.DB = originalStore, originalDB
					if fault.mode == commitfault.Pass {
						if e != nil || isClaim && !validClaimedFlow(claimResult) || isCompletion && issued.Token == "" {
							t.Fatal("actual acknowledged callback transaction", e)
						}
					} else {
						if e == nil || e.Error() != string(transaction.CommitUnknown) || !reflect.DeepEqual(claimResult, claimedFlow{}) || issued != (identity.IssuedSession{}) {
							t.Fatal("uncertain callback leaked credentials/session or hid outcome", e)
						}
						select {
						case observed := <-proxy.Outcomes():
							if observed.Mode != fault.mode || !observed.Suppressed || observed.Forwarded != fault.committed || observed.ServerOK != fault.committed {
								t.Fatal("actual callback COMMIT wire observation mismatch")
							}
						case <-f.ctx.Done():
							t.Fatal("actual callback wire observation absent")
						}
					}
					if proxy.Commits() != 1 {
						t.Fatal("callback silently resent an uncertain transaction")
					}
					var state string
					var erased bool
					var user, session sql.NullString
					if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT state,(verifier_key_id IS NULL AND verifier_nonce IS NULL AND verifier_ciphertext IS NULL),authenticated_user_id::text,issued_session_id::text FROM sso_flows WHERE tenant_id=$1 AND id=$2`, `SELECT state,(verifier_key_id IS NULL AND verifier_nonce IS NULL AND verifier_ciphertext IS NULL),authenticated_user_id,issued_session_id FROM sso_flows WHERE tenant_id=? AND id=?`, f.f.Tenant, id)).Scan(&state, &erased, &user, &session); e != nil {
						t.Fatal("independent callback wire outcome", e)
					}
					wantState := flowClaimed
					wantErased := true
					wantAudits := before
					if isClaim {
						wantState = flowPending
						wantErased = fault.committed
						if fault.committed {
							wantState = flowClaimed
						}
					} else if fault.committed {
						if isCompletion {
							wantState = "succeeded"
							wantAudits[0]++
							if mode == ModeLink {
								wantAudits[1]++
							}
						} else {
							wantState = "failed"
							wantAudits[2]++
						}
					}
					wantSession := isCompletion && fault.committed
					if state != wantState || erased != wantErased || user.Valid != wantSession || session.Valid != wantSession || callbackAudits(t, f) != wantAudits {
						t.Fatal("flow/verifier/issued source/audit did not share actual outcome")
					}
					var sessions int
					if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT COUNT(*) FROM sessions WHERE sso_tenant_id=$1 AND sso_flow_id=$2`, `SELECT COUNT(*) FROM sessions WHERE sso_tenant_id=? AND sso_flow_id=?`, f.f.Tenant, id)).Scan(&sessions); e != nil || sessions != boolCount(wantSession) {
						t.Fatal("source session escaped callback commit boundary", e)
					}
					if mode == ModeLink {
						_, e := f.accounts.Authenticate(f.ctx, previous, "portal")
						if wantSession {
							if e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
								t.Fatal("previous session rotation missing from committed completion", e)
							}
						} else if e != nil {
							t.Fatal("uncommitted completion rotated current local session", e)
						}
					}
					if strings.HasPrefix(operation, "failure_") {
						// Explicit internal CAS recovery cannot repeat provider exchange or
						// rewrite a succeeded flow. It records one failure audit at most.
						if e := f.s.failFlow(f.ctx, claimed, "SSO_PROVIDER_REJECTED", uuid.NewString()); e != nil {
							t.Fatal("explicit failure CAS recovery", e)
						}
						final := before
						final[2]++
						if callbackAudits(t, f) != final {
							t.Fatal("failure recovery duplicated its audit")
						}
					} else {
						// Subjects are explicit synthetic verified-identity inputs. Recovery
						// uses a new real Start/claim;it never retries token exchange/issuance
						// for the old uncertain callback or resurrects an erased verifier.
						fresh, token := callbackStart(t, f, password, "portal", mode, kind)
						next := callbackClaim(t, f, fresh, "portal", token)
						if next.id == id || fresh.BrowserToken == start.BrowserToken {
							t.Fatal("explicit recovery reused uncertain callback authority")
						}
						if isCompletion {
							out, e := f.s.commitFlow(f.ctx, next, subject, token, uuid.NewString())
							if e != nil || out.Token == "" {
								t.Fatal("explicit new-flow completion recovery", e)
							}
							if _, e := f.accounts.Authenticate(f.ctx, out.Token, "portal"); e != nil {
								t.Fatal("recovered sourced session invalid", e)
							}
						}
					}
					t.Logf("actual engine=%s operation=%s fault_mode=%d initial_commit_commands=1 atomic_outcome=1 explicit_recovery=1", f.f.Store.Dialect(), operation, fault.mode)
				})
			}
		})
	}
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}
