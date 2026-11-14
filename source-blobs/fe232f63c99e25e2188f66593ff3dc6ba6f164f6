//go:build repository_integration

package sso

import (
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
)

func TestActualSSOStartCommitAcknowledgementLoss(t *testing.T) {
	for _, mode := range []string{ModeLogin, ModeLink} {
		t.Run(mode, func(t *testing.T) {
			for _, fault := range []struct {
				name      string
				mode      commitfault.Mode
				committed bool
			}{{"normal_acknowledged_commit", commitfault.Pass, true}, {"commit_not_forwarded", commitfault.DropBeforeCommit, false}, {"actual_server_ACK_suppressed", commitfault.DropCommitAcknowledgement, true}} {
				t.Run(fault.name, func(t *testing.T) {
					f, password := bindingRepositoryFixture(t)
					var actor *identity.Auth
					previous := ""
					input := StartInput{Mode: mode}
					if mode == ModeLink {
						a, token := startActor(t, f, password, "portal", false)
						actor, previous = &a, token
						reason := "Synthetic actual link start wire control"
						input.Password, input.Reason = &password, &reason
					}
					before := startCounts(t, f)
					store, proxy := connectorFaultStore(t, f)
					originalStore, originalDB := f.accounts.Storage, f.accounts.DB
					defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
					f.accounts.Storage, f.accounts.DB = store, nil
					if fault.mode != commitfault.Pass {
						if e := proxy.Arm(fault.mode); e != nil {
							t.Fatal(e)
						}
					}
					out, e := f.s.Start(f.ctx, f.source.ConnectorID, "portal", previous, actor, input, uuid.NewString())
					f.accounts.Storage, f.accounts.DB = originalStore, originalDB
					if fault.mode == commitfault.Pass {
						if e != nil || out.BrowserToken == "" {
							t.Fatal("actual acknowledged start", e)
						}
					} else {
						if e == nil || e.Error() != string(transaction.CommitUnknown) || out != (StartResult{}) {
							t.Fatal("uncertain start must return no browser token or URL", e)
						}
						select {
						case observed := <-proxy.Outcomes():
							if observed.Mode != fault.mode || !observed.Suppressed || observed.Forwarded != fault.committed || observed.ServerOK != fault.committed {
								t.Fatal("actual start COMMIT wire observation")
							}
						case <-f.ctx.Done():
							t.Fatal("actual start wire observation absent")
						}
					}
					want := before
					if fault.committed {
						want[0]++
						want[1]++
					}
					if startCounts(t, f) != want || proxy.Commits() != 1 {
						t.Fatal("start flow/audit atomic outcome or implicit resend")
					}
					// Start has no replay contract. An explicit new user attempt creates
					// another independently bound pending flow and consumes another slot.
					next, e := f.s.Start(f.ctx, f.source.ConnectorID, "portal", previous, actor, input, uuid.NewString())
					want[0]++
					want[1]++
					if e != nil || next.BrowserToken == "" || startCounts(t, f) != want || fault.mode == commitfault.Pass && (next.BrowserToken == out.BrowserToken || next.AuthorizationURL == out.AuthorizationURL) {
						t.Fatal("explicit new start did not use independent flow/admission", e)
					}
					t.Logf("actual engine=%s mode=%s fault_mode=%d initial_commit_commands=1 atomic_flow_audit=1 explicit_new_start=1", f.f.Store.Dialect(), mode, fault.mode)
				})
			}
		})
	}
}
