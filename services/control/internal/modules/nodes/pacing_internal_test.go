package nodes

import (
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestPacingCacheExpires(t *testing.T) {
	var c pacingCache
	now := time.Now()
	if got, fresh := c.read(now); fresh || got != wire.DefaultPacing() {
		t.Fatalf("before the first load %+v fresh %v", got, fresh)
	}
	slow := wire.Pacing{HeartbeatSeconds: 20}
	c.put(slow, now)
	if got, fresh := c.read(now.Add(PacingRefreshInterval - time.Nanosecond)); !fresh || got != slow {
		t.Fatalf("inside the interval %+v fresh %v", got, fresh)
	}
	if got, fresh := c.read(now.Add(PacingRefreshInterval)); fresh || got != slow {
		t.Fatalf("after the interval %+v fresh %v", got, fresh)
	}
}
