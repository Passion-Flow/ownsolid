//go:build portal_database_integration

package sso

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/notifications"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/preferences"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestM15ActualEveryPortalServiceRechecksRevokedSSOSource(t *testing.T) {
	fixture.PreflightOwnedPostgres(t)
	for _, revoked := range []string{"connector", "binding"} {
		t.Run(revoked, func(t *testing.T) {
			f := sourceFixture(t)
			ctx := context.Background()
			account := f.s.Identity
			issued := f.issue(t, "")
			a, e := account.Authenticate(ctx, issued.Token, "admin")
			if e != nil {
				t.Fatal(e)
			}
			pref := &preferences.Service{DB: account.DB, Identity: account}
			notice := &notifications.Service{DB: account.DB, Identity: account}
			if e = notice.Start(ctx); e != nil {
				t.Fatal(e)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if e := notice.Stop(ctx); e != nil {
					t.Error(e)
				}
			}()
			if _, e = pref.Get(ctx, a); e != nil {
				t.Fatal(e)
			}
			if _, e = notice.List(ctx, a, url.Values{}); e != nil {
				t.Fatal(e)
			}
			portalSource := f.source
			portalSource.FlowID = uuid.NewString()
			f.pendingFlowForAudience(t, portalSource.FlowID, "portal")
			if _, e = account.DB.Exec(ctx, `UPDATE sso_flows SET state='claimed',claim_id=$2,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=$1`, portalSource.FlowID, uuid.NewString()); e != nil {
				t.Fatal(e)
			}
			if _, e = account.DB.Exec(ctx, `UPDATE sso_flows SET state='succeeded',authenticated_user_id=$2,finished_at=clock_timestamp() WHERE id=$1`, portalSource.FlowID, f.a.Me.User.ID); e != nil {
				t.Fatal(e)
			}
			var issuedPortal identity.IssuedSession
			if e = f.s.transaction(ctx, func(tx pgx.Tx) error {
				if e := account.LockSessionUsers(ctx, tx, f.a.Me.User.ID, "", "portal"); e != nil {
					return e
				}
				var e error
				issuedPortal, e = account.IssueSession(ctx, tx, f.a.Me.User.ID, f.a.Epoch, "portal", "", uuid.NewString(), &portalSource)
				return e
			}); e != nil {
				t.Fatal(e)
			}
			consumer, e := account.Authenticate(ctx, issuedPortal.Token, "portal")
			if e != nil {
				t.Fatal(e)
			}
			ws := &workspaces.Service{DB: account.DB, Identity: account}
			engine := &workflows.Service{DB: account.DB, Identity: account, Workspaces: ws}
			apps := &applications.Service{DB: account.DB, Identity: account, Workspaces: ws, Workflows: engine}
			if _, e = apps.List(ctx, consumer, consumer.Me.Workspace.ID, url.Values{}); e != nil {
				t.Fatal("live SSO consumer application discovery", e)
			}
			if _, e = apps.Conversations(ctx, consumer, consumer.Me.Workspace.ID, url.Values{}); e != nil {
				t.Fatal("live SSO private conversation discovery", e)
			}
			id := f.source.ConnectorID
			query := `UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1,updated_at=clock_timestamp() WHERE id=$1`
			if revoked == "binding" {
				id = f.source.BindingID
				query = `UPDATE sso_bindings SET active=false,revision=revision+1 WHERE id=$1`
			}
			if _, e = account.DB.Exec(ctx, query, id); e != nil {
				t.Fatal(e)
			}
			checks := map[string]func() error{"preference_get": func() error { _, e := pref.Get(ctx, a); return e }, "preference_update": func() error {
				_, e := pref.Update(ctx, a, preferences.UpdateRequest{Theme: "dark", ExpectedRevision: 1}, uuid.NewString())
				return e
			}, "notice_list": func() error { _, e := notice.List(ctx, a, url.Values{}); return e }, "notice_configuration": func() error { _, e := notice.Configuration(ctx, a); return e }, "notice_get": func() error { _, e := notice.Get(ctx, a, uuid.NewString()); return e }, "notice_read": func() error { _, e := notice.Read(ctx, a, uuid.NewString(), uuid.NewString()); return e }}
			for name, check := range checks {
				t.Run(name, func(t *testing.T) { requireCode(t, check(), "AUTH_SESSION_EXPIRED") })
			}

			for name, check := range map[string]func() error{"application_list": func() error { _, e := apps.List(ctx, consumer, consumer.Me.Workspace.ID, url.Values{}); return e }, "private_conversations": func() error {
				_, e := apps.Conversations(ctx, consumer, consumer.Me.Workspace.ID, url.Values{})
				return e
			}} {
				t.Run(name, func(t *testing.T) { requireCode(t, check(), "AUTH_SESSION_EXPIRED") })
			}
		})
	}
}
