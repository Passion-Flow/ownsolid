//go:build repository_integration

package sso

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestActualSharedSourcedSessionRepository(t *testing.T) {
	f := newSourceRepositoryFixture(t)
	f.accounts.Sources = f.s
	issue := func(request string, source *identity.SessionSource) (identity.IssuedSession, error) {
		var issued identity.IssuedSession
		e := f.f.Store.Within(f.ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			var e error
			issued, e = f.accounts.IssueSessionWithin(ctx, tx, f.f.User, 1, "admin", "", request, source)
			return e
		})
		return issued, e
	}
	t.Run("audit_failure_rolls_back_actual_one_time_source_claim", func(t *testing.T) {
		issued, e := issue("invalid-request-id", &f.source)
		if e == nil || issued.Token != "" {
			t.Fatal("sourced audit failure returned success/credential", e)
		}
		var claimed bool
		var count int
		if e = f.f.Store.QueryRow(f.ctx, f.f.Statement("SELECT issued_session_id IS NOT NULL FROM sso_flows WHERE id=$1", "SELECT issued_session_id IS NOT NULL FROM sso_flows WHERE id=?", f.source.FlowID)).Scan(&claimed); e != nil || claimed {
			t.Fatal("failed audit retained flow claim", e)
		}
		if e = f.f.Store.QueryRow(f.ctx, f.f.Statement("SELECT count(*) FROM sessions WHERE user_id=$1", "SELECT count(*) FROM sessions WHERE user_id=?", f.f.User)).Scan(&count); e != nil || count != 0 {
			t.Fatal("failed audit retained sourced session", e)
		}
	})
	issued, e := issue(uuid.NewString(), &f.source)
	if e != nil {
		t.Fatal(e)
	}
	a, e := f.accounts.Authenticate(f.ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	t.Run("real_source_issuance_and_single_use", func(t *testing.T) {
		if a.SessionID != issued.SessionID || a.Me.User.ID != f.f.User {
			t.Fatal("source issued wrong ordinary actor")
		}
		if _, e := issue(uuid.NewString(), &f.source); e == nil {
			t.Fatal("source flow issued twice")
		}
	})
	t.Run("ordinary_actor_recheck_holds_current_source_fence", func(t *testing.T) {
		result := make(chan error, 1)
		e := f.f.Store.Within(f.ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			if e := f.accounts.RecheckWithin(ctx, tx, a); e != nil {
				return e
			}
			go func() {
				_, e := f.f.Store.Exec(f.ctx, f.f.Statement("UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE id=$1", "UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE id=?", f.source.ConnectorID))
				result <- e
			}()
			select {
			case e := <-result:
				t.Fatal("revocation escaped sourced actor fence", e)
			case <-time.After(150 * time.Millisecond):
			}
			return audit.WriteWithin(ctx, tx, f.f.User, f.f.User, "admin", "auth.synthetic_sourced_write", "success", "user", f.f.User, "", uuid.NewString())
		})
		if e != nil {
			t.Fatal(e)
		}
		if e = <-result; e != nil {
			t.Fatal(e)
		}
		if _, e = f.accounts.Authenticate(f.ctx, issued.Token, "admin"); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
			t.Fatal("source revocation still authenticated", e)
		}
		e = f.f.Store.Within(f.ctx, false, func(ctx context.Context, tx transaction.Executor) error { return f.accounts.RecheckWithin(ctx, tx, a) })
		if e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
			t.Fatal("source revocation still authorized business write", e)
		}
		local, e := issue(uuid.NewString(), nil)
		if e != nil {
			t.Fatal("ordinary operator recovery failed", e)
		}
		if _, e = f.accounts.Authenticate(f.ctx, local.Token, "admin"); e != nil {
			t.Fatal("ordinary recovery inherited revoked provider", e)
		}
	})
}
