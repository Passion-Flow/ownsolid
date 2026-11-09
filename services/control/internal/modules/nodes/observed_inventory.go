package nodes

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// ObservedInventory combines server-owned node identity with independently timed observations.
type ObservedInventory struct {
	NodeID      string
	TenantID    string
	WorkspaceID string
	SiteID      string
	CellID      string
	Host        *ObservedHost
	GPUs        *ObservedGPUs
}

// ObservedHost is the latest stored valid v1 host and filesystem sample.
type ObservedHost struct {
	OSID             string
	OSVersion        string
	Architecture     string
	Hostname         string
	LogicalCPUs      int
	MemoryTotalBytes int64
	AgentVersion     string
	ObservedAt       time.Time
	Filesystem       ObservedFilesystem
}

// ObservedFilesystem is a sample at the Agent's configured path. A nil available count is unknown.
type ObservedFilesystem struct {
	Path           string
	TotalBytes     int64
	AvailableBytes *int64
}

// ObservedGPUs is a recent report. A present report with no devices is a measured empty list.
type ObservedGPUs struct {
	ObservedAt time.Time
	Devices    []ObservedGPU
}

// ObservedGPU describes a reported NVIDIA device within this node.
type ObservedGPU struct {
	UUID              string
	Name              string
	DriverVersion     string
	MemoryTotalMiB    int64
	ComputeCapability float64
	MIGMode           string
	ObservedAt        time.Time
}

// ObservedInventory reads one scoped node with the same session and workspace fences as Get.
// A nil observation denotes unknown hardware. An unsuccessful transaction returns no inventory.
func (s *Service) ObservedInventory(ctx context.Context, a identity.Auth, workspace, id string) (ObservedInventory, error) {
	var out ObservedInventory
	err := s.readActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		w, err := s.nodeReadScopeWithin(ctx, q, a, workspace, id)
		if err != nil {
			return err
		}
		var storedFacts json.RawMessage
		n, err := scanNodeFacts(s.lookupNodeRow(ctx, q, w.TenantID, w.ID, id), &storedFacts)
		if err != nil {
			return err
		}
		gpus, err := s.observedGPUsWithin(ctx, q, n)
		if err != nil {
			return err
		}
		out = ObservedInventory{
			NodeID: n.ID, TenantID: n.TenantID, WorkspaceID: n.WorkspaceID, SiteID: n.SiteID, CellID: n.CellID,
			Host: observedHost(n.Facts, storedFacts), GPUs: gpus,
		}
		return nil
	})
	if err != nil {
		return ObservedInventory{}, err
	}
	return out, nil
}

func observedHost(facts wire.Facts, storedFacts json.RawMessage) *ObservedHost {
	if facts.Validate() != nil {
		return nil
	}
	var optional struct {
		AvailableBytes *int64 `json:"disk_available_bytes"`
	}
	if json.Unmarshal(storedFacts, &optional) != nil {
		return nil
	}
	return &ObservedHost{
		OSID: facts.OSID, OSVersion: facts.OSVersion, Architecture: facts.Architecture, Hostname: facts.Hostname,
		LogicalCPUs: facts.LogicalCPUs, MemoryTotalBytes: facts.MemoryTotalBytes, AgentVersion: facts.AgentVersion,
		ObservedAt: facts.ObservedAt.UTC(),
		Filesystem: ObservedFilesystem{Path: facts.DiskPath, TotalBytes: facts.DiskTotalBytes, AvailableBytes: optional.AvailableBytes},
	}
}

func (s *Service) observedGPUsWithin(ctx context.Context, q transaction.Executor, n Node) (*ObservedGPUs, error) {
	owner, ok := s.Inference.(interface {
		HardwareWithin(context.Context, transaction.Executor, Node) (wire.InferenceObservation, error)
	})
	if !ok || !wire.IsInferenceProfile(n.ExecutionProfile) {
		return nil, nil
	}
	observation, err := owner.HardwareWithin(ctx, q, n)
	if err != nil {
		// The hardware owner uses this code for a missing or stale report.
		if err.Error() == "TRAINING_PREFLIGHT_REQUIRED" {
			return nil, nil
		}
		return nil, err
	}
	if observation.ProbeErrorCode != "" && len(observation.GPUs) == 0 {
		return nil, nil
	}
	out := &ObservedGPUs{ObservedAt: observation.ObservedAt.UTC(), Devices: make([]ObservedGPU, 0, len(observation.GPUs))}
	for _, gpu := range observation.GPUs {
		out.Devices = append(out.Devices, ObservedGPU{
			UUID: gpu.UUID, Name: gpu.Name, DriverVersion: gpu.DriverVersion, MemoryTotalMiB: gpu.MemoryTotalMiB,
			ComputeCapability: gpu.ComputeCapability, MIGMode: gpu.MIGMode, ObservedAt: gpu.ObservedAt.UTC(),
		})
	}
	slices.SortFunc(out.Devices, func(a, b ObservedGPU) int { return strings.Compare(a.UUID, b.UUID) })
	return out, nil
}
