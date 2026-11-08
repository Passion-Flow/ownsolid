# Training orchestration

Snapshot copying uses [snapshotio](../../platform/snapshotio/README.md) for bounded
writeback. This owner retains paths, context/size/hash, exclusive output, final
file/directory durability, audit and partial failures. See the
[M17 contract](../../../../../docs/milestones/m17-offline-compose/snapshot-writeback.md).

This owner authorizes training jobs from verified model/dataset versions, manages node/GPU dispatch and attempts, persists events/checkpoints/artifacts and accepts or recovers results. Python performs computation; the agent owns exact local containers; neither can commit training business state.

[creator_repository.go](creator_repository.go), admission.go and dispatch.go cover current inputs and admission. [events.go](events.go), checkpoints.go and metrics.go retain bounded observations. Upload/transfer/output/publication files validate artifacts; [accepted_adapter.go](accepted_adapter.go) supplies preparation inputs. Stop, invalidation, operator and recovery files preserve uncertain/stale attempts.

`AcceptedAdapterSnapshot` owns current actor/Workspace checks through the selected PostgreSQL/MySQL transaction; `AcceptedAdapterWithin` borrows those checks from its caller. Both retain the exact stopped job/attempt/command/descriptor identity. Snapshot errors, including an unknown commit, expose no source; file hashing/opening remains outside business locks. A machine claim binds its stop token to exactly one unbound attempt or rolls back its task lease and audit. See the [selected-result handoff contract](../../../../../docs/milestones/m09-training/selected-result-handoff.md) for the executable worker/callback/result path and its pending database gates.

Migration 000009 stores jobs, attempts, checkpoints, artifacts, recoveries and uploads. Input dataset/model hashes, node/profile, GPU lease and attempt identity remain part of acceptance. A sealed checkpoint can be resumed only through the exact recovery contract; a cancelled/disconnected worker cannot be promoted to success from partial logs.

The [training lifecycle contract](../../../../../docs/research/training-job-lifecycle.md)
covers idempotent creation, failure recovery and cancellation. Cancellation,
terminal dispatch failure and restore invalidation clear the pending reference
and preserve its history. Active cancellation holds the physical lease until
the exact attempt stop is accepted.

The scheduling candidate reserves quota inside the dispatch savepoint after
the physical GPU hold. Exact stop and proven non-delivery release quota and
the physical hold in the same transaction. The integration test applies both
scheduler SQL fixtures to a test-owned PostgreSQL schema. Production
activation requires the admitted catalog and accounting for existing
physical holds. Run the candidate cases with the usual local PostgreSQL
fixture and pinned CPython using
`go -C services/control test ./internal/modules/training -run '^TestActualTrainingScheduling' -count=1`.

This owner has separate admission, execution, transfer and accepted-artifact areas. Any future extraction must retain current creator/Workspace/license checks and lock ordering, without inventing a second scheduler or artifact acceptance path.

Synchronize [contracts/training](../../../../../contracts/training), contracts/agent, [Python training](../../../../../runtimes/training/README.md), agent/training, modelpreparations, [training frontend](../../../../../web/README.md), [training image](../../../../../deploy/images/training/Dockerfile) and [agent assets](../../../../../deploy/training/agent) and [M09](../../../../../docs/milestones/m09-training).

Focused native check: go -C services/control test ./internal/modules/training. Real selected-SQL dispatch/recovery, actual CPU worker, target container/cgroup and NVIDIA execution are distinct gates. Run heavy profiles only after explicit resource admission.

The cumulative M09 training composition uses account/000015 after the accepted M08 ledger, preserving its GPU IDs and earlier profile contracts. Only this checked catalog omits the absent M10 holdout owner. Current integration/evidence is tracked in [the cumulative record](../../../../../docs/execution/mvp/m09-cumulative-integration-20261026.md). Historical schema9 gates retain their original scope.

The cumulative M12 matched-recovery fixture extends the existing accepted-output
profile with delivered-M11 native upgrade, encrypted SSO/keyring state and real
knowledge source/vector plus pending replacement/building generation. It reuses
actual accepted CPU files and never retrains. `knowledge_paired_recovery_integration_test.go`
and `sso_upgrade_integration_test.go` keep separate prior/current catalog contracts;
`tools/quality/knowledge/recovery.py --group paired` owns explicit inputs, separate
Go/PostgreSQL groups and optional private temporary internal copies. Controlled
hardware/upstream observations retain their original narrow scope.

The cumulative M13 matched profile begins at the delivered populated M12
knowledge catalog74/18. It prepares real source/vector and encrypted SSO state
before repeating `upgrade-workflows`, checks all original column projections and
the same runtime role OID/SCRAM, then exports/restores all four stores with SQL
and the protected keyring. `workflows_paired_recovery_integration_test.go` retains
a service-created draft and explicitly declared lost-retrieval SQL fixture; it
never invents model success. The real late workflow fault rolls back every80table
owner. Full successful published CPU/browser execution has a separate causal
receipt. Run through `tools/quality/workflows/recovery.py --group paired` with
explicit accepted inputs; no training or hardware qualification is repeated.

`OpenStoreForVerification` opens the existing ownership lock descriptor O_RDONLY
with the same exclusive nonblocking flock and namespace/private-root/link/mode/
limit checks. It creates or adopts no state. Ordinary `OpenStore` retains O_RDWR.
This fixed verification operation adds no deployment switch and is not a general
mutation sandbox; callers use readonly mounts. Native PostgreSQL `VerifyRestore`
selects it through the [readonly owner contract](../../../../../docs/milestones/m18-kubernetes/postgres-readonly-owners.md).
Unsupported readonly-exclusive locking fails closed and still needs filesystem/
CSI qualification. The original readonly failure remains in its dated checkpoint;
new image/fresh-target execution has separate evidence.
