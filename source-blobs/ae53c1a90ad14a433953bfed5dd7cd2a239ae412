package e2e

import (
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/tests/e2e/egress"
)

// Destinations of the workload egress probes, both in TEST-NET-1 so that TestMain treats
// them as harness probes and reports anything else a container sends as egress.
var (
	controlProbe = netip.MustParseAddrPort("192.0.2.10:9")
	guardedProbe = netip.MustParseAddrPort("192.0.2.20:9")
)

// TestWorkloadEgressDenied runs the agent's egress guard test on this isolated Docker
// host. A probe on an unguarded bridge proves that a container's connection does leave
// the host and reaches the recorder; the same probe on the agent's guarded inference
// network, which also tries a host service and a DNS lookup, must leave no trace. Any
// other packet, such as the guarded lookup reaching a resolver, fails the run in TestMain.
func TestWorkloadEgressDenied(t *testing.T) {
	e := loadEnvironment(t)
	binary := os.Getenv("OWNSOLID_E2E_INFERENCE_EGRESS_TEST")
	if binary == "" {
		t.Fatal("OWNSOLID_E2E_INFERENCE_EGRESS_TEST must name the agent's egress guard test binary")
	}
	if _, err := run(t, "docker", "image", "inspect", e.controlImage); err != nil {
		if output, err := run(t, "docker", "load", "--input", e.archive); err != nil {
			t.Fatalf("load images: %v %s", err, output)
		}
	}

	output, err := runWithEnvironment(t, []string{
		"PROJECT_ONE_DOCKER_SOCKET=/var/run/docker.sock",
		"PROJECT_ONE_EGRESS_PROBE_IMAGE=" + e.controlImage,
		"PROJECT_ONE_EGRESS_CONTROL_TARGET=" + controlProbe.String(),
		"PROJECT_ONE_EGRESS_GUARDED_TARGET=" + guardedProbe.String(),
	}, binary, "-test.v", "-test.run=^TestActualEgressGuard")
	t.Log(output)
	if err != nil {
		t.Fatalf("agent egress guard test: %v", err)
	}

	e.egress.expectRecorded(t, controlProbe)
	attempts, err := egress.ReadLog(e.egress.log)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range attempts {
		if a.Destination == guardedProbe || strings.Contains(a.DNSQuestion, "egress-guard-probe") {
			t.Errorf("the guarded workload reached the network: %s", a)
		}
	}
}
