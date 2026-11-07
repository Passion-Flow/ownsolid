//go:build repository_integration

package deployments

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func (g deploymentRuntimeStory) placement(t *testing.T) Placement {
	t.Helper()
	p, e := g.s.Placement(g.ctx, g.admin, g.model)
	if e != nil {
		t.Fatal("actual placement", e)
	}
	return p
}

func placementNode(t *testing.T, p Placement, node string) PlacementNode {
	t.Helper()
	for _, n := range p.Candidates {
		if n.NodeID == node {
			return n
		}
	}
	t.Fatalf("node %s missing from %+v", node, p.Candidates)
	return PlacementNode{}
}

func placementRefusal(t *testing.T, got *string, want string) {
	t.Helper()
	if want == "" && got != nil || want != "" && (got == nil || *got != want) {
		t.Fatalf("refusal %v, wanted %q", got, want)
	}
}

// The preview judges with the deployment's own checks, so whatever it recommends a deployment
// accepts, and whatever it refuses a deployment refuses with the same code.
func TestActualPlacementAgreesWithDeployment(t *testing.T) {
	for _, name := range []string{"recommends_the_freest_eligible_gpu", "refuses_as_the_deployment_does", "stale_and_busy_nodes", "scope_and_permission"} {
		t.Run(name, func(t *testing.T) {
			g := deploymentRuntimeSetup(t)
			now := time.Now().UTC().Truncate(time.Second)
			switch name {
			case "recommends_the_freest_eligible_gpu":
				p := g.placement(t)
				if p.ModelID != g.model || p.WorkspaceID != g.f.Workspace || p.Recommended == nil || p.Recommended.NodeID != g.node.NodeID || p.Recommended.GPUUUID != g.gpu {
					t.Fatalf("single eligible GPU not recommended: %+v", p)
				}
				roomier := "GPU-" + uuid.NewString()
				h := g.handshake
				h.Inference = &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{{UUID: roomier, Name: "Synthetic larger GPU", DriverVersion: "580.2", MemoryTotalMiB: 49152, MemoryFreeMiB: 40000, ComputeCapability: 8.9, MIGMode: "Disabled", ObservedAt: now}}, ObservedAt: now}
				other := g.enroll(t, g.f.Workspace, h)
				p = g.placement(t)
				if len(p.Candidates) != 2 || p.Recommended == nil || p.Recommended.NodeID != other.NodeID || p.Recommended.GPUUUID != roomier {
					t.Fatalf("freest GPU not recommended: %+v", p)
				}
				input := g.input()
				input.NodeID, input.GPUUUID = p.Recommended.NodeID, p.Recommended.GPUUUID
				if _, e := g.s.Create(g.ctx, g.admin, input, uuid.NewString(), uuid.NewString()); e != nil {
					t.Fatal("recommendation refused by the deployment", e)
				}
				p = g.placement(t)
				placementRefusal(t, placementNode(t, p, other.NodeID).Refusal, "INFERENCE_RESOURCE_BUSY")
				if p.Recommended == nil || p.Recommended.NodeID != g.node.NodeID {
					t.Fatalf("remaining GPU not recommended: %+v", p)
				}
			case "refuses_as_the_deployment_does":
				h := g.handshake
				gpus := map[string]wire.NVIDIAGPU{
					"INFERENCE_DRIVER_UNSUPPORTED": {Name: "Old driver", DriverVersion: "535.1", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8, MIGMode: "Disabled"},
					"INFERENCE_GPU_UNSUPPORTED":    {Name: "Old card", DriverVersion: "580.1", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 7, MIGMode: "Disabled"},
					"INFERENCE_MIG_UNSUPPORTED":    {Name: "Partitioned", DriverVersion: "580.1", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8, MIGMode: "Enabled"},
					"INFERENCE_GPU_MEMORY_LOW":     {Name: "Small", DriverVersion: "580.1", MemoryTotalMiB: 4096, MemoryFreeMiB: 4096, ComputeCapability: 8, MIGMode: "Disabled"},
				}
				observation := &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{}, ObservedAt: now}
				uuids := map[string]string{}
				for code, gpu := range gpus {
					gpu.UUID = "GPU-" + uuid.NewString()
					gpu.ObservedAt = now
					uuids[code] = gpu.UUID
					observation.GPUs = append(observation.GPUs, gpu)
				}
				h.Inference = observation
				other := g.enroll(t, g.f.Workspace, h)
				n := placementNode(t, g.placement(t), other.NodeID)
				placementRefusal(t, n.Refusal, "")
				for _, gpu := range n.GPUs {
					for code, id := range uuids {
						if gpu.UUID != id {
							continue
						}
						placementRefusal(t, gpu.Refusal, code)
						input := g.input()
						input.NodeID, input.GPUUUID = other.NodeID, id
						_, e := g.s.Create(g.ctx, g.admin, input, uuid.NewString(), uuid.NewString())
						deploymentRuntimeCode(t, e, code)
					}
				}
				h = g.handshake
				h.Facts.MemoryTotalBytes = 4 << 30
				h.Inference = &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{{UUID: "GPU-" + uuid.NewString(), Name: "Fine card", DriverVersion: "580.1", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8, MIGMode: "Disabled", ObservedAt: now}}, ObservedAt: now}
				small := g.enroll(t, g.f.Workspace, h)
				n = placementNode(t, g.placement(t), small.NodeID)
				placementRefusal(t, n.Refusal, "INFERENCE_HOST_MEMORY_LOW")
				if len(n.GPUs) != 1 || n.GPUs[0].Refusal != nil {
					t.Fatalf("a node's refusal must not be repeated on its GPUs: %+v", n)
				}
			case "stale_and_busy_nodes":
				g.exec(t, "UPDATE inference_nodes SET received_at=clock_timestamp()-interval '1 day' WHERE node_id=$1", "UPDATE inference_nodes SET received_at=TIMESTAMPADD(DAY,-1,UTC_TIMESTAMP(6)) WHERE node_id=?", g.node.NodeID)
				p := g.placement(t)
				placementRefusal(t, placementNode(t, p, g.node.NodeID).Refusal, "INFERENCE_PREFLIGHT_REQUIRED")
				if p.Recommended != nil {
					t.Fatal("stale GPUs recommended")
				}
				_, e := g.s.Create(g.ctx, g.admin, g.input(), uuid.NewString(), uuid.NewString())
				deploymentRuntimeCode(t, e, "INFERENCE_PREFLIGHT_REQUIRED")
				g.heartbeat(t, g.node, g.handshake)
				g.create(t)
				p = g.placement(t)
				placementRefusal(t, placementNode(t, p, g.node.NodeID).Refusal, "INFERENCE_RESOURCE_BUSY")
				if p.Recommended != nil {
					t.Fatal("busy node recommended")
				}
			case "scope_and_permission":
				if _, e := g.s.Placement(g.ctx, g.portal, g.model); e == nil || e.Error() != "ACCESS_DENIED" {
					t.Fatal("portal placement", e)
				}
				if _, e := g.s.Placement(g.ctx, g.admin, "not-a-model"); e == nil || e.Error() != "VALIDATION_FAILED" {
					t.Fatal("invalid model", e)
				}
				if _, e := g.s.Placement(g.ctx, g.admin, uuid.NewString()); e == nil || e.Error() != "RESOURCE_NOT_FOUND" {
					t.Fatal("missing model", e)
				}
				other := uuid.NewString()
				g.exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other synthetic Workspace','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Other synthetic Workspace','active')", other, g.f.Tenant)
				g.exec(t, "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES($1,$2,$3,'owner')", "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES(?,?,?,'owner')", g.f.Tenant, other, g.f.User)
				foreign := g.enroll(t, other, g.handshake)
				p := g.placement(t)
				for _, n := range p.Candidates {
					if n.NodeID == foreign.NodeID {
						t.Fatal("a node of another workspace was offered")
					}
				}
				model := uuid.NewString()
				manifest, _ := json.Marshal(wire.ApprovedModel())
				g.exec(t, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Other model',$4,$5,$6,'ready',$7)`, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES(?,?,?,'Other model',?,?,CAST(? AS JSON),'ready',?)`, model, g.f.Tenant, other, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), manifest, g.f.User)
				theirs, e := g.s.Placement(g.ctx, g.admin, model)
				if e != nil || theirs.WorkspaceID != other || len(theirs.Candidates) != 1 || theirs.Candidates[0].NodeID != foreign.NodeID {
					t.Fatalf("placement left the model's workspace: %+v %v", theirs, e)
				}
			}
		})
	}
}
