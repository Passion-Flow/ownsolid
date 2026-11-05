//go:build !linux

package inference

// unavailableGuard refuses every request: the inference profile is Linux only, and a
// host without the guard must not start a workload.
type unavailableGuard struct{}

func newEgressGuard() egressGuard { return unavailableGuard{} }

func (unavailableGuard) Ensure(string) error { return fail(egressGuardUnavailable) }

func (unavailableGuard) Remove(string) error { return fail(egressGuardUnavailable) }
