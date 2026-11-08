//go:build repository_integration

package productagent

import (
	"context"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	"github.com/google/uuid"
)

func TestOfflineCommandsWithCurrentSessionAndWorkspace(t *testing.T) {
	fixture := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	owner := &identity.Service{Storage: fixture.Store}
	owner.Config.Workflows.Enabled = true
	policy := owner.Config.Policy()
	policy.UI.PageSize = 1
	owner.Config.Runtime = &policy
	spaces := &workspaces.Service{Identity: owner}
	flow := &workflows.Service{Identity: owner, Workspaces: spaces}
	service, err := New(&applications.Service{Identity: owner, Workspaces: spaces, Workflows: flow})
	if err != nil {
		t.Fatal(err)
	}
	exec := func(pg, mysql string, args ...any) {
		t.Helper()
		if _, err := fixture.Store.Exec(ctx, fixture.Statement(pg, mysql, args...)); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role,authority_id) VALUES($1,$2,$3,'owner',$4)`,
		`INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role,authority_id) VALUES(?,?,?,'owner',?)`,
		fixture.User, fixture.Tenant, fixture.Workspace, uuid.NewString())
	authenticate := func(audience string) identity.Auth {
		t.Helper()
		var issued identity.IssuedSession
		if err := fixture.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			var err error
			issued, err = owner.IssueSessionWithin(ctx, tx, fixture.User, 1, audience, "", uuid.NewString(), nil)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		actor, err := owner.Authenticate(ctx, issued.Token, audience)
		if err != nil {
			t.Fatal(err)
		}
		return actor
	}
	admin := authenticate("admin")
	portal := authenticate("portal")
	if result, err := service.Run(ctx, admin, "open nodes", ""); err != nil || result.Path != "/nodes" {
		t.Fatalf("console navigation: %#v, %v", result, err)
	}
	if result, err := service.Run(ctx, portal, "\u6253\u5f00\u5bc6\u94a5", ""); err != nil || result.Path != "/keys" {
		t.Fatalf("portal navigation: %#v, %v", result, err)
	}
	if result, err := service.Run(ctx, portal, "show applications", ""); err != nil || result.Applications == nil || result.Applications.Items == nil || len(result.Applications.Items) != 0 {
		t.Fatalf("read scoped empty applications: %#v, %v", result, err)
	}
	if result, err := service.Run(ctx, portal, "show conversations", ""); err != nil || result.Conversations == nil || result.Conversations.Items == nil || len(result.Conversations.Items) != 0 {
		t.Fatalf("read scoped empty conversations: %#v, %v", result, err)
	}
	publish := func(name string) (string, string) {
		t.Helper()
		workflow, err := flow.Create(ctx, portal, workflows.CreateRequest{WorkspaceID: fixture.Workspace, Name: name, Reason: "Assistant projection fixture"}, uuid.NewString(), uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		version := uuid.NewString()
		// Seed a published projection without requiring a model or executing the graph.
		exec(`INSERT INTO workflow_versions(id,tenant_id,workspace_id,workflow_id,version_number,draft_revision,graph,graph_sha256,published_by) SELECT $1,tenant_id,workspace_id,id,1,revision,draft_graph,draft_sha256,created_by FROM workflows WHERE id=$2`,
			`INSERT INTO workflow_versions(id,tenant_id,workspace_id,workflow_id,version_number,draft_revision,graph,graph_sha256,published_by) SELECT ?,tenant_id,workspace_id,id,1,revision,draft_graph,draft_sha256,created_by FROM workflows WHERE id=?`, version, workflow.ID)
		exec(`UPDATE workflows SET current_version_id=$1 WHERE id=$2`, `UPDATE workflows SET current_version_id=? WHERE id=?`, version, workflow.ID)
		return workflow.ID, version
	}
	workflow, version := publish("First fixture application")
	publish("Second fixture application")
	page, err := service.Run(ctx, portal, "show applications", "")
	if err != nil || page.Applications == nil || len(page.Applications.Items) != 1 || page.Applications.NextCursor == nil {
		t.Fatalf("first application page: %#v, %v", page, err)
	}
	cursor := *page.Applications.NextCursor
	following, err := service.Run(ctx, portal, "\u67e5\u770b\u5e94\u7528", cursor)
	if err != nil || following.Applications == nil || len(following.Applications.Items) != 1 || following.Applications.Items[0].WorkflowID == page.Applications.Items[0].WorkflowID || following.Applications.NextCursor != nil {
		t.Fatalf("second application page: %#v, %v", following, err)
	}
	if result, err := service.Run(ctx, portal, "show conversations", cursor); err == nil || result != (Result{}) {
		t.Fatalf("application cursor accepted for conversation list: %#v, %v", result, err)
	}
	otherUser := uuid.NewString()
	email := otherUser + "@example.invalid"
	exec(`INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$3,'Other fixture user','en','active',false)`,
		`INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Other fixture user','en','active',false)`, otherUser, email, email)
	conversation := func(user string) string {
		id := uuid.NewString()
		exec(`INSERT INTO workflow_conversations(id,tenant_id,workspace_id,workflow_id,version_id,owner_id) VALUES($1,$2,$3,$4,$5,$6)`,
			`INSERT INTO workflow_conversations(id,tenant_id,workspace_id,workflow_id,version_id,owner_id) VALUES(?,?,?,?,?,?)`, id, fixture.Tenant, fixture.Workspace, workflow, version, user)
		return id
	}
	ownConversation := conversation(fixture.User)
	conversation(otherUser)
	if result, err := service.Run(ctx, portal, "show conversations", ""); err != nil || result.Conversations == nil || len(result.Conversations.Items) != 1 || result.Conversations.Items[0].ID != ownConversation || result.Conversations.NextCursor != nil {
		t.Fatalf("private conversation list: %#v, %v", result, err)
	}

	otherTenant, otherWorkspace := uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO tenants(id,name,status) VALUES($1,'Other tenant','active')`, `INSERT INTO tenants(id,name,status) VALUES(?,'Other tenant','active')`, otherTenant)
	exec(`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other workspace','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Other workspace','active')`, otherWorkspace, otherTenant)
	outsideWorkspace := portal
	outsideWorkspace.Me.Workspace = &identity.Workspace{ID: otherWorkspace, TenantID: otherTenant}
	if result, err := service.Run(ctx, outsideWorkspace, "show applications", ""); err == nil || err.Error() != "RESOURCE_NOT_FOUND" || result != (Result{}) {
		t.Fatalf("read outside scope: %#v, %v", result, err)
	}

	exec(`UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='logout' WHERE id=$1`, `UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='logout' WHERE id=?`, portal.SessionID)
	for _, cmd := range []string{"open home", "show applications", "show conversations"} {
		if result, err := service.Run(ctx, portal, cmd, ""); err == nil || err.Error() != "AUTH_SESSION_EXPIRED" || result != (Result{}) {
			t.Errorf("%s with revoked session: %#v, %v", cmd, result, err)
		}
	}
}
