package http_test

import (
	"context"
	"github.com/google/uuid"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"strings"
	"testing"
)

func TestM02HTTPContractsAndAuthorization(t *testing.T) {
	s := fixture.New(t)
	p := fixture.Bootstrap(t, s)
	ctx := context.Background()
	a := fixture.Login(t, s, "admin@example.invalid", p, "admin")
	password := fixture.Password()
	invite, e := s.Invite(ctx, a, "", "createInvitation", uuid.NewString(), "alice@example.invalid", "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Register(ctx, fixture.InvitationToken(t, s, invite.ID), "Alice", password, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	alice := fixture.Login(t, s, "alice@example.invalid", password, "portal")
	server := httpfixture.NewTLSServer(t, transport.New(s))
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../../api/openapi/v1/workspaces/openapi.json")
	makeClient := func(audience, email, password string) *httpfixture.Client {
		c := httpfixture.New(t, server, audience, schemas)
		c.Write("POST", "/api/v1/auth/login", map[string]any{"email": email, "password": password}, 200, "")
		return c
	}
	admin := makeClient("admin", "admin@example.invalid", p)
	portal := makeClient("portal", "alice@example.invalid", password)
	adminPortal := makeClient("portal", "admin@example.invalid", p)
	base := "/api/v1/admin/workspaces"
	w := admin.Write("POST", base, map[string]any{"name": "HTTP workspace", "owner_user_id": alice.Me.User.ID}, 201, "WorkspaceResponse")
	id := w["id"].(string)
	path := base + "/" + id
	admin.Call("GET", base, nil, 200, "WorkspaceListResponse")
	admin.Call("GET", path, nil, 200, "WorkspaceResponse")
	admin.Write("POST", base, map[string]any{"name": "HTTP workspace", "owner_user_id": alice.Me.User.ID}, 409, "Problem")
	portal.Call("GET", base, nil, 404, "Problem")
	admin.Call("GET", "/api/v1/workspaces", nil, 404, "Problem")
	adminPortal.Call("GET", "/api/v1/workspaces/"+id, nil, 404, "Problem")
	portalBase := "/api/v1/workspaces/" + id
	portal.Call("GET", "/api/v1/workspaces", nil, 200, "WorkspaceListResponse")
	portal.Call("GET", portalBase, nil, 200, "WorkspaceResponse")
	m := admin.Write("POST", path+"/members", map[string]any{"email": "admin@example.invalid", "role": "viewer"}, 201, "MembershipResponse")
	admin.Call("GET", path+"/members", nil, 200, "MembershipListResponse")
	portal.Call("GET", portalBase+"/members", nil, 200, "MembershipListResponse")
	portal.Write("POST", portalBase+"/members", map[string]any{"email": "admin@example.invalid", "role": "viewer"}, 409, "Problem")
	portal.Write("POST", portalBase+"/members", map[string]any{"email": "unknown@example.invalid", "role": "member"}, 404, "Problem")
	memberPath := portalBase + "/members/" + a.Me.User.ID
	m = portal.Write("PATCH", memberPath, map[string]any{"role": "member", "version": m["version"], "reason": "Synthetic promotion"}, 200, "MembershipResponse")
	admin.Write("PATCH", path+"/members/"+a.Me.User.ID, map[string]any{"role": "viewer", "version": m["version"], "reason": "Synthetic downgrade"}, 200, "MembershipResponse")
	note := portal.Write("POST", portalBase+"/notes", map[string]any{"title": "HTTP note", "text": "Synthetic note text"}, 201, "NoteResponse")
	np := portalBase + "/notes/" + note["id"].(string)
	portal.Call("GET", portalBase+"/notes", nil, 200, "NoteListResponse")
	portal.Call("GET", np, nil, 200, "NoteResponse")
	if adminPortal.Call("GET", np+"/export", nil, 200, "text")["text"] != "Synthetic note text" {
		t.Fatal("export content mismatch")
	}
	for _, operation := range []struct {
		method, path string
		body         any
	}{{"POST", portalBase + "/notes", map[string]any{"title": "Forbidden", "text": "text"}}, {"PATCH", np, map[string]any{"title": "Forbidden", "text": "text", "version": 1}}, {"DELETE", np, map[string]any{"version": 1}}, {"PATCH", portalBase, map[string]any{"name": "Forbidden", "status": "active", "version": 1, "reason": "Synthetic"}}, {"POST", portalBase + "/members", map[string]any{"email": "alice@example.invalid", "role": "owner"}}, {"DELETE", memberPath, map[string]any{"version": 2, "reason": "Synthetic"}}} {
		adminPortal.Write(operation.method, operation.path, operation.body, 403, "Problem")
	}
	portal.CSRF = ""
	portal.Call("DELETE", np, map[string]any{"version": 1}, 403, "Problem")
	for _, body := range [][]byte{[]byte(`{"title":"x","text":"x","tenant_id":"injected"}`), []byte(`{"title":"x","title":"y","text":"x"}`), []byte(`{"title":"x"}`)} {
		portal.Write("POST", portalBase+"/notes", body, 400, "Problem")
	}
	portal.Write("POST", portalBase+"/notes", map[string]any{"title": "Oversize", "text": strings.Repeat("x", 8193)}, 400, "Problem")
	for _, query := range []string{"limit=0", "limit=101", "limit=1&limit=2", "tenant_id=" + w["tenant_id"].(string), "cursor=garbage"} {
		portal.Call("GET", portalBase+"/notes?"+query, nil, 400, "Problem")
	}
	note = portal.Write("PATCH", np, map[string]any{"title": "Edited", "text": "Updated note", "version": note["version"]}, 200, "NoteResponse")
	portal.Write("PATCH", np, map[string]any{"title": "Stale", "text": "stale", "version": 1}, 409, "Problem")
	portal.Write("DELETE", np, map[string]any{"version": note["version"]}, 204, "")
	portal.Call("GET", np, nil, 404, "Problem")
	portal.Call("GET", np+"/export", nil, 404, "Problem")
	members := admin.Call("GET", path+"/members", nil, 200, "MembershipListResponse")
	for _, item := range members["items"].([]any) {
		m := item.(map[string]any)
		if m["user_id"] == a.Me.User.ID {
			admin.Write("DELETE", path+"/members/"+a.Me.User.ID, map[string]any{"version": m["version"], "reason": "Synthetic revoke"}, 204, "")
		}
	}
	w = admin.Call("GET", path, nil, 200, "WorkspaceResponse")
	w = portal.Write("PATCH", portalBase, map[string]any{"name": "Renamed", "status": "active", "version": w["version"], "reason": "Synthetic rename"}, 200, "WorkspaceResponse")
	w = admin.Write("PATCH", path, map[string]any{"name": "Renamed", "status": "disabled", "version": w["version"], "reason": "Synthetic archive"}, 200, "WorkspaceResponse")
	portal.Call("GET", portalBase, nil, 404, "Problem")
	admin.Write("PATCH", path, map[string]any{"name": "Renamed", "status": "active", "version": w["version"], "reason": "Synthetic restore"}, 200, "WorkspaceResponse")
}
