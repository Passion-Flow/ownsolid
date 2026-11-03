# Inference deployment state

Deployments connects verified model versions to admitted nodes and hardware reservations. It owns desired/observed deployment state, runtime observations and Registry-backed deployment references.

[service.go](service.go) is the action/list boundary; [hardware.go](hardware.go) and hardware_repository.go manage admitted hardware state; [runtime_repository.go](runtime_repository.go), intent_repository.go and observation_repository.go retain the lifecycle. [derived.go](derived.go), [distribution.go](distribution.go) and Registry action/reference files are explicit consumer integrations.

Nodes and models remain the authority for node/profile and artifact identity. Migration 000006 includes deployments and GPU leases. Creating a deployment or accepting desired state does not mean a container is ready; current observations, stale attempts, unknown results and removal/fencing must preserve existing reconciliation.

Synchronize [nodes](../nodes/README.md), [models](../models/README.md), [distribution](../distribution/README.md), agent/inference, contracts/agent deployment recipe and HTTP interfaces, deploy/inference and [deployments frontend](../../../../../apps/web/packages/deployments).

Focused native check: go -C services/control test ./internal/modules/deployments. Real SQL reservation/concurrency, daemon/image and actual NVIDIA backend qualification are separate; fixtures must not silently replace the declared target architecture.

The historical native gateway identity slice in [native_gateway.go](native_gateway.go)
and gateway_repository.go validates deployment/GPU/model identity and stop/removal
for the [approved-model chain](../models/native-chain.md). The current selected
native [runtime](native_runtime.go) additionally implements creation, desired-state
actions, observation ingestion, hardware/task ownership and Registry references
through explicit native statements in the existing repositories. Borrowed
executors must belong to the exact Identity kernel; a dialect label alone is
insufficient. Unknown commits retain their original ownership and recovery rules.

The historical first slice used the `models_v2` contribution. Current execution
requires the complete serving catalog and DML-only runtime grants. The
[native runtime contract](../../../../../docs/milestones/m29-domestic-platform/deployments-native-runtime.md)
retains source-bound controls and build results separately from actual native SQL,
installed lifecycle, restored serving, and physical backend qualification.


M06 cumulative integration (2026-10-26): explicit `models` extends maintenance
through entry11 and `upgrade-models`, retaining the installed prefix/principal.
Only approved immutable import, deployment/GPU ownership, exact leased file
transfer and bounded model worker are added. Later owners remain structurally
inactive. Minimal restore invalidates imports and preserves uncertain GPU leases;
see [current integration](../../../../../docs/execution/mvp/m06-cumulative-integration-20261026.md).
