package v1

// These are v1 machine-protocol timing invariants, not installation settings. Changing
// them requires a compatible protocol/profile and updated contract tests. The heartbeat
// interval here is only the default; control paces agents at runtime through Pacing.
const (
	HeartbeatSeconds    = 10
	TaskLeaseSeconds    = 30
	LeaseRenewalSeconds = 8
)
