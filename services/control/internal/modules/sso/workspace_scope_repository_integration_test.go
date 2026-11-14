//go:build repository_integration

package sso

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestActualSourcedWorkspaceScopeRepository(t *testing.T) {
	f := newSourceRepositoryFixture(t)
	f.accounts.Sources = f.s
	var issued identity.IssuedSession
	if e := f.f.Store.Within(f.ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		var e error
		issued, e = f.accounts.IssueSessionWithin(ctx, tx, f.f.User, 1, "admin", "", uuid.NewString(), &f.source)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	a, e := f.accounts.Authenticate(f.ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	s := &workspaces.Service{Identity: f.accounts, DB: f.f.PostgreSQL}
	if w, e := s.Get(f.ctx, a, f.f.Workspace, false); e != nil || w.ID != f.f.Workspace {
		t.Fatal("actual source refused Workspace", e)
	}
	f.exec(t, f.f.Statement("UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE id=$1", "UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE id=?", f.source.ConnectorID))
	if w, e := s.Get(f.ctx, a, f.f.Workspace, false); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" || w.ID != "" {
		t.Fatal("revoked source exposed Workspace", e)
	}
}
