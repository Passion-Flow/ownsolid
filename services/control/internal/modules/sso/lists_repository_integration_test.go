//go:build repository_integration

package sso

import (
	"context"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestActualSourcedAdministrativeListsRepository(t *testing.T) {
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
	query := url.Values{"email_prefix": {"synthetic+" + f.f.User + "@"}}
	out, e := f.accounts.ListForActor(f.ctx, a, "users", query)
	if e != nil || len(out.Items) != 1 || out.Items[0]["id"] != f.f.User {
		t.Fatal("actual sourced Admin list", e)
	}
	f.exec(t, f.f.Statement("UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE id=$1", "UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE id=?", f.source.ConnectorID))
	out, e = f.accounts.ListForActor(f.ctx, a, "users", query)
	if e == nil || e.Error() != "AUTH_SESSION_EXPIRED" || len(out.Items) != 0 || out.NextCursor != nil {
		t.Fatal("revoked source exposed Admin page", e)
	}
}
