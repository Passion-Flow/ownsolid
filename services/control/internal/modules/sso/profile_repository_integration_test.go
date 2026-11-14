//go:build repository_integration

package sso

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestActualSourcedProfileAndStatusRepository(t *testing.T) {
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
	me, e := f.accounts.UpdateMe(f.ctx, a, "Sourced profile \u4e16\u754c", "en", 1, uuid.NewString())
	if e != nil || me.User.DisplayName != "Sourced profile \u4e16\u754c" {
		t.Fatal("actual source refused valid profile", e)
	}
	target, address := uuid.NewString(), "synthetic+"+uuid.NewString()+"@example.invalid"
	f.exec(t, f.f.Statement("INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$3,'Synthetic target','en','active',false)", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Synthetic target','en','active',false)", target, address, address))
	u, e := f.accounts.SetStatus(f.ctx, a, target, "disabled", 1, "Synthetic sourced admin", uuid.NewString())
	if e != nil || u.Status != "disabled" {
		t.Fatal("actual source refused valid account status", e)
	}
	f.exec(t, f.f.Statement("UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE id=$1", "UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE id=?", f.source.ConnectorID))
	if _, e = f.accounts.UpdateMe(f.ctx, a, "Revoked source profile", "en", me.User.Version, uuid.NewString()); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
		t.Fatal("revoked source changed profile", e)
	}
	if _, e = f.accounts.SetStatus(f.ctx, a, target, "active", u.Version, "Synthetic revoked sourced admin", uuid.NewString()); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
		t.Fatal("revoked source changed account status", e)
	}
	if current, e := f.accounts.GetUser(f.ctx, target); e != nil || current.Status != "disabled" || current.Version != u.Version {
		t.Fatal("revoked source leaked account mutation", e)
	}
}
