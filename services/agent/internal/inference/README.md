# Owned inference workloads

[manager.go](manager.go) owns approved inference workload state for the explicit Linux amd64/root profile. [execute.go](execute.go), [container.go](container.go) and [docker.go](docker.go) implement bounded operations against a protected Docker socket. The control deployment/node owners still choose and authorize the task.

Model/cache and derived-cache paths validate retained bytes. GPU, memory and preflight helpers admit real resources; network/ownership helpers bind the exact container and network. [verification.go](verification.go) checks actual serving behavior rather than accepting container startup as inference readiness. Registry integration must preserve the same approved image identity.

The private journal binds node/task/command digest, phase, image/network/container IDs, cache-publication intent and verification. Recovery and [remove.go](remove.go) act on that exact ownership. Revoked work, failed model loading, OOM or lost external responses cannot become a new untracked container or broad cleanup. Manager.Close closes owned transport/probe clients without erasing recovery state.

Configuration uses inference.Config and injected [settings](../settings/README.md), with workload roots disjoint from agent identity. Synchronize control/deployments, nodes and models, [agent contracts](../../../../contracts/agent), approved image/model recipes and native Docker/network/GPU tests.

Focused check: `go -C services/agent test ./internal/inference`. Docker doubles, native loopback faults, actual OOM and real NVIDIA generation are different evidence profiles; do not promote one to another or weaken platform admission for development-host convenience.
