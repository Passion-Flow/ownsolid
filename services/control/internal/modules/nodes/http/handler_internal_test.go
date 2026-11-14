package http

import (
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestRackNetworkIsTheSubnetOfTheAddress(t *testing.T) {
	policy := config.RatesPolicy{AgentEnrollRackIPv4Prefix: 24, AgentEnrollRackIPv6Prefix: 64}
	for address, want := range map[string]string{
		"10.1.2.3":             "10.1.2.0/24",
		"10.1.2.200":           "10.1.2.0/24",
		"10.1.3.3":             "10.1.3.0/24",
		"::ffff:10.1.2.3":      "10.1.2.0/24",
		"2001:db8:1:2:3:4:5:6": "2001:db8:1:2::/64",
	} {
		if got, known := rackNetwork(address, policy); !known || got != want {
			t.Errorf("%s: got %s %v, want %s", address, got, known, want)
		}
	}
	if _, known := rackNetwork("not an address", policy); known {
		t.Error("an address that does not parse keys no limit")
	}
	if _, known := rackNetwork("", policy); known {
		t.Error("an empty address keys no limit")
	}
}
