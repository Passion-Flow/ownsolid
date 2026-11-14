package sso

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestM11ActualSourcedPortalCurrentWorkspaceRolesMembershipAndTenantIsolation(t *testing.T) {
	f := sourceFixture(t)
	ctx := context.Background()
	accounts := f.s.Identity
	password, email := fixture.Password(), "scoped-sso@example.invalid"
	invite, e := accounts.Invite(ctx, f.a, "", "createInvitation", uuid.NewString(), email, "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = accounts.Register(ctx, fixture.InvitationToken(t, accounts, invite.ID), "Scoped synthetic user", password, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	user := fixture.Login(t, accounts, email, password, "portal")
	source := identity.SessionSource{TenantID: f.source.TenantID, ConnectorID: f.source.ConnectorID, BindingID: uuid.NewString(), FlowID: uuid.NewString(), AuthVersion: 1}
	if _, e = accounts.DB.Exec(ctx, `INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES($1,$2,$3,'3',$4)`, source.BindingID, source.TenantID, source.ConnectorID, user.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	f.pendingFlowForAudience(t, source.FlowID, "portal")
	// Explicit successful-provider-output fixture; real provider/React exchange
	// is independently exercised by the tagged provider/browser profiles.
	if _, e = accounts.DB.Exec(ctx, `UPDATE sso_flows SET state='claimed',claim_id=$2,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=$1`, source.FlowID, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = accounts.DB.Exec(ctx, `UPDATE sso_flows SET state='succeeded',authenticated_user_id=$2,finished_at=clock_timestamp() WHERE id=$1`, source.FlowID, user.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	var issued identity.IssuedSession
	e = f.s.transaction(ctx, func(tx pgx.Tx) error {
		if err := accounts.LockSessionUsers(ctx, tx, user.Me.User.ID, "", "portal"); err != nil {
			return err
		}
		var err error
		issued, err = accounts.IssueSession(ctx, tx, user.Me.User.ID, user.Epoch, "portal", "", uuid.NewString(), &source)
		return err
	})
	if e != nil {
		t.Fatal(e)
	}
	a, e := accounts.Authenticate(ctx, issued.Token, "portal")
	if e != nil || a.Me.User.IsPlatformAdmin {
		t.Fatal("sourced ordinary identity changed", e)
	}
	scopes := &workspaces.Service{DB: accounts.DB, Identity: accounts}
	w, e := scopes.Create(ctx, f.a, "Current sourced roles", f.a.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	membership, e := scopes.AddMember(ctx, f.a, w.ID, email, "viewer", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = scopes.Get(ctx, a, w.ID, false); e != nil {
		t.Fatal("viewer read denied", e)
	}
	_, e = scopes.CreateNote(ctx, a, w.ID, "Forbidden viewer write", "Synthetic", uuid.NewString())
	requireCode(t, e, "ACCESS_DENIED")
	for _, role := range []string{"member", "owner"} {
		membership, e = scopes.ChangeMember(ctx, f.a, w.ID, a.Me.User.ID, role, membership.Version, "Synthetic current-role change", uuid.NewString(), false)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = scopes.CreateNote(ctx, a, w.ID, "Allowed "+role, "Synthetic", uuid.NewString()); e != nil {
			t.Fatal("current role was not applied", e)
		}
		_, e = scopes.Get(ctx, a, w.ID, true)
		if role == "member" {
			requireCode(t, e, "ACCESS_DENIED")
		} else if e != nil {
			t.Fatal(e)
		}
	}
	membership, e = scopes.ChangeMember(ctx, f.a, w.ID, a.Me.User.ID, "viewer", membership.Version, "Synthetic live downgrade", uuid.NewString(), false)
	if e != nil {
		t.Fatal(e)
	}
	_, e = scopes.CreateNote(ctx, a, w.ID, "Forbidden downgraded write", "Synthetic", uuid.NewString())
	requireCode(t, e, "ACCESS_DENIED")
	foreign, e := scopes.Create(ctx, f.a, "Unshared synthetic scope", f.a.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, e = scopes.Get(ctx, a, foreign.ID, false)
	requireCode(t, e, "RESOURCE_NOT_FOUND")
	foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString()
	if _, e = accounts.DB.Exec(ctx, `INSERT INTO tenants(id,name,status) VALUES($1,'Foreign synthetic tenant','active')`, foreignTenant); e != nil {
		t.Fatal(e)
	}
	if _, e = accounts.DB.Exec(ctx, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign synthetic Workspace','active')`, foreignWorkspace, foreignTenant); e != nil {
		t.Fatal(e)
	}
	_, e = scopes.Get(ctx, a, foreignWorkspace, false)
	requireCode(t, e, "RESOURCE_NOT_FOUND")
	if _, e = scopes.ChangeMember(ctx, f.a, w.ID, a.Me.User.ID, "", membership.Version, "Synthetic membership revocation", uuid.NewString(), true); e != nil {
		t.Fatal(e)
	}
	_, e = scopes.Get(ctx, a, w.ID, false)
	requireCode(t, e, "RESOURCE_NOT_FOUND")
	_, e = scopes.CreateNote(ctx, a, w.ID, "Forbidden removed member", "Synthetic", uuid.NewString())
	requireCode(t, e, "RESOURCE_NOT_FOUND")
	if _, e = accounts.Authenticate(ctx, issued.Token, "portal"); e != nil {
		t.Fatal("membership removal unnecessarily destroyed ordinary user session", e)
	}
	if _, e = accounts.DB.Exec(ctx, `UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE id=$1`, source.ConnectorID); e != nil {
		t.Fatal(e)
	}
	_, e = scopes.CreateNote(ctx, a, a.Me.Workspace.ID, "Forbidden revoked source", "Synthetic", uuid.NewString())
	requireCode(t, e, "AUTH_SESSION_EXPIRED")
}
