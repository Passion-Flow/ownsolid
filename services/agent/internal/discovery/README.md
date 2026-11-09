# Linux node fact collection

[collect_linux.go](collect_linux.go) collects the host facts consumed by enrollment and agent preflight. [parse.go](parse.go) contains bounded parsing of observed system data. The disk path is an explicit caller input; reporting capacity does not authorize allocating it or establish a sustained workload budget.

[collect_other.go](collect_other.go) deliberately returns AGENT_OS_UNSUPPORTED outside Linux. Do not replace missing observations with fabricated Linux facts to make a development-host test pass. Target architecture, OS, memory, CPU and device observations belong to the actual machine named by the profile.

This package has no persistent business state, credentials or control-plane permissions. Runtime/managers consume its facts, and [agent contracts](../../../../contracts/agent) define the wire representation. When changing a field or parser, synchronize handshake/preflight consumers and invalid/missing/overflow cases.

Focused native check: `go -C services/agent test ./internal/discovery`. Parser tests qualify the supplied fixtures only; actual host collection and GPU/workload support require their distinct target checks.

## NVIDIA DCGM readings

`CollectDCGM` reads DCGM Exporter 4.9.0 with DCGM 4.7.0 on a numeric loopback
address supplied by the caller. Use the physical GPU device selector and
disable workload label enrichment. Start with the pinned default metrics
and enable the two volatile ECC totals and the NVLink recovery counters
from the collector list where available. The snapshot carries physical GPU
UUIDs, available PCI addresses and canonical DCGM field names. A missing sample means the exporter
did not provide a usable reading. See [GPU telemetry research](../../../../docs/research/gpu-telemetry.md)
for source versions, units and deployment gates.

## NVIDIA driver faults

`ReadNVIDIAJournal` reads a bounded page of local `journalctl --kernel
--output=json` output for a caller-supplied current boot ID. It checks kernel
provenance and returns PCI addresses, Xid numbers and replay cursors.
It accepts text and single byte-array messages and excludes events with the
NVIDIA simulation marker and ambiguous multi-value messages.
Persist the cursor only after accepted delivery. A PCI address must resolve
uniquely to the physical GPU in same-boot inventory before an action can
target it. `AdviceForNVIDIAXID` and `AdviceForDCGMIncident` return operator
guidance. See [NVIDIA driver fault research](../../../../docs/research/nvidia-driver-faults.md)
for the rules and verification boundaries.
