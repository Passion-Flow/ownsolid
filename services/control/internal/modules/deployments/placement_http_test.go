package deployments_test

import (
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/google/uuid"
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

func TestRealTLSPlacementAndCreationRequireFreshCapacity(t *testing.T) {
	c := setup(t)
	ctx := t.Context()
	server := httpfixture.NewTLSServer(t, transport.New(c.s.Nodes.Identity))
	t.Cleanup(server.Close)
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/deployments/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	admin.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": c.password}, 200, "")
	age := time.Duration(c.s.Nodes.Identity.Config.Policy().Nodes.OnlineSeconds) * time.Second
	const path = "/api/v1/admin/deployments"
	input := c.input()
	for _, tc := range []struct {
		name                 string
		reportSkew, gpuSkew  time.Duration
		freeMiB              int64
		nodeRefusal, refusal string
	}{
		{name: "recent receipt with stale report", reportSkew: -2 * age, freeMiB: 8193, nodeRefusal: "INFERENCE_PREFLIGHT_REQUIRED"},
		{name: "fresh report with stale GPU", gpuSkew: -2 * age, freeMiB: 8193, refusal: "INFERENCE_PREFLIGHT_REQUIRED"},
		{name: "GPU sample beyond clock window", gpuSkew: 2 * age, freeMiB: 8193, refusal: "INFERENCE_PREFLIGHT_REQUIRED"},
		{name: "fractional free requirement rounded up", freeMiB: 6554, refusal: "INFERENCE_GPU_MEMORY_LOW"},
		{name: "exact ceiling", freeMiB: 6555},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC()
			handshake := c.h
			gpu := c.h.Inference.GPUs[0]
			gpu.MemoryTotalMiB = 8193
			gpu.MemoryFreeMiB = tc.freeMiB
			gpu.ObservedAt = now.Add(tc.gpuSkew)
			handshake.Facts.ObservedAt = now
			handshake.Inference = &wire.InferenceObservation{
				ObservedAt: now.Add(tc.reportSkew), GPUs: []wire.NVIDIAGPU{gpu},
			}
			if _, err := c.s.Nodes.Heartbeat(ctx, c.node.NodeID, c.node.Credential, handshake); err != nil {
				t.Fatal("record hardware report", err)
			}

			placement := admin.Call("GET", path+"/placement?model_id="+c.model, nil, 200, "PlacementResponse")
			node := placement["candidates"].([]any)[0].(map[string]any)
			device := node["gpus"].([]any)[0].(map[string]any)
			code := tc.refusal
			if tc.nodeRefusal != "" {
				code = tc.nodeRefusal
			}
			admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
			if code == "" {
				if node["refusal"] != nil || device["refusal"] != nil || placement["recommended"] == nil {
					t.Fatal("exact ceiling was refused", placement)
				}
				admin.Write("POST", path, input, 202, "DeploymentResponse")
				return
			}
			if placement["recommended"] != nil || refusalCode(node["refusal"]) != tc.nodeRefusal || refusalCode(device["refusal"]) != tc.refusal {
				t.Fatal("placement returned a different capacity decision", placement)
			}
			if got := admin.Write("POST", path, input, 409, "Problem"); got["code"] != code {
				t.Fatal("creation returned a different capacity decision", got)
			}
			var count int
			if err := c.s.Nodes.DB.QueryRow(ctx, "SELECT count(*) FROM deployments WHERE model_id=$1", c.model).Scan(&count); err != nil || count != 0 {
				t.Fatalf("capacity refusal wrote a deployment: %d, %v", count, err)
			}
		})
	}
}

func refusalCode(value any) string {
	code, _ := value.(string)
	return code
}
