package scope

import (
	"context"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

func cumulativeRegistryOwner(p CumulativeUpgradePlan) engine.Ownership {
	return engine.Ownership{ScopeID: p.ScopeID, OperationID: p.GenerationID, Role: engine.NativePostgreSQLRegistryRole}
}

func cumulativeRegistryReleaseOwner(p CumulativeUpgradePlan) engine.Ownership {
	return engine.Ownership{ScopeID: p.ScopeID, OperationID: p.GenerationID, Role: compose.MVPRegistryReleaseRole}
}

func cumulativeRegistryCompleted(events []Event) bool {
	s := cumulativeUpgradeState(events)
	for _, phase := range []string{"registry-volume-create", "registry-utility-create", "registry-payload-write", "registry-utility-remove", "registry-release-volume-create", "registry-release-utility-create", "registry-release-payload-write", "registry-release-utility-remove"} {
		r := cumulativePhaseRecord(s.completed, phase)
		if r.Phase != phase {
			return false
		}
	}
	return s.activated && s.pending == ""
}

// Current consumers use actual original Engine ownership, never a fabricated
// volumeState entry. Data is mutable after activation; the initializer is not.
func verifyCumulativeRegistry(ctx context.Context, c runtimeEngine, p CumulativeUpgradePlan, control string) error {
	owner := cumulativeRegistryOwner(p)
	if _, found, e := c.InspectVolume(ctx, owner); e != nil || !found {
		return failure("OFFLINE_SCOPE_MVP_REGISTRY_VOLUME_REQUIRED")
	}
	if e := c.VerifyVolumeConsumers(ctx, owner, []string{control}); e != nil {
		return e
	}
	owner = cumulativeRegistryReleaseOwner(p)
	if _, found, e := c.InspectVolume(ctx, owner); e != nil || !found {
		return failure("OFFLINE_SCOPE_MVP_REGISTRY_RELEASE_REQUIRED")
	}
	return c.VerifyVolumeConsumers(ctx, owner, []string{control})
}
