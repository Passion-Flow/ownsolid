//go:build repository_integration

package sso

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestActualSourcedInvitationRepository(t *testing.T) {
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
	seed := func() string {
		t.Helper()
		id := uuid.NewString()
		f.exec(t, f.f.Statement("INSERT INTO invitations(id,tenant_id,workspace_id,email_canonical,locale,status,token_hash,created_by,created_at,updated_at,expires_at) VALUES($1,$2,$3,$4,'en','pending',$5,$6,clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '1 hour')", "INSERT INTO invitations(id,tenant_id,workspace_id,email_canonical,locale,status,token_hash,created_by,created_at,updated_at,expires_at) VALUES(?,?,?,?,'en','pending',?,?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),TIMESTAMPADD(HOUR,1,UTC_TIMESTAMP(6)))", id, f.f.Tenant, f.f.Workspace, "synthetic+"+uuid.NewString()+"@example.invalid", security.Digest(security.Token()), f.f.User))
		return id
	}
	id := seed()
	i, e := f.accounts.RevokeInvitation(f.ctx, a, id, 1, "Synthetic sourced revoke", uuid.NewString())
	if e != nil || i.Status != "revoked" {
		t.Fatal("actual source refused valid revocation", e)
	}
	id = seed()
	f.exec(t, f.f.Statement("UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE id=$1", "UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE id=?", f.source.ConnectorID))
	if _, e = f.accounts.RevokeInvitation(f.ctx, a, id, 1, "Synthetic revoked source", uuid.NewString()); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
		t.Fatal("revoked source changed invitation", e)
	}
	var status string
	if e = f.f.Store.QueryRow(f.ctx, f.f.Statement("SELECT status FROM invitations WHERE id=$1", "SELECT status FROM invitations WHERE id=?", id)).Scan(&status); e != nil || status != "pending" {
		t.Fatal("revoked source leaked mutation", e)
	}
}
