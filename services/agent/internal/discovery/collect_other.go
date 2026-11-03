//go:build !linux

package discovery

import (
	"errors"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func Collect(string) (wire.Facts, error) { return wire.Facts{}, errors.New("AGENT_OS_UNSUPPORTED") }
