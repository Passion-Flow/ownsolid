# Linux node fact collection

[collect_linux.go](collect_linux.go) collects the host facts consumed by enrollment and agent preflight. [parse.go](parse.go) contains bounded parsing of observed system data. The disk path is an explicit caller input; reporting capacity does not authorize allocating it or establish a sustained workload budget.

[collect_other.go](collect_other.go) deliberately returns AGENT_OS_UNSUPPORTED outside Linux. Do not replace missing observations with fabricated Linux facts to make a development-host test pass. Target architecture, OS, memory, CPU and device observations belong to the actual machine named by the profile.

This package has no persistent business state, credentials or control-plane permissions. Runtime/managers consume its facts, and [agent contracts](../../../../contracts/agent) define the wire representation. When changing a field or parser, synchronize handshake/preflight consumers and invalid/missing/overflow cases.

Focused native check: `go -C services/agent test ./internal/discovery`. Parser tests qualify the supplied fixtures only; actual host collection and GPU/workload support require their distinct target checks.
