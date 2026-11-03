package http_test

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/google/uuid"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// Retains the independent 46401939 reproduction with real SQL/login/positive
// controls, then covers the other platform owners sharing the Workspace prefix.
func TestM02DisabledDescendantRoutes(t *testing.T) {
	for _, profile := range []string{"workspaces", "account"} {
		t.Run(profile, func(t *testing.T) {
			newFixture := fixture.NewM02
			if profile == "account" {
				newFixture = fixture.NewAccount
			}
			s := newFixture(t)
			password := fixture.Bootstrap(t, s)
			handler := transport.New(s)
			for _, name := range []string{"Nodes", "Components", "Models", "Deployments", "Distribution", "Datasets", "Training", "ModelPreparations", "Knowledge", "Workflows", "Plugins", "Middleware", "AWS", "PrivateClusters", "ArtifactStores", "ImageRegistry", "TaskDeliveries", "Applications", "Notifications", "Preferences", "SSO", "Monitoring", "Releases"} {
				if !reflect.ValueOf(handler).Elem().FieldByName(name).IsNil() {
					t.Fatal("disabled owner constructed", name)
				}
			}
			server := httpfixture.NewTLSServer(t, handler)
			defer server.Close()
			schemas := httpfixture.Schemas(t, "../../../../../../api/openapi/v1/workspaces/openapi.json")
			portal := httpfixture.New(t, server, "portal", schemas)
			portal.Write(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
			me := portal.Call(http.MethodGet, "/api/v1/me", nil, 200, "")
			workspace := me["workspace"].(map[string]any)["id"].(string)
			base := "/api/v1/workspaces/" + workspace
			admin := httpfixture.New(t, server, "admin", schemas)
			admin.Write(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
			adminBase := "/api/v1/admin/workspaces/" + workspace
			if profile == "workspaces" {
				portal.Call(http.MethodGet, base+"/notes", nil, 200, "NoteListResponse")
				note := portal.Write(http.MethodPost, base+"/notes", map[string]any{"title": "Routing control", "text": "M02_ROUTING_CONTROL"}, 201, "NoteResponse")
				path := base + "/notes/" + note["id"].(string)
				portal.Call(http.MethodGet, path, nil, 200, "NoteResponse")
				if portal.Call(http.MethodGet, path+"/export", nil, 200, "text")["text"] != "M02_ROUTING_CONTROL" {
					t.Fatal("positive export control")
				}
				portal.Call(http.MethodGet, base+"/members", nil, 200, "MembershipListResponse")
				portal.Call(http.MethodGet, "/api/v1/workspaces/operations/"+uuid.NewString(), nil, 200, "OperationReceiptResponse")
				admin.Call(http.MethodGet, adminBase, nil, 200, "WorkspaceResponse")
				admin.Call(http.MethodGet, adminBase+"/members", nil, 200, "MembershipListResponse")
				admin.Call(http.MethodGet, "/api/v1/admin/workspaces/operations/"+uuid.NewString(), nil, 200, "OperationReceiptResponse")
				t.Log("positive real M02 account/note/read/export/membership/reconciliation controls passed before disabled descendants")
			} else if handler.Workspaces != nil {
				t.Fatal("account profile constructed M02 owner")
			}
			missing := func(client *httpfixture.Client, path string) {
				t.Helper()
				problem := client.Call(http.MethodGet, path, nil, 404, "Problem")
				if problem["code"] != "RESOURCE_NOT_FOUND" {
					t.Fatal("wrong refusal", path, problem["code"])
				}
			}
			node := uuid.NewString()
			descendants := []string{"nodes", "nodes/" + node, "nodes/" + node + "/components/node-exporter", "nodes/" + node + "/inference", "nodes/" + node + "/task-deliveries", "models", "models/" + uuid.NewString(), "deployments", "distribution", "datasets", "training", "model-preparations", "model-holdouts", "knowledge-bases", "workflows", "plugins", "middleware", "aws", "aws/imports", "private-clusters", "artifact-objects", "image-registry/assets", "notes/" + uuid.NewString() + "/models", "members/" + node + "/nodes/" + node + "/components/node-exporter"}
			for _, tail := range descendants {
				missing(portal, base+"/"+tail)
				missing(admin, adminBase+"/"+tail)
			}
			for _, path := range []string{"/api/v1/admin/nodes", "/api/v1/admin/models", "/api/v1/admin/datasets", "/api/v1/admin/sso/connectors", "/api/v1/auth/sso/connectors", "/api/v1/notifications", "/api/v1/preferences", "/api/v1/admin/monitoring"} {
				missing(portal, path)
				missing(admin, path)
			}
			if profile == "workspaces" {
				portal.CSRF = ""
				problem := portal.Call(http.MethodPost, base+"/nodes", map[string]any{}, 403, "Problem")
				if problem["code"] != "CSRF_INVALID" {
					t.Fatal("CSRF order changed")
				}
				problem = portal.Write(http.MethodPost, base+"/nodes", map[string]any{}, 404, "Problem")
				if problem["code"] != "RESOURCE_NOT_FOUND" {
					t.Fatal("write reached disabled owner")
				}
				portal.Call(http.MethodGet, base+"/notes", nil, 200, "NoteListResponse")
			}
			portal.Call(http.MethodGet, "/api/v1/me", nil, 200, "")
			t.Logf("%s: %d authenticated disabled-route GETs returned structured404; account remains available", profile, 2*len(descendants)+16)
		})
	}
}

// A paired original-dispatcher control: the platform's constructed Nodes owner
// still validates its own route. No node SQL/runtime is started by this test.
func TestM02MinimalRoutingPreservesPlatformNodeDispatch(t *testing.T) {
	s := fixture.NewM02(t)
	password := fixture.Bootstrap(t, s)
	profile := "platform"
	s.Config.Server.ApplicationProfile = &profile
	handler := transport.New(s)
	if handler.Nodes == nil {
		t.Fatal("platform Nodes owner missing")
	}
	server := httpfixture.NewTLSServer(t, handler)
	defer server.Close()
	portal := httpfixture.New(t, server, "portal", nil)
	portal.Write(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	me := portal.Call(http.MethodGet, "/api/v1/me", nil, 200, "")
	path := "/api/v1/workspaces/" + me["workspace"].(map[string]any)["id"].(string) + "/nodes/invalid-node-id"
	problem := portal.Call(http.MethodGet, path, nil, 400, "")
	if problem["code"] != "VALIDATION_FAILED" {
		t.Fatal("platform dispatch shadowed", problem["code"])
	}
}
