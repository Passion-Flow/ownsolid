package http_test

import (
	"github.com/google/uuid"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"reflect"
	"testing"
)

func TestM03MinimalCompositionRoutes(t *testing.T) {
	s := fixture.NewM03(t)
	password := fixture.Bootstrap(t, s)
	h := transport.New(s)
	for _, name := range []string{"Components", "Models", "Deployments", "Distribution", "Datasets", "Training", "ModelPreparations", "Knowledge", "Workflows", "Plugins", "SSO", "ImageRegistry", "Monitoring", "TaskDeliveries", "Releases"} {
		if !reflect.ValueOf(h).Elem().FieldByName(name).IsNil() {
			t.Fatal("future owner constructed", name)
		}
	}
	server := httpfixture.NewTLSServer(t, h)
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../../api/openapi/v1/nodes/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	admin.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	portal := httpfixture.New(t, server, "portal", schemas)
	portal.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	me := portal.Call("GET", "/api/v1/me", nil, 200, "")
	workspace := me["workspace"].(map[string]any)["id"].(string)
	base := "/api/v1/workspaces/" + workspace
	portal.Call("GET", base+"/notes", nil, 200, "")
	portal.Call("GET", base+"/nodes", nil, 200, "NodeListResponse")
	admin.Call("GET", "/api/v1/admin/nodes", nil, 200, "NodeListResponse")
	node := uuid.NewString()
	task := uuid.NewString()
	for _, tail := range []string{"models", "training", "model-preparations", "knowledge-bases", "nodes/" + node + "/components/node-exporter", "nodes/" + node + "/inference", "nodes/" + node + "/task-deliveries"} {
		portal.Call("GET", base+"/"+tail, nil, 404, "Problem")
	}
	for _, path := range []string{"/api/v1/admin/models", "/api/v1/admin/training", "/api/v1/admin/nodes/" + node + "/components/node-exporter", "/api/v1/admin/sso/connectors", "/gateway/v1/claims", "/agent/v1/image-registry/assets", "/agent/v1/nodes/" + node + "/tasks/" + task + "/artifact", "/agent/v1/nodes/" + node + "/tasks/" + task + "/renewals"} {
		admin.Call("GET", path, nil, 404, "Problem")
	}
	admin.Write("POST", "/api/v1/admin/node-join-tokens", map[string]any{"workspace_id": workspace, "node_name": "Refused later profile", "execution_profile": "systemd_components"}, 403, "Problem")
	t.Log("minimal M03 enables only current human/discovery machine routes; disabled descendants return structured404 without optional dispatch")
}
