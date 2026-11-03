//go:build repository_integration && registry_probe_integration

package deployments

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Real PostgreSQL, signed synthetic License, sessions/enrollment/heartbeats and
// persisted preflight reads. GPU/model metadata is synthetic; no image/Registry,
// accepted model bytes, Docker, current product HTTP or NVIDIA qualification.
func TestActualRegistryProbePersistence(t *testing.T) {
	g := deploymentRuntimeSetup(t)
	if g.f.PostgreSQL == nil {
		t.Fatal("REGISTRY_PROBE_ACTUAL_POSTGRESQL_REQUIRED")
	}
	before := map[string]int{}
	for _, table := range []string{"deployments", "node_tasks", "gpu_leases"} {
		before[table] = g.count(t, table)
	}
	for _, sample := range []struct {
		name, probe, want string
		pin, capability   bool
	}{
		{"ordinary_present", "", "", false, false},
		{"pinned_loader_disabled_present", "", "AGENT_PROFILE_REJECTED", true, false},
		{"ordinary_missing", wire.InferenceImageMissing, wire.InferenceImageMissing, false, true},
		{"pinned_missing_loader_disabled", wire.InferenceImageMissing, "AGENT_PROFILE_REJECTED", true, false},
		{"pinned_missing_allowed", wire.InferenceImageMissing, "", true, true},
		{"pinned_engine_failure", "INFERENCE_IMAGE_UNAVAILABLE", "INFERENCE_IMAGE_UNAVAILABLE", true, true},
		{"pinned_wrong_image", "INFERENCE_IMAGE_INVALID", "INFERENCE_IMAGE_INVALID", true, true},
		{"pinned_GPU_memory_low", wire.InferenceImageMissing, "INFERENCE_GPU_MEMORY_LOW", true, true},
		{"pinned_driver_low", wire.InferenceImageMissing, "INFERENCE_DRIVER_UNSUPPORTED", true, true},
		{"pinned_host_memory_low", wire.InferenceImageMissing, "INFERENCE_HOST_MEMORY_LOW", true, true},
		{"pinned_wrong_OS", wire.InferenceImageMissing, "INFERENCE_PLATFORM_UNSUPPORTED", true, true},
		{"pinned_missing_GPU", wire.InferenceImageMissing, "INFERENCE_GPU_UNAVAILABLE", true, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			h := g.handshake
			observation := *h.Inference
			observation.GPUs = append([]wire.NVIDIAGPU{}, h.Inference.GPUs...)
			h.Inference = &observation
			now := time.Now().UTC()
			h.Facts.ObservedAt = now
			observation.ObservedAt = now
			observation.ProbeErrorCode = sample.probe
			observation.RegistryImageDelivery = wire.RegistryDeliveryCapability(sample.capability)
			for i := range observation.GPUs {
				observation.GPUs[i].ObservedAt = now
			}
			recipe := wire.ApprovedInferenceRecipe()
			switch sample.name {
			case "pinned_GPU_memory_low":
				observation.GPUs[0].MemoryTotalMiB = recipe.MinimumGPUMemoryMiB - 1
				observation.GPUs[0].MemoryFreeMiB = 0
			case "pinned_driver_low":
				observation.GPUs[0].DriverVersion = strconv.Itoa(recipe.MinimumDriverMajor-1) + ".0"
			case "pinned_host_memory_low":
				h.Facts.MemoryTotalBytes = recipe.MinimumHostMemoryBytes - 1
			case "pinned_wrong_OS":
				h.Facts.OSID = "debian"
			case "pinned_missing_GPU":
				observation.GPUs = []wire.NVIDIAGPU{}
			}
			g.heartbeat(t, g.node, h)
			node, e := g.s.Nodes.Get(g.ctx, g.admin, "", g.node.NodeID)
			if e != nil {
				t.Fatal(e)
			}
			command := wire.InferenceCommand{Action: "deploy", DeploymentID: uuid.NewString(), ModelID: g.model, CatalogDigest: wire.ModelCatalogDigest(), GPUUUID: g.gpu, Revision: 1}
			if sample.pin {
				command.RegistryPinID = uuid.NewString()
			}
			e = g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
				return g.s.preflightCommandWithin(ctx, q, node, command)
			})
			if sample.want == "" {
				if e != nil {
					t.Fatal("actual persisted preflight refused", e)
				}
			} else if e == nil || e.Error() != sample.want {
				t.Fatal("persisted preflight authority changed", e)
			}
			for table, count := range before {
				if g.count(t, table) != count {
					t.Fatal("read-only preflight created business effects", table)
				}
			}
		})
	}
}
