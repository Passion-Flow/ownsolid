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

func TestActualSourcedWorkspaceListsRepository(t *testing.T) {
	f := newSourceRepositoryFixture(t)
	f.accounts.Sources = f.s
	var issued identity.IssuedSession
	if e := f.f.Store.Within(f.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = f.accounts.IssueSessionWithin(ctx, q, f.f.User, 1, "admin", "", uuid.NewString(), &f.source)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	a, e := f.accounts.Authenticate(f.ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	s := &workspaces.Service{Identity: f.accounts, DB: f.f.PostgreSQL}
	if p, e := s.List(f.ctx, a, nil); e != nil || len(p.Items) != 1 {
		t.Fatal("valid source list", e)
	}
	f.exec(t, f.f.Statement("UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE id=$1", "UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE id=?", f.source.ConnectorID))
	if p, e := s.List(f.ctx, a, nil); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" || len(p.Items) > 0 || p.NextCursor != nil {
		t.Fatal("revoked source returned discovery", e)
	}
}
