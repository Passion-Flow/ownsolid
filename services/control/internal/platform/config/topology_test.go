package config

import (
	"os"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/pkg/capabilities"
)

func topologyFixture(t *testing.T) string {
	t.Helper()
	raw, e := os.ReadFile("../../../config/examples/control/control.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	_, rest, ok := strings.Cut(string(raw), "size: single-node\n")
	if !ok {
		t.Fatal("example does not document topology")
	}
	return rest
}

func TestTopologyDefaultsAndExplicitSingleNode(t *testing.T) {
	body := topologyFixture(t)
	for _, prefix := range []string{"", "topology: {}\n", "topology:\n  size: single-node\n"} {
		c, e := Decode([]byte(prefix + body))
		if e != nil || c.Topology.EffectiveSize() != TopologySingleNode {
			t.Fatal(prefix, c.Topology, e)
		}
	}
}

func TestTopologySizesAreDeclared(t *testing.T) {
	sizes, found := capabilities.Lookup(capabilities.InstallSizes)
	if !found || len(sizes.Options) != len(TopologySizes) {
		t.Fatal("the topology model and the declaration differ", sizes.Options)
	}
	for i, size := range TopologySizes {
		if sizes.Options[i].ID != string(size) {
			t.Fatal("the declaration lists sizes in another order", i, size)
		}
	}
}

func TestTopologyRefusesUnsupportedUnknownAndMalformedValues(t *testing.T) {
	body := topologyFixture(t)
	for _, size := range []TopologySize{TopologyHA, TopologyCell, TopologyMultiCell, TopologyMultiSite} {
		if !size.Known() || size.Supported() {
			t.Fatal("size classification", size)
		}
		if _, e := Decode([]byte("topology:\n  size: " + string(size) + "\n" + body)); e == nil || e.Error() != "CONFIG_TOPOLOGY_SIZE_NOT_SUPPORTED" {
			t.Fatal(size, e)
		}
	}
	for prefix, code := range map[string]string{
		"topology:\n  size: huge\n": "CONFIG_TOPOLOGY_SIZE_INVALID",
		"topology:\n  size: 3\n":    "CONFIG_INVALID",
		"topology:\n  size: \"\"\n": "CONFIG_INVALID",
		"topology:\n  cells: 2\n":   "CONFIG_INVALID",
		"topology:\n":               "CONFIG_TOPOLOGY_INVALID",
		"topology: single-node\n":   "CONFIG_TOPOLOGY_INVALID",
	} {
		if _, e := Decode([]byte(prefix + body)); e == nil || e.Error() != code {
			t.Fatal(prefix, e)
		}
	}
}

func TestRemovedApplicationProfileIsRefused(t *testing.T) {
	body := topologyFixture(t)
	changed := strings.Replace(body, "server:\n", "server:\n  application_profile: platform\n", 1)
	if changed == body {
		t.Fatal("fixture has no server group")
	}
	if _, e := Decode([]byte(changed)); e == nil {
		t.Fatal("removed profile selector accepted")
	}
}

func TestValidateServingRequiresTransportAndBudgetConsistency(t *testing.T) {
	c, e := Decode([]byte(topologyFixture(t)))
	if e != nil {
		t.Fatal(e)
	}
	if e = c.ValidateServing(); e != nil {
		t.Fatal(e)
	}
	plaintext := c
	plaintext.Server.TransportMode = TransportHTTP
	plaintext.MachineServer = nil
	if e = plaintext.ValidateServing(); e == nil || e.Error() != "CONFIG_NODES_MACHINE_HTTPS_REQUIRED" {
		t.Fatal(e)
	}
	policy := c.Policy()
	policy.HTTP.ShutdownSeconds = policy.Portal.CommitSeconds
	short := c
	short.Runtime = &policy
	if e = short.ValidateServing(); e == nil || e.Error() != "CONFIG_PORTAL_HTTP_BUDGET_INVALID" {
		t.Fatal(e)
	}
}
