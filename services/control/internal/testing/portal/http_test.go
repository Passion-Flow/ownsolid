//go:build portal_database_integration

package portal_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestM15ActualPortalHumanTLSGeneratedContractsStrictCSRFAndPersistence(t *testing.T) {
	fixture.PreflightOwnedPostgres(t)
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	ctx := context.Background()
	account.Config.Workflows.Enabled = true
	actor := fixture.Login(t, account, "admin@example.invalid", password, "portal")
	h := identityhttp.New(account)
	if e := h.Notifications.Service.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := h.Notifications.Service.Stop(ctx); e != nil {
			t.Error(e)
		}
	}()
	server := httpfixture.NewTLSServer(t, h)
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/preferences/openapi.json")
	for _, module := range []string{"applications", "notifications"} {
		for name, schema := range httpfixture.Schemas(t, "../../../../../api/openapi/v1/"+module+"/openapi.json") {
			schemas[name] = schema
		}
	}
	portal := httpfixture.New(t, server, "portal", schemas)
	admin := httpfixture.New(t, server, "admin", schemas)
	for _, client := range []*httpfixture.Client{portal, admin} {
		client.Write("POST", "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": password}, 200, "")
	}
	p := portal.Call("GET", "/api/v1/preferences", nil, 200, "PreferenceResponse")
	if p["theme"] != "system" || p["density"] != "comfortable" || p["revision"] != float64(1) {
		t.Fatal("initial persisted appearance")
	}
	body := map[string]any{"theme": "dark", "expected_revision": 1}
	portal.CSRF = ""
	portal.Call("PATCH", "/api/v1/preferences", body, 403, "Problem")
	portal.CSRF = portal.Call("GET", "/api/v1/auth/csrf", nil, 200, "")["token"].(string)
	portal.Headers = map[string]string{"Origin": "https://foreign.example.invalid"}
	portal.Call("PATCH", "/api/v1/preferences", body, 403, "Problem")
	portal.Headers = nil
	for _, raw := range []string{`null`, `[]`, `{}`, `{"theme":"dark","expected_revision":1,"user_id":"foreign"}`, `{"Theme":"dark","expected_revision":1}`, `{"theme":"dark","theme":"light","expected_revision":1}`, `{"theme":null,"expected_revision":1}`} {
		portal.Write("PATCH", "/api/v1/preferences", []byte(raw), 400, "Problem")
	}
	portal.Headers = map[string]string{"Content-Type": "text/plain"}
	portal.Write("PATCH", "/api/v1/preferences", body, 415, "Problem")
	portal.Headers = nil
	p = portal.Write("PATCH", "/api/v1/preferences", body, 200, "PreferenceResponse")
	if p["theme"] != "dark" || p["revision"] != float64(2) {
		t.Fatal("actual theme update")
	}
	portal.Write("PATCH", "/api/v1/preferences", body, 409, "Problem")
	// A change of one field keeps the other, which another browser may have set.
	p = portal.Write("PATCH", "/api/v1/preferences", map[string]any{"density": "compact", "expected_revision": 2}, 200, "PreferenceResponse")
	if p["theme"] != "dark" || p["density"] != "compact" || p["revision"] != float64(3) {
		t.Fatal("density update must keep the theme")
	}
	for _, raw := range []string{`{"density":"cozy","expected_revision":3}`, `{"theme":"auto","expected_revision":3}`, `{"expected_revision":3}`, `{"theme":null,"density":"compact","expected_revision":3}`} {
		portal.Write("PATCH", "/api/v1/preferences", []byte(raw), 400, "Problem")
	}
	portal.Call("GET", "/api/v1/preferences?user_id="+uuid.NewString(), nil, 400, "Problem")
	// Both entrances address the same current account, without a caller override.
	if shared := admin.Call("GET", "/api/v1/preferences", nil, 200, "PreferenceResponse"); shared["theme"] != "dark" || shared["density"] != "compact" {
		t.Fatal("same account appearance not durable")
	}
	workspace := actor.Me.Workspace.ID
	portal.Call("GET", "/api/v1/applications?workspace_id="+workspace, nil, 200, "ApplicationPageResponse")
	portal.Call("GET", "/api/v1/application-conversations?workspace_id="+workspace, nil, 200, "ApplicationConversationPageResponse")
	admin.Call("GET", "/api/v1/applications?workspace_id="+workspace, nil, 404, "Problem")
	for _, query := range []string{"", "?workspace_id=" + workspace + "&workspace_id=" + workspace, "?workspace_id=" + workspace + "&recipient_id=" + uuid.NewString(), "?workspace_id=" + workspace + "&limit=0"} {
		portal.Call("GET", "/api/v1/applications"+query, nil, 400, "Problem")
	}
	portal.Call("GET", "/api/v1/notifications", nil, 200, "TerminalNotificationPageResponse")
	portal.Call("GET", "/api/v1/notifications/configuration", nil, 200, "NotificationConfigurationResponse")
	draft, e := h.Workflows.Service.Create(ctx, actor, workflows.CreateRequest{WorkspaceID: workspace, Name: "HTTP notice SQL fixture", Reason: "Synthetic metadata for strict response contract"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	run := uuid.NewString()
	if _, e = account.DB.Exec(ctx, `INSERT INTO workflow_runs(id,tenant_id,workspace_id,workflow_id,owner_id,draft_revision,execution_authority,membership_authority,requested_audience,graph_snapshot,graph_sha256,state,question,deadline_at) SELECT $1,f.tenant_id,f.workspace_id,f.id,$2,f.revision,f.execution_authority,m.authority_id,'portal',f.draft_graph,f.draft_sha256,'pending','Synthetic private HTTP body',clock_timestamp()+interval '1 minute' FROM workflows f JOIN workspace_memberships m ON m.tenant_id=f.tenant_id AND m.workspace_id=f.workspace_id AND m.user_id=$2 WHERE f.id=$3`, run, actor.Me.User.ID, draft.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = account.DB.Exec(ctx, "UPDATE workflow_runs SET state='failed',error_code='SYNTHETIC_HTTP_FAILURE',finished_at=clock_timestamp() WHERE id=$1", run); e != nil {
		t.Fatal(e)
	}
	notices := portal.Call("GET", "/api/v1/notifications?limit=1", nil, 200, "TerminalNotificationPageResponse")["items"].([]any)
	if len(notices) != 1 {
		t.Fatal("real trigger notice unavailable")
	}
	id := notices[0].(map[string]any)["id"].(string)
	path := "/api/v1/notifications/" + id + "/read"
	for _, raw := range []string{`null`, `[]`, `{"recipient_id":"foreign"}`, `{"x":null}`} {
		portal.Write("POST", path, []byte(raw), 400, "Problem")
	}
	n := portal.Write("POST", path, map[string]any{}, 200, "TerminalNotificationResponse")
	again := portal.Write("POST", path, map[string]any{}, 200, "TerminalNotificationResponse")
	if n["read_at"] == nil || n["read_at"] != again["read_at"] {
		t.Fatal("HTTP read is not monotone idempotent")
	}
	portal.Write("POST", "/api/v1/notifications/"+uuid.NewString()+"/read", map[string]any{}, 404, "Problem")
	portal.Call("GET", "/api/v1/notifications?recipient_id="+uuid.NewString(), nil, 400, "Problem")
	fresh := httpfixture.New(t, server, "portal", schemas)
	fresh.Write("POST", "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": password}, 200, "")
	if fresh.Call("GET", "/api/v1/preferences", nil, 200, "PreferenceResponse")["theme"] != "dark" {
		t.Fatal("fresh login lost preference")
	}
	fresh.Call("GET", "/api/v1/notifications?limit=1", nil, 200, "TerminalNotificationPageResponse")
}
