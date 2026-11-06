//go:build !linux

package discovery

import (
	"context"
	"errors"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func Collect(string) (wire.Facts, error) { return wire.Facts{}, errors.New("AGENT_OS_UNSUPPORTED") }

// CollectReporting reads this node's facts, which only Linux supports.
func CollectReporting(context.Context, string) (wire.Facts, error) {
	return wire.Facts{}, errors.New("AGENT_OS_UNSUPPORTED")
}
