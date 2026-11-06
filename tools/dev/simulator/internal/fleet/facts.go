package fleet

import (
	"fmt"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// The machine every simulated node reports: a large GPU server's host facts. Only the
// host name differs between nodes.
const (
	simulatedOS          = "ubuntu"
	simulatedOSVersion   = "24.04"
	simulatedCPUs        = 128
	simulatedMemoryBytes = 2 << 40
	simulatedDiskBytes   = 8 << 40
	simulatedDiskFree    = 6 << 40
)

// hostname names the simulated node with index.
func hostname(index int) string { return fmt.Sprintf("sim-%06d", index) }

// facts are the facts of the node named host, observed now.
func facts(host string) wire.Facts {
	return wire.Facts{OSID: simulatedOS, OSVersion: simulatedOSVersion, Architecture: "amd64", Hostname: host,
		LogicalCPUs: simulatedCPUs, MemoryTotalBytes: simulatedMemoryBytes, DiskPath: "/",
		DiskTotalBytes: simulatedDiskBytes, DiskAvailableBytes: simulatedDiskFree,
		ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}
}

// handshake is the heartbeat of a read-only node named host.
func handshake(host string) wire.Handshake {
	return wire.Handshake{ProtocolVersion: wire.ProfileVersion(wire.ReadOnlyProfile), Capabilities: wire.Capabilities(wire.ReadOnlyProfile), Facts: facts(host)}
}

// progress is what collecting facts reports, as the agent's discovery does: three steps
// with a line each.
func progress(f wire.Facts) []wire.ProgressEntry {
	now := time.Now().UTC()
	entries := []wire.ProgressEntry{
		{Kind: wire.ProgressKindStep, Step: wire.ProgressStepReadSystem, State: wire.ProgressStepRunning},
		{Kind: wire.ProgressKindLog, Level: wire.ProgressLevelInfo, Message: fmt.Sprintf("Operating system: %s %s", f.OSID, f.OSVersion)},
		{Kind: wire.ProgressKindStep, Step: wire.ProgressStepReadSystem, State: wire.ProgressStepSucceeded},
		{Kind: wire.ProgressKindStep, Step: wire.ProgressStepReadMemory, State: wire.ProgressStepRunning},
		{Kind: wire.ProgressKindLog, Level: wire.ProgressLevelInfo, Message: fmt.Sprintf("Memory: %d bytes", f.MemoryTotalBytes)},
		{Kind: wire.ProgressKindStep, Step: wire.ProgressStepReadMemory, State: wire.ProgressStepSucceeded},
		{Kind: wire.ProgressKindStep, Step: wire.ProgressStepReadDisk, State: wire.ProgressStepRunning},
		{Kind: wire.ProgressKindStep, Step: wire.ProgressStepReadDisk, State: wire.ProgressStepSucceeded},
	}
	for i := range entries {
		entries[i].Sequence, entries[i].At = i+1, now
	}
	return entries
}
