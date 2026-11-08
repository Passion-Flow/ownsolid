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
attempt. A queued recovery pointer must disappear on cancellation,
terminal dispatch failure or restore invalidation. A restored job waiting
to retry cannot retain its pending intent after operator invalidation.
Keep its immutable recovery record with its
previous attempt and empty next attempt. Existing databases can contain a
terminal job with an old pointer. Read that job with no pending recovery
and keep the historical record available. An active cancellation retains
its lease until the exact stop is accepted.

Deleting the recovery record would lose the user's history and idempotency
identity. Releasing an active lease on a cancellation request could let the
next workload reuse a GPU that still has a physical owner. The existing
nullable response field and status codes cover these outcomes.

## Scheduler handoff

[Create](../../services/control/internal/modules/training/creator_repository.go)
continues to commit the authenticated, queued training job and its audit.
[Kueue admission](https://kueue.sigs.k8s.io/docs/concepts/admission/) separates
queued demand from a resource hold. Take quota only in
[reserveDispatchWithin](../../services/control/internal/modules/training/dispatch.go)
after current authority, immutable input and physical GPU checks. The existing
[PostgreSQL savepoint](https://www.postgresql.org/docs/18/sql-rollback-to.html)
contains the physical hold, scheduling hold, node task and numbered attempt.
Use the locked workspace and job and the frozen command identity. Demand is
one NVIDIA GPU and the frozen worker memory budget. CPU seconds are a
cumulative CPU-time budget. A millicore request needs its own policy.

Use stable training wait codes for quota exhaustion, an unconfigured policy
and a missing workspace allocation. Roll the savepoint back before writing
the wait state. A held replay on the queued-only branch indicates stale owner
state and must abort. A failed or unknown owner commit publishes no dispatch
success. A retry starts by reading current job state.

The stop and reconcile paths release the scheduling hold next to the physical
lease, only after exact stop proof or transactional proof of no delivery.
They keep the physical GPU lock ahead of scheduling locks. Replaying an
accepted receipt follows the existing owner replay path. A missing or
prematurely released hold aborts a new stop acceptance. Expiry of a delivered
lease or loss of human access does not by itself establish physical stop.

The [scheduler candidate](scheduling-training-reservations.md) supplies the
borrowed PostgreSQL writer and fixture SQL. Enable this training path only in
a test-owned candidate schema. Table-existence probing or a configurable quota
bypass would let a damaged or partly migrated installation ignore capacity.
Production activation needs catalog validation, runtime grants and a cutover
that drains or accounts for pre-existing physical holds. Dispatch and stop
owners must switch together. There is no new dependency, model or dataset. The linked scheduler research records the
existing PostgreSQL, pgx, Go and UUID licence terms and offline use.

## Verification

Drive the published routes through the TLS HTTP fixture and use real
PostgreSQL and immutable dataset publication. Check creation replay, failure
and its error code, scoped recovery, stale-revision refusal, cancellation of
a queued recovery, retained history and rollback when the cancellation audit
fails. Check active cancellation and exact stop using synthetic node
observations and receipts. Exercise terminal dispatch failure and old rows
with a stale recovery pointer. Drive the operator restore invalidation and verify it withdraws
the queued recovery while preserving history and the prior release. Exercise
node disable with the production recovery owner graph and verify that an
uncertain physical lease remains fenced. CLI, UI and actual GPU execution
are separate acceptance gates.

The change adds no dependency, model or dataset. The existing Go, pgx and
PostgreSQL licensing and offline requirements are recorded in the
[scheduling model research](scheduling-job-model.md#licensing-and-offline-operation).
The fixture uses synthetic rows, node reports and stop receipts.

For the candidate integration, apply the two scheduler SQL fixtures to the
test-owned training schema, then seed the quota through the existing writer.
Verify a denied dispatch leaves no task, attempt, physical hold or scheduling
hold. Verify node busy rolls both holds back and retains its wait code. Inject
audit failure after reservation and after stop proof to check transaction
rollback. Retry through the regular dispatcher and replay an accepted stop
without taking or releasing another hold. Check both exact stop and proven
never-delivered paths, including an absent reservation and a delivered lease
that has lost contact. Exercise the default service with the unchanged catalog.

On Linux amd64, the three candidate cases passed against a disconnected
PostgreSQL 18.6 fixture with pgvector 0.8.7 and CPython 3.12.15. The fixture
started with the 118-table installation lineage, added the two candidate
tables, and removed the isolated schema after each case. Three control cases
passed against the unchanged catalog. Node hardware facts and container
absence proofs were synthetic. The authorized training, task, reservation,
stop and audit writes were real.
