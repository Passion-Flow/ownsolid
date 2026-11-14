//go:build !linux

package inference

import "testing"

func TestEgressGuardRefusesOutsideLinux(t *testing.T) {
	guard := newEgressGuard()
	if e := guard.Ensure("br-0123456789ab"); e == nil || e.Error() != egressGuardUnavailable {
		t.Fatal("workload network guarded on an unsupported platform", e)
	}
	if e := guard.Remove("br-0123456789ab"); e == nil || e.Error() != egressGuardUnavailable {
		t.Fatal(e)
	}
}
