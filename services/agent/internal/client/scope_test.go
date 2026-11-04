package client

import (
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func localScopeForTest() wire.Scope { return wire.LocalScope() }

// onWire is the path a control plane sees for a cell-relative agent path.
func onWire(path string) string {
	return strings.Replace(path, "/agent/v1/nodes/", "/agent/v1/sites/default/cells/default/nodes/", 1)
}

func TestTargetAddsTheCellOnlyToNodePaths(t *testing.T) {
	c := &Client{Origin: "https://control.test", Scope: wire.Scope{SiteID: "s1", CellID: "c1"}}
	if got := c.target("/agent/v1/nodes/n/heartbeat"); got != "https://control.test/agent/v1/sites/s1/cells/c1/nodes/n/heartbeat" {
		t.Fatal(got)
	}
	if got := c.target("/agent/v1/enroll"); got != "https://control.test/agent/v1/enroll" {
		t.Fatal(got)
	}
}
