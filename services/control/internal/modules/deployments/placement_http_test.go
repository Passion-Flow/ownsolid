package deployments_test

import (
	"testing"

	"github.com/google/uuid"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
)

// The placement preview answers as its contract states, over the real transport, and only to a
// platform administrator in the admin entrance.
func TestRealTLSPlacementContract(t *testing.T) {
	c := setup(t)
	server := httpfixture.NewTLSServer(t, transport.New(c.s.Nodes.Identity))
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/deployments/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	admin.Headers = map[string]string{}
	admin.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": c.password}, 200, "")
	out := admin.Call("GET", "/api/v1/admin/deployments/placement?model_id="+c.model, nil, 200, "PlacementResponse")
	recommended, _ := out["recommended"].(map[string]any)
	if out["model_id"] != c.model || recommended["node_id"] != c.node.NodeID || recommended["gpu_uuid"] != gpuID {
		t.Fatalf("placement did not recommend the only eligible GPU: %v", out)
	}
	admin.Call("GET", "/api/v1/admin/deployments/placement", nil, 400, "Problem")
	admin.Call("GET", "/api/v1/admin/deployments/placement?model_id="+c.model+"&workspace_id="+c.a.Me.Workspace.ID, nil, 400, "Problem")
	admin.Call("GET", "/api/v1/admin/deployments/placement?model_id=not-a-uuid", nil, 400, "Problem")
	admin.Call("GET", "/api/v1/admin/deployments/placement?model_id="+uuid.NewString(), nil, 404, "Problem")
	portal := httpfixture.New(t, server, "portal", schemas)
	portal.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": c.password}, 200, "")
	portal.Call("GET", "/api/v1/admin/deployments/placement?model_id="+c.model, nil, 404, "Problem")
}
