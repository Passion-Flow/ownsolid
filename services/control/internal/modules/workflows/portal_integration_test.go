//go:build portal_database_integration && plugin_database_integration && knowledge_encoder_integration

package workflows_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/preferences"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestM15ActualPortalPublishedDiscoveryPrivateConversationNoticeAndAuditRollback(t *testing.T) {
	h, admin, portal, _, private := toolFixture(t)
	ctx := context.Background()
	s := h.Workflows.Service
	workspace := portal.Me.Workspace.ID
	if e := h.Notifications.Service.Start(ctx); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := h.Notifications.Service.Stop(ctx); e != nil {
			t.Error(e)
		}
	})
	apps := h.Applications.Service
	notices := h.Notifications.Service
	before, e := apps.List(ctx, portal, workspace, url.Values{})
	if e != nil || len(before.Items) != 0 {
		t.Fatal("real empty application center", e)
	}
	version, _ := installTool(t, h.Plugins.Service, admin, private, "1.0.0")
	draft, e := s.Create(ctx, portal, workflows.CreateRequest{WorkspaceID: workspace, Name: strings.Repeat("A", 110) + " · \u5de5\u4f5c\u6d41", Reason: "Actual Portal application source"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	draft, e = s.Update(ctx, portal, workspace, draft.ID, workflows.UpdateRequest{Name: draft.Name, Graph: toolGraph(version.ID), Enabled: true, ExpectedRevision: draft.Revision, Reason: "Actual signed directory application"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if page, e := apps.List(ctx, portal, workspace, url.Values{}); e != nil || len(page.Items) != 0 {
		t.Fatal("unpublished draft appeared", e)
	}
	publication, e := s.Publish(ctx, admin, workspace, draft.ID, workflows.RevisionReason{ExpectedRevision: draft.Revision, Reason: "Admin publishes initial application"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	page, e := apps.List(ctx, portal, workspace, url.Values{})
	if e != nil || len(page.Items) != 1 || page.Items[0].WorkflowID != draft.ID || page.Items[0].VersionID != publication.ID || !page.Items[0].CanExecute {
		t.Fatal("actual published application projection", e, page)
	}
	raw, _ := json.Marshal(page)
	for _, field := range []string{"draft_graph", "instructions", "configuration", "manifest", "path", "question"} {
		if strings.Contains(string(raw), `"`+field+`"`) {
			t.Fatal("application projection disclosed private graph", field)
		}
	}
	if _, e = apps.List(ctx, admin, workspace, url.Values{}); e == nil || e.Error() != "ACCESS_DENIED" {
		t.Fatal("admin audience became consumer", e)
	}
	if _, e = apps.List(ctx, portal, uuid.NewString(), url.Values{}); e == nil {
		t.Fatal("foreign Workspace discovered")
	}
	conversation, e := s.CreateConversation(ctx, portal, workspace, draft.ID, workflows.ConversationRequest{VersionID: publication.ID, Reason: "Real application center start"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	input := workflows.RunRequest{Question: "Private application question · \u6d4b\u8bd5", Reason: "Actual consumer interaction"}
	intent := uuid.NewString()
	admitted, e := s.RunConversation(ctx, portal, workspace, draft.ID, conversation.ID, input, intent, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	result := waitTool(t, s, portal, draft.ID, admitted.ID)
	expectToolSuccess(t, result)
	var notice string
	if e = h.Service.DB.QueryRow(ctx, "SELECT id::text FROM terminal_notifications WHERE run_id=$1 AND outcome='succeeded'", result.ID).Scan(&notice); e != nil {
		t.Fatal("real executor completion notice", e)
	}
	replay, e := s.RunConversation(ctx, portal, workspace, draft.ID, conversation.ID, input, intent, uuid.NewString())
	if e != nil || replay.ID != result.ID {
		t.Fatal("consumer exact replay", e)
	}
	var count int
	if e = h.Service.DB.QueryRow(ctx, "SELECT count(*) FROM terminal_notifications WHERE run_id=$1", result.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal("replay duplicated notice", e, count)
	}
	histories, e := apps.Conversations(ctx, portal, workspace, url.Values{})
	if e != nil || len(histories.Items) != 1 || histories.Items[0].ID != conversation.ID || histories.Items[0].VersionID != publication.ID || histories.Items[0].TurnCount != 1 {
		t.Fatal("durable private conversation discovery", e, histories)
	}
	if _, e = notices.Read(ctx, portal, notice, uuid.NewString()); e != nil {
		t.Fatal("actual deep link read state", e)
	}
	preference, e := h.Preferences.Service.Update(ctx, portal, preferences.UpdateRequest{Theme: preferences.DarkTheme, ExpectedRevision: 1}, uuid.NewString())
	if e != nil || preference.Theme != preferences.DarkTheme {
		t.Fatal(e)
	}
	// Register an ordinary second identity through the real invitation path. No
	// delivery worker runs and no external recipient is contacted.
	email := uuid.NewString() + "@example.invalid"
	invitation, e := h.Service.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), email, "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	password := fixture.Password()
	if e = h.Service.Register(ctx, fixture.InvitationToken(t, h.Service, invitation.ID), "Other application user", password, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	other := fixture.Login(t, h.Service, email, password, "portal")
	member, e := h.Workspaces.Service.ChangeMember(ctx, admin, workspace, other.Me.User.ID, "viewer", 1, "Actual viewer private history boundary", uuid.NewString(), false)
	if e != nil || member.Role != "viewer" {
		t.Fatal("current viewer membership", e)
	}
	if histories, e := apps.Conversations(ctx, other, workspace, url.Values{}); e != nil || len(histories.Items) != 0 {
		t.Fatal("other owner conversation disclosure", e)
	}
	if _, e = notices.Read(ctx, other, notice, uuid.NewString()); e == nil || e.Error() != "NOTIFICATION_NOT_FOUND" {
		t.Fatal("other owner notification disclosure", e)
	}
	if _, e = s.Conversation(ctx, other, workspace, draft.ID, conversation.ID); e == nil {
		t.Fatal("other owner conversation deep link")
	}
	// A failed terminal audit must roll back the success notice in the same
	// transaction; the real executor then records its truthful failure notice.
	if _, e = h.Service.DB.Exec(ctx, `CREATE FUNCTION portal_workflow_reject_success_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='workflow.run_succeeded' THEN RAISE EXCEPTION 'SYNTHETIC_PORTAL_FINAL_AUDIT_REFUSAL';END IF;RETURN NEW;END;$$;CREATE TRIGGER portal_workflow_reject_success_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION portal_workflow_reject_success_audit()`); e != nil {
		t.Fatal(e)
	}
	failed, e := s.RunConversation(ctx, portal, workspace, draft.ID, conversation.ID, workflows.RunRequest{Question: "Real final audit rollback", Reason: "Failure recovery acceptance"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	failed = waitTool(t, s, portal, draft.ID, failed.ID)
	if failed.State != "failed" || failed.ResultAvailable || failed.ToolResult != nil {
		t.Fatal("failed terminal leaked success", failed.State, failed.ErrorCode)
	}
	if e = h.Service.DB.QueryRow(ctx, "SELECT count(*) FROM terminal_notifications WHERE run_id=$1 AND outcome='failed'", failed.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal("truthful failure notice", e, count)
	}
	if e = h.Service.DB.QueryRow(ctx, "SELECT count(*) FROM terminal_notifications WHERE run_id=$1 AND outcome='succeeded'", failed.ID).Scan(&count); e != nil || count != 0 {
		t.Fatal("phantom success notice", e, count)
	}
	if _, e = h.Service.DB.Exec(ctx, "DROP TRIGGER portal_workflow_reject_success_audit ON audit_events;DROP FUNCTION portal_workflow_reject_success_audit()"); e != nil {
		t.Fatal(e)
	}
	draft, e = s.Get(ctx, admin, workspace, draft.ID)
	if e != nil {
		t.Fatal(e)
	}
	draft, e = s.Update(ctx, admin, workspace, draft.ID, workflows.UpdateRequest{Name: draft.Name, Graph: toolGraph(version.ID), Enabled: false, ExpectedRevision: draft.Revision, Reason: "Admin withdraws application"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if page, e := apps.List(ctx, portal, workspace, url.Values{}); e != nil || len(page.Items) != 0 {
		t.Fatal("withdrawn application still discoverable", e)
	}
	histories, e = apps.Conversations(ctx, portal, workspace, url.Values{})
	if e != nil || len(histories.Items) != 1 || histories.Items[0].CanExecute || histories.Items[0].VersionID != publication.ID {
		t.Fatal("withdrawal lost or retargeted history", e)
	}
	if _, e = s.RunConversation(ctx, portal, workspace, draft.ID, conversation.ID, input, uuid.NewString(), uuid.NewString()); e == nil {
		t.Fatal("withdrawn application admitted work")
	}
	retained, e := s.GetRun(ctx, portal, workspace, draft.ID, result.ID)
	if e != nil {
		t.Fatal(e)
	}
	expectToolSuccess(t, retained)
	current, e := s.Conversation(ctx, portal, workspace, draft.ID, conversation.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.DeleteConversation(ctx, portal, workspace, draft.ID, conversation.ID, workflows.RevisionReason{ExpectedRevision: current.Revision, Reason: "Consumer removes private conversation"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = notices.Get(ctx, portal, notice); e == nil || e.Error() != "NOTIFICATION_NOT_FOUND" {
		t.Fatal("deleted private conversation still disclosed by notice", e)
	}
	if histories, e := apps.Conversations(ctx, portal, workspace, url.Values{}); e != nil || len(histories.Items) != 0 {
		t.Fatal("deleted conversation still discoverable", e)
	}
}
