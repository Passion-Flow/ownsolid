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

func TestActualSourcedWorkspaceOperationReceipt(t *testing.T) {
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
	s := &workspaces.Service{Identity: f.accounts}
	request := uuid.NewString()
	w, e := s.Create(f.ctx, a, "Sourced receipt", f.f.User, request)
	if e != nil {
		t.Fatal(e)
	}
	out, e := s.Operation(f.ctx, a, request)
	if e != nil || out.State != workspaces.OperationCommitted || out.Event == nil || out.Event.WorkspaceID != w.ID {
		t.Fatal(out, e)
	}
	f.exec(t, f.f.Statement("UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE id=$1", "UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE id=?", f.source.ConnectorID))
	out, e = s.Operation(f.ctx, a, request)
	if e == nil || e.Error() != "AUTH_SESSION_EXPIRED" || out != (workspaces.OperationReceipt{}) {
		t.Fatal("revoked receipt source", out, e)
	}
}
