# Model preparation, review and derivation

Snapshot copying uses [snapshotio](../../platform/snapshotio/README.md) for bounded
writeback. This owner retains paths, context/size/hash, exclusive output, final
file/directory durability, audit and partial failures. See the
[M17 contract](../../../../../docs/milestones/m17-offline-compose/snapshot-writeback.md).

This owner binds accepted training output and held-out data into preparation attempts, accepts evaluation/merged-model results and controls review/derived-model publication. Alias target changes must remain coordinated with distribution; a worker's gate result is not review approval.

Use create.go, holdouts.go and training_admission.go for source admission; dispatch/events/stop/runtime files for attempts; upload/publication/integrity files for returned bytes; [review.go](review.go) and review_snapshot.go for review identity. Creator/admission/source/holdout repositories preserve exact selected-SQL boundaries.

Migration 000010 stores held-outs, attempts, reviews, artifacts, derivations and alias target/cutover intents. Current training/held-out/model/node/Workspace authority is rechecked at the relevant stage. Input mutation, partial transfer, unknown result, invalidated review and failed alias cutover retain their own reconciliation; do not collapse them into one status.

The scoped preparation detail read exposes internal `DatasetLineage.Training`
and `DatasetLineage.Holdout` references from the validated version 1 input
snapshot. Each contains the dataset ID, version ID and manifest SHA-256. These
references survive source version replacement or retirement. The HTTP detail
response keeps its existing fields. See the
[dataset lineage handoff](../../../../../docs/research/training-dataset-lineage.md#preparation-detail-projection).

This large module should only be split along held-out source, attempt/result and review/publication ownership after cross-domain transactions and snapshot consumers are enumerated. Domain policy must not move into the Python evaluator.

Synchronize [contracts/model-lifecycle](../../../../../contracts/model-lifecycle), [preparation runtime](../../../../../runtimes/model-lifecycle/README.md), agent/modelpreparations, training/models/distribution, [model-lifecycle frontend](../../../../../web/README.md), [model lifecycle image](../../../../../deploy/images/model-lifecycle/Dockerfile) and [agent assets](../../../../../deploy/model-lifecycle/agent) and [M10](../../../../../docs/milestones/m10-model-lifecycle).

Focused native check: go -C services/control test ./internal/modules/modelpreparations. Real SQL source/review/cutover, worker process integrity and target merge/inference are separate evidence, with original failures retained.


The current cumulative `lifecycle` profile composes this owner after accepted M09 through account/000016, rather than replaying historical platform000010. It injects the actual training holdout guard and joins its worker before closing the preparation store. Source-bound chronology is tracked in [the cumulative record](../../../../../docs/execution/mvp/m10-cumulative-integration-20261026.md); the [current closeout](../../../../../docs/execution/mvp/m10-closeout-current.md) binds complete initial implementation/local delivery and separately pending physical targets.

`OpenStoreForVerification` opens the existing ownership lock descriptor O_RDONLY
with the same exclusive nonblocking flock and namespace/private-root/link/mode/
limit checks. It creates or adopts no state. Ordinary `OpenStore` retains O_RDWR.
This fixed verification operation adds no deployment switch and is not a general
mutation sandbox; callers use readonly mounts. Native PostgreSQL `VerifyRestore`
selects it through the [readonly owner contract](../../../../../docs/milestones/m18-kubernetes/postgres-readonly-owners.md).
Unsupported readonly-exclusive locking fails closed and still needs filesystem/
CSI qualification. The original readonly failure remains in its dated checkpoint;
new image/fresh-target execution has separate evidence.


The selected native Dameng path uses explicit quoted statements in the existing
creator, admission, authority, execution, upload, physical-stop, recovery,
history/review and operator repositories. `native_runtime.go` owns native
lexicographic pages and strict canonical UUID/UTC microsecond/integer-boolean
row decoding, while `native_sql.go` binds JSON as text for explicit CLOB casts.
Borrowed native callbacks must belong to the Identity store's exact native
kernel; a dialect string alone grants no transaction authority. The executor
itself remains unchanged when borrowed across module boundaries.

Retained tasks preserve original node/profile delivery uncertainty and require
the separately accepted physical stop before releasing a GPU. Unknown commits
clear callback DTOs and are never automatically replayed. Native inventory walks
and upload coverage checks remain bounded by the existing reader capacity and
file custody. The [native preparation contract](../../../../../docs/milestones/m29-domestic-platform/preparation-runtime.md)
separates these source and behavioral checks from actual native SQL/installed
lifecycle and physical GPU qualification. PostgreSQL/MySQL statements and argument
expressions retain their original selected behavior.
