package agentadmission

import (
	"errors"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// ShedLevel is how much agent traffic an operator has told control to refuse (ADR 0043). Each
// level refuses everything the level before it does, lowest priority first.
type ShedLevel int32

const (
	// ShedNone refuses nothing.
	ShedNone ShedLevel = iota
	// ShedBulk refuses transfers and other bulk calls.
	ShedBulk
	// ShedTelemetry also refuses telemetry.
	ShedTelemetry
	// ShedHeartbeats also refuses heartbeats and claims, leaving quarantine reports, so nodes
	// drop offline in control while their running work continues (ADR 0032).
	ShedHeartbeats
)

var shedLevelNames = [...]string{"none", "bulk", "telemetry", "heartbeats"}

func (l ShedLevel) String() string {
	if l < ShedNone || l > ShedHeartbeats {
		return "unknown"
	}
	return shedLevelNames[l]
}

// ParseShedLevel reads a level by its name.
func ParseShedLevel(name string) (ShedLevel, error) {
	for i, known := range shedLevelNames {
		if name == known {
			return ShedLevel(i), nil
		}
	}
	return ShedNone, errors.New("AGENT_SHED_LEVEL_INVALID")
}

// refuses reports whether the level sheds calls of priority p.
func (l ShedLevel) refuses(p wire.Priority) bool {
	switch p {
	case wire.PriorityBulk:
		return l >= ShedBulk
	case wire.PriorityTelemetry:
		return l >= ShedTelemetry
	case wire.PriorityHeartbeat:
		return l >= ShedHeartbeats
	default:
		return false
	}
}

// Shed returns the level in force in this process.
func (c *Controller) Shed() ShedLevel { return ShedLevel(c.shed.Load()) }

// SetShed changes the level at once and needs no database, so it works when the database is what
// is overloaded. Calls already holding a seat finish; the next one is refused.
func (c *Controller) SetShed(level ShedLevel) error {
	if level < ShedNone || level > ShedHeartbeats {
		return errors.New("AGENT_SHED_LEVEL_INVALID")
	}
	if previous := ShedLevel(c.shed.Swap(int32(level))); previous != level {
		c.logger.Warn("agent_shed_level_changed", "from", previous.String(), "to", level.String())
	}
	return nil
}
