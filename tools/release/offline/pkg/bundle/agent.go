package bundle

import (
	"context"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/internal/factory"
)

// AgentProfile identifies the installable Linux amd64 host Agent inventory.
const AgentProfile = factory.AgentProfile

const (
	// AgentBinaryAsset is the signed Agent executable.
	AgentBinaryAsset = factory.AgentBinaryAsset
	// AgentUnitAsset is the signed systemd service unit.
	AgentUnitAsset = factory.AgentUnitAsset
)

// InspectAgent verifies every signed Agent payload against independent release trust.
func InspectAgent(ctx context.Context, p Verification, source string) (Result, error) {
	c, err := p.config()
	if err != nil {
		return Result{}, err
	}
	return factory.Inspect(ctx, factory.WithAgentProfile(c), source)
}

// UnpackAgent verifies and prepares the dedicated Agent inventory using independent release trust.
func UnpackAgent(ctx context.Context, p Verification, source, destination string) (Result, error) {
	c, err := p.config()
	if err != nil {
		return Result{}, err
	}
	return factory.Unpack(ctx, factory.WithAgentProfile(c), source, destination, "", "")
}
