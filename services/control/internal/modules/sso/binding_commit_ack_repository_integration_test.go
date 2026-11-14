//go:build repository_integration

package sso

import (
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
)

func TestActualSSOBindingCommitAcknowledgementLoss(t *testing.T) {
	for _, fault := range []struct {
		name      string
		mode      commitfault.Mode
		committed bool
	}{
		{"normal_acknowledged_commit", commitfault.Pass, true},
		{"commit_not_forwarded", commitfault.DropBeforeCommit, false},
		{"actual_server_ACK_suppressed", commitfault.DropCommitAcknowledgement, true},
	} {
		t.Run(fault.name, func(t *testing.T) {
			f, password := bindingRepositoryFixture(t)
			local := catalogAuth(t, f, f.f.User, "admin", nil)
			sourced := catalogAuth(t, f, f.f.User, "admin", &f.source)
			pending := bindingLinkFlow(t, f, local, "pending")
			input, key := UnlinkBinding{Revision: 1, Password: password, Reason: "Synthetic actual unlink wire fault"}, uuid.NewString()
			store, proxy := connectorFaultStore(t, f)
			originalStore, originalDB := f.accounts.Storage, f.accounts.DB
			defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
			f.accounts.Storage, f.accounts.DB = store, nil
			if fault.mode != commitfault.Pass {
				if e := proxy.Arm(fault.mode); e != nil {
					t.Fatal(e)
				}
			}
			out, e := f.s.Unlink(f.ctx, local, f.source.BindingID, input, key, uuid.NewString())
			f.accounts.Storage, f.accounts.DB = originalStore, originalDB
			if fault.mode == commitfault.Pass {
				if e != nil || out.Active || out.Revision != 2 {
					t.Fatal("actual acknowledged unlink", e)
				}
			} else {
				if e == nil || e.Error() != string(transaction.CommitUnknown) || out != (Binding{}) {
					t.Fatal("lost unlink ACK must return zero unknown result", e)
				}
				select {
				case observed := <-proxy.Outcomes():
					if observed.Mode != fault.mode || !observed.Suppressed || observed.Forwarded != fault.committed || observed.ServerOK != fault.committed {
						t.Fatal("actual unlink wire observation mismatch")
					}
				case <-f.ctx.Done():
					t.Fatal("actual wire observation absent")
				}
			}
			want := [3]int{1, 0, 0}
			version := int64(1)
			if fault.committed {
				want = [3]int{1, 1, 1}
				version = 2
			}
			stored, e := selectBindingWithin(f.ctx, f.f.Store, f.f.Tenant, f.f.User, f.source.BindingID)
			if e != nil || stored.Active == fault.committed || stored.Revision != version || bindingCounts(t, f, stored.ID, key) != want || proxy.Commits() != 1 {
				t.Fatal("independent unlink/intent/audit atomic outcome or automatic resend", e)
			}
			var state string
			var erased bool
			if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT state,verifier_ciphertext IS NULL FROM sso_flows WHERE id=$1`, `SELECT state,verifier_ciphertext IS NULL FROM sso_flows WHERE id=?`, pending)).Scan(&state, &erased); e != nil || fault.committed && (state != "failed" || !erased) || !fault.committed && (state != "pending" || erased) {
				t.Fatal("actual unlink verifier atomicity", e)
			}
			assertConnectorAuth(t, f, sourced, !fault.committed)
			// An explicit identical retry must still verify the current password.
			wrong := input
			wrong.Password = "Synthetic invalid confirmation"
			if value, e := f.s.Unlink(f.ctx, local, stored.ID, wrong, key, uuid.NewString()); e == nil || e.Error() != "SSO_PASSWORD_REQUIRED" || value != (Binding{}) || bindingCounts(t, f, stored.ID, key) != want {
				t.Fatal("uncertain recovery bypassed password", e)
			}
			recovered, e := f.s.Unlink(f.ctx, local, stored.ID, input, key, uuid.NewString())
			if e != nil || recovered.Active || recovered.Revision != 2 || bindingCounts(t, f, stored.ID, key) != ([3]int{1, 1, 1}) || fault.committed && !sameBinding(stored, recovered) {
				t.Fatal("explicit identical unlink recovery advanced twice", e)
			}
			assertConnectorAuth(t, f, local, true)
			assertConnectorAuth(t, f, sourced, false)
			if value, e := f.s.Unlink(f.ctx, sourced, stored.ID, input, key, uuid.NewString()); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" || value != (Binding{}) {
				t.Fatal("accepted intent bypassed revoked source", e)
			}
			t.Logf("actual engine=%s fault_mode=%d initial_commit_commands=1 atomic_binding_intent_audit=1 explicit_retry=1", f.f.Store.Dialect(), fault.mode)
		})
	}
}
