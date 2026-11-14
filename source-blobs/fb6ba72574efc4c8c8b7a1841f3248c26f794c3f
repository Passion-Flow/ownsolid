package runtime

import (
	"context"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// The production component manager owns all journal and physical effects. This
// interface keeps recovery and observation on the same instance across cycles.
type componentLifecycle interface {
	Recover(context.Context) error
	Observe(context.Context) wire.ComponentObservation
	Execute(context.Context, wire.ComponentCommand, func(context.Context, string) error) (wire.ComponentObservation, error)
	ExecuteMaintenance(context.Context, string, wire.ComponentCommand, func(context.Context, string) error) (wire.ComponentObservation, *wire.ComponentLogs, error)
}

func recoverComponent(ctx context.Context, c Config, manager componentLifecycle) error {
	if manager == nil {
		return nil
	}
	bounded, stop := context.WithTimeout(ctx, settings.Seconds(c.Policy().ComponentTaskSeconds))
	defer stop()
	if e := bounded.Err(); e != nil {
		return e
	}
	if e := manager.Recover(bounded); e != nil {
		return e
	}
	return bounded.Err()
}
