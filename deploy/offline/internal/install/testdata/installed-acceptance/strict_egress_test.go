package e2e

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/tests/e2e/egress"
)

// probePrefix is TEST-NET-1 (RFC 5737). The harness sends its own probes there to prove
// the recorder sees traffic; attempts to any other address fail the run.
var probePrefix = netip.MustParsePrefix("192.0.2.0/24")

// TestMain fails the run when the software under test tried to reach anything off the
// host, whichever test caused it.
func TestMain(m *testing.M) {
	code := m.Run()
	if path := os.Getenv("OWNSOLID_E2E_EGRESS_LOG"); path != "" {
		if err := verifyNoEgress(path); err != nil {
			fmt.Fprintln(os.Stderr, "FAIL: zero egress:", err)
			code = 1
		}
	}
	os.Exit(code)
}

func verifyNoEgress(path string) error {
	attempts, err := egress.ReadLog(path)
	if err != nil {
		return err
	}
	expected := map[string]int{
		"tcp|192.0.2.10:9|":                  2,
		"tcp|[2001:db8::10]:9|":              1,
		"udp|192.0.2.53:53|probe.invalid. A": 2,
	}
	if len(attempts) != 5 {
		return fmt.Errorf("recorder contains %d packets; require exactly five independently exercised negative controls", len(attempts))
	}
	for _, attempt := range attempts {
		key := attempt.Protocol + "|" + attempt.Destination.String() + "|" + attempt.DNSQuestion
		count, allowed := expected[key]
		if !allowed || count == 0 {
			return fmt.Errorf("unexpected outbound packet; inspect the protected capture")
		}
		expected[key] = count - 1
	}
	for _, count := range expected {
		if count != 0 {
			return fmt.Errorf("a required negative control is absent")
		}
	}
	fmt.Println("zero egress: exactly the five host and workload IPv4/IPv6/DNS negative-control packets")
	return nil
}

// egressWatch is the recorder side of the environment.
type egressWatch struct {
	log, iface string
}

// expectRecorded waits until the recorder has logged an attempt to destination.
func (w egressWatch) expectRecorded(t *testing.T, destination netip.AddrPort) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		attempts, err := egress.ReadLog(w.log)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range attempts {
			if a.Destination == destination {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the egress recorder did not see the probe to %s; recorded: %v", destination, attempts)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// assertIsolated proves the host has no interface but loopback, Docker's local bridges
// and the recorder, that the default route leads to the recorder, and that the recorder
// sees and rejects a connection attempt at once.
func (w egressWatch) assertIsolated(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name != "lo" && name != w.iface && name != "docker0" && !strings.HasPrefix(name, "br-") && !strings.HasPrefix(name, "veth") {
			t.Fatalf("interface %s could lead off the host", name)
		}
	}
	raw, err := os.ReadFile("/proc/net/route")
	if err != nil {
		t.Fatal(err)
	}
	defaults := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if fields := strings.Fields(line); len(fields) > 1 && fields[1] == "00000000" {
			if fields[0] != w.iface {
				t.Fatalf("a default route bypasses the recorder: %s", line)
			}
			defaults++
		}
	}
	if defaults != 1 {
		t.Fatalf("%d default routes to the recorder, want 1", defaults)
	}
	probe := netip.MustParseAddrPort("192.0.2.1:9")
	started := time.Now()
	conn, err := net.DialTimeout("tcp", probe.String(), 5*time.Second)
	if err == nil {
		conn.Close()
		t.Fatalf("connected to %s", probe)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("the attempt was not rejected promptly (%s): %v", elapsed, err)
	}
	w.expectRecorded(t, probe)
}
