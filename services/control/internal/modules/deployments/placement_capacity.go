package deployments

import (
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// gpuObservationWindow bounds age and clock difference for the source sample.
// The database separately bounds the age of the report receipt.
type gpuObservationWindow struct {
	earliest time.Time
	latest   time.Time
}

func (s *Service) gpuObservationWindow() gpuObservationWindow {
	now := time.Now().UTC()
	age := config.Seconds(s.Nodes.Identity.Config.Policy().Nodes.OnlineSeconds)
	return gpuObservationWindow{earliest: now.Add(-age), latest: now.Add(age)}
}

func (window gpuObservationWindow) contains(observed time.Time) bool {
	return !observed.Before(window.earliest) && !observed.After(window.latest)
}

// requiredServingFreeMiB rounds the recipe's free share up to the next
// whole reported MiB. Derived loads also have an absolute free-memory floor.
func requiredServingFreeMiB(totalMiB int64, derived bool, derivedMinimumMiB int64) int64 {
	const percentDenominator = 100
	required := (totalMiB*wire.InferenceFreeMemoryPercent + percentDenominator - 1) / percentDenominator
	if derived {
		return max(required, derivedMinimumMiB)
	}
	return required
}
