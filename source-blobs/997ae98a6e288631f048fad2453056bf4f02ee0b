//go:build repository_integration

package workspaces_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

func TestActualWorkspaceMySQLCommitAcknowledgementLoss(t *testing.T) {
	if os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE") != "mysql" {
		t.Fatal("MYSQL_ACK_LOSS_EXPLICIT_ISOLATED_ENGINE_REQUIRED")
	}
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	directAccounts := &identity.Service{Storage: f.Store}
	var issued identity.IssuedSession
	if e := f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = directAccounts.IssueSessionWithin(ctx, q, f.User, 1, "admin", "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	a, e := directAccounts.Authenticate(ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	direct := &workspaces.Service{Identity: directAccounts}
	c, e := mysqlstore.Load(os.Getenv("PROJECT_ONE_REPOSITORY_MYSQL_CONFIGURATION"), "")
	if e != nil {
		t.Fatal("actual private MySQL configuration")
	}
	for _, test := range []struct {
		name      string
		mode      commitfault.Mode
		committed bool
	}{
		{"normal_acknowledged_commit_control", commitfault.Pass, true},
		{"commit_never_forwarded_control", commitfault.DropBeforeCommit, false},
		{"actual_server_OK_suppressed_after_commit", commitfault.DropCommitAcknowledgement, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			proxy, e := commitfault.NewMySQL(c)
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if e := proxy.Close(ctx); e != nil {
					t.Error(e)
				}
			})
			proxied := c
			proxied.Port = proxy.Port()
			proxied.Policy.MaximumOpenConnections = 2
			proxied.Policy.MaximumIdleConnections = 0
			kernel, observation, e := mysqlstore.Open(ctx, proxied)
			if e != nil {
				t.Fatal("actual TLS forwarding connector", e)
			}
			t.Cleanup(func() {
				if e := kernel.Close(); e != nil {
					t.Error("actual forwarded pool close")
				}
			})
			if !observation.TLSAuthenticated {
				t.Fatal("actual TLS required")
			}
			store, e := transaction.FromMySQL(kernel)
			if e != nil {
				t.Fatal(e)
			}
			s := &workspaces.Service{Identity: &identity.Service{Storage: store}}
			request, name := uuid.NewString(), "Network receipt "+uuid.NewString()
			if test.mode != commitfault.Pass {
				if e = proxy.Arm(test.mode); e != nil {
					t.Fatal(e)
				}
			}
			out, e := s.Create(ctx, a, name, f.User, request)
			if test.mode == commitfault.Pass {
				if e != nil || out.ID == "" {
					t.Fatal("normal acknowledgement", e)
				}
			} else {
				if e == nil || e.Error() != string(transaction.CommitUnknown) || out != (workspaces.Workspace{}) {
					t.Fatal("lost acknowledgement must remain unknown without partial Workspace", e)
				}
				select {
				case observed := <-proxy.Outcomes():
					if observed.Mode != test.mode || !observed.Suppressed || observed.Forwarded != test.committed || observed.ServerOK != test.committed {
						t.Fatal("actual wire observation did not prove selected fault", observed)
					}
				case <-ctx.Done():
					t.Fatal("actual wire fault observation missing")
				}
			}
			for range 2 {
				// Independent direct connections bypass the fault transport. Two
				// explicit receipt checks must never resend the original mutation.
				receipt, e := direct.Operation(ctx, a, request)
				if e != nil {
					t.Fatal(e)
				}
				if test.committed {
					if receipt.State != workspaces.OperationCommitted || receipt.Event == nil || receipt.Event.Action != "workspace.created" {
						t.Fatal("durable commit receipt missing", receipt)
					}
					w, e := direct.Get(ctx, a, receipt.Event.WorkspaceID, false)
					if e != nil || w.Name != name {
						t.Fatal("durable domain readback", e)
					}
				} else if receipt.State != workspaces.OperationUnconfirmed || receipt.Event != nil {
					t.Fatal("absence falsely established rollback or success", receipt)
				}
			}
			var rows, audits int
			if e = f.Store.QueryRow(ctx, f.Statement("SELECT count(*) FROM workspaces WHERE tenant_id=$1 AND name=$2", "SELECT count(*) FROM workspaces WHERE tenant_id=? AND name=?", f.Tenant, name)).Scan(&rows); e != nil {
				t.Fatal(e)
			}
			if e = f.Store.QueryRow(ctx, f.Statement("SELECT count(*) FROM audit_events WHERE request_id=$1", "SELECT count(*) FROM audit_events WHERE request_id=?", request)).Scan(&audits); e != nil {
				t.Fatal(e)
			}
			want := 0
			if test.committed {
				want = 1
			}
			if rows != want || audits != want || proxy.Commits() != 1 {
				t.Fatal("duplicate,missing,non-atomic write or automatic commit retry", rows, audits, proxy.Commits())
			}
			t.Logf("actual TLS wire control mode=%d commit_commands=1 domain_rows=%d atomic_audits=%d explicit_receipt_checks=2", test.mode, rows, audits)
		})
	}
}
