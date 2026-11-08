# TRN-101 training lifecycle and SCH-101 transaction boundary

[Kubeflow TrainJob lifecycle](https://trainer.kubeflow.org/en/latest/user-guides/trainjob-lifecycle.html)
keeps the run and its configuration when the runtime is suspended.
[Kueue Workload](https://kueue.sigs.k8s.io/docs/concepts/workload/) represents
the resource demand of an admitted workload. Its lifecycle distinguishes
quota reservation, admission and completion. [PostgreSQL transactions](https://www.postgresql.org/docs/current/tutorial-transactions.html)
make the job intent, resource ownership and corresponding audit event atomic
at their respective commit points.

## Training contract

The existing Admin and Portal training operations use the same owner.
Creation freezes model, dataset version and recipe, verifies the published
input outside locks, and repeats authority and source checks in the final
transaction. An exact creation key replay returns the same durable job.
Dispatch creates the physical GPU lease, node task and numbered attempt
together. A verified stop may record a failure and release the exact lease.
Explicit restart recovery checks the latest revision and the prior stop,
then records a new queued intent for the same job. Checkpoint recovery also
verifies the accepted checkpoint identity and bytes.

Cancelling queued work ends its active intent without creating another
attempt. A queued recovery pointer must disappear on cancellation or
terminal dispatch failure. Keep its immutable recovery record with its
previous attempt and empty next attempt. Existing databases can contain a
terminal job with an old pointer. Read that job with no pending recovery
and keep the historical record available. An active cancellation retains
its lease until the exact stop is accepted.

Deleting the recovery record would lose the user's history and idempotency
identity. Releasing an active lease on a cancellation request could let the
next workload reuse a GPU that still has a physical owner. The existing
nullable response field and status codes cover these outcomes.

## Scheduler handoff

The training creation hook belongs in the final callback of
[Create](../../services/control/internal/modules/training/creator_repository.go),
after idempotency replay, repeated authority and current input checks. A
durable scheduling writer must borrow that same transaction and take the
generated training job ID plus the authenticated tenant and workspace.
An exact replay must resolve the existing scheduler intent. Its insertion,
the training row and the creation audit must all roll back on failure.
External placement calls belong outside the transaction.

Creation represents queued demand. A scheduler reservation belongs with
[reserveDispatchWithin](../../services/control/internal/modules/training/dispatch.go)
and must roll back with a rejected node task, a stale attempt or an audit
failure. Preserve the exact stop and lease release boundary when reconciling
scheduler state. Use the verified whole NVIDIA GPU and the frozen memory
budget when defining the resource mapping. The training CPU-seconds budget
measures cumulative CPU time. A millicore request needs its own policy.

Main has the typed scheduling intent and preflight. Its PostgreSQL repository
currently persists only a quota policy in a candidate test schema. A durable
job writer, production schema lineage and cell composition are still needed.
An in-memory projection at creation would disappear after restart and leave
no transaction to prove.

## Verification

Drive the published routes through the TLS HTTP fixture and use real
PostgreSQL and immutable dataset publication. Check creation replay, failure
and its error code, scoped recovery, stale-revision refusal, cancellation of
a queued recovery, retained history and rollback when the cancellation audit
fails. Check active cancellation and exact stop using synthetic node
observations and receipts. Exercise terminal dispatch failure and old rows
with a stale recovery pointer. CLI, UI and actual GPU execution are separate
acceptance gates.

The change adds no dependency, model or dataset. The existing Go, pgx and
PostgreSQL licensing and offline requirements are recorded in the
[scheduling model research](scheduling-job-model.md#licensing-and-offline-operation).
The fixture uses synthetic rows, node reports and stop receipts.
