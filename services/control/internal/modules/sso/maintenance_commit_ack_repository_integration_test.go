//go:build repository_integration

package sso

import (
	"context"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
)

func TestActualSSOMaintenanceCommitAcknowledgementLoss(t *testing.T) {
	for _, operation := range []string{"cleanup", "restore_invalidation"} {
		t.Run(operation, func(t *testing.T) {
			for _, fault := range []struct {
				name      string
				mode      commitfault.Mode
				committed bool
			}{
				{"normal_ACK", commitfault.Pass, true}, {"commit_not_forwarded", commitfault.DropBeforeCommit, false}, {"actual_server_ACK_suppressed", commitfault.DropCommitAcknowledgement, true},
			} {
				t.Run(fault.name, func(t *testing.T) {
					f := maintenanceFixture(t, 2, 3)
					now := maintenanceNow(t, f)
					pending := maintenanceFlow(t, f, flowPending, now.Add(-2*time.Hour), now.Add(-time.Hour), time.Time{})
					terminal := maintenanceFlow(t, f, "failed", now.Add(-12*maintenanceRetentionDay), now.Add(-11*maintenanceRetentionDay), now.Add(-10*maintenanceRetentionDay))
					claimed := ""
					if operation == "restore_invalidation" {
						claimed = maintenanceFlow(t, f, flowClaimed, now, now.Add(time.Hour), time.Time{})
					}
					store, proxy := connectorFaultStore(t, f)
					originalStore, originalDB := f.accounts.Storage, f.accounts.DB
					f.accounts.Storage, f.accounts.DB = store, nil
					defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
					if fault.mode != commitfault.Pass {
						if e := proxy.Arm(fault.mode); e != nil {
							t.Fatal(e)
						}
					}
					var e error
					if operation == "cleanup" {
						e = f.s.Cleanup(f.ctx)
					} else {
						e = store.Within(f.ctx, false, func(ctx context.Context, q transaction.Executor) error { return InvalidateAuthoritiesWithin(ctx, q) })
					}
					f.accounts.Storage, f.accounts.DB = originalStore, originalDB
					if fault.mode == commitfault.Pass {
						if e != nil {
							t.Fatal("actual acknowledged maintenance commit", e)
						}
					} else {
						if e == nil || e.Error() != string(transaction.CommitUnknown) {
							t.Fatal("maintenance hid actual uncertain commit", e)
						}
						select {
						case observed := <-proxy.Outcomes():
							if observed.Mode != fault.mode || !observed.Suppressed || observed.Forwarded != fault.committed || observed.ServerOK != fault.committed {
								t.Fatal("actual maintenance COMMIT forwarding outcome")
							}
						case <-f.ctx.Done():
							t.Fatal("maintenance wire observation deadline")
						}
					}
					if proxy.Commits() != 1 {
						t.Fatal("maintenance silently resent uncertain transaction")
					}
					wantState, wantCode := flowPending, ""
					wantAudits := 0
					if fault.committed {
						if operation == "cleanup" {
							wantState, wantCode, wantAudits = "expired", "SSO_FLOW_EXPIRED", 1
						} else {
							wantState, wantCode = "failed", "SSO_FLOW_UNUSABLE"
						}
					}
					state, erased, code := maintenanceState(t, f, pending)
					if state != wantState || erased != fault.committed || code != wantCode || maintenanceAuditCount(t, f) != wantAudits {
						t.Fatal("independent maintenance verifier/state/audit atomicity mismatch")
					}
					wantTerminal := "failed"
					if operation == "cleanup" && fault.committed {
						wantTerminal = "absent"
					}
					maintenanceAssert(t, f, terminal, wantTerminal)
					if claimed != "" {
						want := flowClaimed
						if fault.committed {
							want = "failed"
						}
						maintenanceAssert(t, f, claimed, want)
					}
					// A deliberate later maintenance cycle is state-based recovery. It
					// cannot exchange provider codes,issue sessions or duplicate an audit.
					if operation == "cleanup" {
						if e := f.s.Cleanup(f.ctx); e != nil {
							t.Fatal("explicit later cleanup recovery", e)
						}
						maintenanceAssert(t, f, pending, "expired")
						maintenanceAssert(t, f, terminal, "absent")
						if maintenanceAuditCount(t, f) != 1 {
							t.Fatal("maintenance recovery lost/duplicated audit")
						}
					} else {
						if e := f.f.Store.Within(f.ctx, false, func(ctx context.Context, q transaction.Executor) error { return InvalidateAuthoritiesWithin(ctx, q) }); e != nil {
							t.Fatal("explicit later restore invalidation", e)
						}
						for _, id := range []string{pending, claimed} {
							state, erased, code := maintenanceState(t, f, id)
							if state != "failed" || !erased || code != "SSO_FLOW_UNUSABLE" {
								t.Fatal("restored authority revived after uncertain commit")
							}
						}
						if maintenanceAuditCount(t, f) != 0 {
							t.Fatal("borrowed invalidation gained independent audit")
						}
					}
					t.Logf("actual engine=%s operation=%s fault_mode=%d initial_COMMIT_commands=1 independent_atomic_outcome=1 explicit_state_recovery=1", f.f.Store.Dialect(), operation, fault.mode)
				})
			}
		})
	}
}
