package v1

// These are negotiated v1 machine-protocol timing invariants, not installation settings.
// Changing them requires a compatible protocol/profile and updated contract tests.
const (
	HeartbeatSeconds    = 10
	TaskLeaseSeconds    = 30
	LeaseRenewalSeconds = 8
)
