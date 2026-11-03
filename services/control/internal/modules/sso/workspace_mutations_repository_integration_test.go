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

func TestActualSourcedWorkspaceMutationsRepository(t *testing.T) {
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
	w, e := s.Create(f.ctx, a, "Actual sourced mutation", f.f.User, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	n, e := s.CreateNote(f.ctx, a, w.ID, "Actual sourced note", "Synthetic", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	f.exec(t, f.f.Statement("UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE id=$1", "UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE id=?", f.source.ConnectorID))
	tests := []func() error{
		func() error {
			v, e := s.Create(f.ctx, a, "Revoked source", f.f.User, uuid.NewString())
			if v.ID != "" {
				t.Fatal("partial Workspace")
			}
			return e
		},
		func() error {
			v, e := s.Update(f.ctx, a, w.ID, w.Name, "active", w.Version, "Revoked source", uuid.NewString())
			if v.ID != "" {
				t.Fatal("partial Workspace update")
			}
			return e
		},
		func() error {
			v, e := s.AddMember(f.ctx, a, w.ID, a.Me.User.Email, "member", uuid.NewString())
			if v.UserID != "" {
				t.Fatal("partial member")
			}
			return e
		},
		func() error {
			v, e := s.ChangeMember(f.ctx, a, w.ID, f.f.User, "member", 1, "Revoked source", uuid.NewString(), false)
			if v.UserID != "" {
				t.Fatal("partial member update")
			}
			return e
		},
		func() error {
			v, e := s.CreateNote(f.ctx, a, w.ID, "Revoked source", "Synthetic", uuid.NewString())
			if v.ID != "" {
				t.Fatal("partial note")
			}
			return e
		},
		func() error {
			v, e := s.ChangeNote(f.ctx, a, w.ID, n.ID, "Revoked source", "Synthetic", n.Version, uuid.NewString(), false)
			if v.ID != "" {
				t.Fatal("partial note update")
			}
			return e
		},
	}
	var before int
	if e = f.f.Store.QueryRow(f.ctx, f.f.Statement("SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events")).Scan(&before); e != nil {
		t.Fatal(e)
	}
	for _, run := range tests {
		if e = run(); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
			t.Fatal("source mutation fence", e)
		}
	}
	var after int
	if e = f.f.Store.QueryRow(f.ctx, f.f.Statement("SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events")).Scan(&after); e != nil || after != before {
		t.Fatal("revoked mutations audited", e)
	}
}
