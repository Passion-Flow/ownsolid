# Training reservations in the owner transaction

SCH-101 needs the existing training dispatch to make a durable whole-attempt
reservation. [Kueue admission](https://kueue.sigs.k8s.io/docs/concepts/admission/)
reserves all workload resources before start and separates that from execution
readiness. The ownsolid scheduler applies workspace quotas and the training
owner retains physical GPU leasing, credential checks, task delivery and stop
proof. Training job creation may queue work before capacity is available.

The scheduler accepts the authorized training scope and exact attempt identity
from a borrowed PostgreSQL executor. It neither opens a second transaction nor
looks up a session. The existing PostgreSQL kernel supplies READ COMMITTED.
Use a shared lock on the current quota policy and an advisory transaction lock
per tenant/workspace to serialize competing reservations within that workspace.
The next statement reads usage after the lock is acquired. Policy replacement
takes the exclusive policy row lock before checking live workspace usage.
[PostgreSQL locks](https://www.postgresql.org/docs/18/explicit-locking.html),
[READ COMMITTED](https://www.postgresql.org/docs/18/transaction-iso.html) and
[savepoints](https://www.postgresql.org/docs/18/sql-savepoint.html) define these
behaviors. Reject other isolation levels so a waiting writer cannot use a stale
snapshot to decide capacity.

Reserve the complete normalized demand or nothing. A workspace's active usage
must fit its burst ceiling. Since the tree separately enforces the sum of child
ceilings at each level, the same invariant bounds every ancestor. Replacing a
policy may neither remove the scope of an active attempt nor shrink its limit
below usage. An active-attempt index bounds reservation queries to one
workspace, and policy replacement alone reads active usage across workspaces.
The first path does not need a second counter table. Measure scan cost before
using this shape for high-volume fleets.

Retain one row per exact attempt after release. A retry with the same identity
and normalized intent replays while held. An already released attempt cannot
be held again. The database rejects two active attempts for one source and
rejects a changed attempt ID at the same ordinal. A release is scoped to
its exact attempt and may be repeated without touching a later attempt. The
owner supplies the proof and commits its state, audit and release together.
A lease timeout or missing Kubernetes object is insufficient proof.

An autonomous scheduler submission API would duplicate existing permissions
and job ownership. Reserving quota during job creation would charge queued
work. Committing scheduling in another transaction would leave phantom
holds or uncharged tasks when either transaction fails. A global exclusive
lock on every admission would serialize unrelated workspaces.

Dependencies are the existing [PostgreSQL](https://www.postgresql.org/about/licence/),
[pgx](https://github.com/jackc/pgx/blob/master/LICENSE) (MIT),
[Go](https://go.dev/LICENSE) and [UUID](https://github.com/google/uuid/blob/master/LICENSE)
(BSD-3-Clause). They permit commercial use, redistribution with their notices
and offline operation. Kueue is an Apache-2.0 design reference. No new
dependency, model, dataset or external runtime connection is introduced.

Verify against real PostgreSQL using the same borrowed executor as training.
Exercise failed owner writes, savepoint rollback followed by committed wait
state, exact replay and changed replay, cross-workspace or stale release,
concurrent contention, replacement below live usage and a full dump/restore
with held and released attempts. Compare the exact fresh and restored catalog
fingerprints with the current production lineage before handing off migration
integration. The production owner still needs to wire the hook and run the
real authenticated training flow, including denial and positive stop proof.

## Local attempt replay and release

[ADR 0006](../decisions/0006-cell-and-site-scope.md) binds scheduling
reservations to one database cell. Verify the stored site and cell when
recognizing an exact held replay. Matching only the tenant, workspace,
source, ordinal and attempt could acknowledge a retained foreign-cell hold
as local. Such a row must return `SCHEDULING_RESERVATION_CONFLICT`.
The release lookup and update both require the local coordinates, so a
foreign record returns `SCHEDULING_RESERVATION_NOT_FOUND`. Keep the exact
stored row and its usage intact. The aggregate read already refuses
foreign records in a selected workspace.

Resolve coordinates through the existing local database functions in the
same owner transaction. Client-supplied scope or a fallback to the current
cell would authorize a different record. The same existing row identity,
locks, replay keys and synchronous commit setting continue to fence
concurrent operations. No candidate DDL or production fingerprint changes
are required.

On disconnected PostgreSQL, exercise both single-replica and gang holds.
Change only the owned fixture row to a foreign site or cell. Replay and
release must fail without changing the held record. Restoring the fixture's
local coordinates must allow the original exact replay, one release and
its idempotent retry. This uses the existing licensed stack and synthetic
scope changes in a disposable schema. Physical stop proof and production
owner integration remain separate acceptance conditions.

## Training owner handoff

Use [the existing dispatch transaction](../../services/control/internal/modules/training/dispatch.go).
`DispatchNext` first freezes inputs, verifies bytes outside locks, then enters
`delegatedRepositoryTransaction` again. The owner rechecks the requester epoch,
current workspace authority, node and job, and compares the frozen snapshot.
The submission entry point in
[creator_repository.go](../../services/control/internal/modules/training/creator_repository.go)
continues to create a queued training job without taking capacity.

In `reserveDispatchWithin`, call `scheduling.ReserveWithin` after
`reserveTrainingGPUWithin` succeeds and before `QueueTrainingWithin`.
Both calls sit within the existing `training_dispatch_reservation` savepoint.
For SCH-103, use `ReserveQueuedWithin` at this same point instead of the
standalone reservation shown below. Supply the authorized workspace domain
and complete eligible owner reader described in the
[training queue patch contract](scheduling-queue-policy.md#training-owner-patch-contract).
The basic snippet describes reservation identity and demand; by itself it
does not enforce queue order.
Keep the physical GPU lock before the scheduling locks on both acquisition and
release. The hook derives the identity and intent from the locked owner values.

```go
attempt := scheduling.Attempt{
    TenantID: w.TenantID, WorkspaceID: w.ID,
    ID: snapshot.command.AttemptID, Number: snapshot.command.AttemptNumber,
    Kind: scheduling.Training, SourceID: j.ID,
}
intent := scheduling.JobIntent{
    Version: scheduling.IntentVersion, Kind: attempt.Kind, SourceID: attempt.SourceID,
    Groups: []scheduling.ReplicaGroup{{
        Name: "worker", Replicas: 1,
        Requests: scheduling.Resources{
            scheduling.NVIDIAGPU: 1,
            scheduling.Memory: snapshot.command.Recipe.Resources.MemoryBytes,
        },
    }},
}
changed, err := scheduling.ReserveWithin(ctx, q, attempt, intent)
```

The first backend leases one physical NVIDIA GPU. The recipe's CPU seconds
are a time budget, so the integration does not convert them into a millicore
request. Reuse the existing GPU eligibility and node admission checks. Scope,
source and attempt identifiers come from the owner transaction, not the HTTP
payload. Use the PostgreSQL path with a configured policy. Rollout must drain
or explicitly account for pre-existing physical holds before enabling the
hook. Never silently exempt an attempt admitted after cutover.

Use `errors.Is` for the stable scheduling errors. Capacity exhaustion rolls
back the savepoint with `dispatchRollbackWait`, records the wait reason and
commits no physical or scheduling hold. Missing policy or workspace allocation
also needs an actionable owner-defined wait or configuration error. A changed
replay, stale attempt, unsupported transaction or storage error must never
queue a task. On the current queued-only dispatch branch, an exact held replay
requires reading the owner state again before taking another dispatch action. A later task-busy, attempt insert, job update or audit failure must use the
existing savepoint or whole-transaction rollback. Publish dispatch success
only after a confirmed commit. After an unknown
commit, reenter the normal dispatcher and read the persisted owner state before
choosing another attempt.

In [RecordStop](../../services/control/internal/modules/training/stop.go), call
`scheduling.ReleaseWithin` next to the physical GPU release, after the exact
credential, command, attempt and stop proof have been checked. In
[ReconcileNext](../../services/control/internal/modules/training/reconcile.go),
make the same call only in the existing `nodes.NeverDelivered` branch, next
to its physical lease release. Use the frozen command's attempt ID and number
and the same owner scope. The already-accepted receipt branch remains a
replay. An unacknowledged or expired delivered attempt, cancellation request,
or `recovery_required` state still holds capacity. Keep permission to release
an exact physically stopped attempt even after the original actor loses
access, as the existing authenticated stop path does. Missing reservation
for a post-cutover attempt is an integrity error. Release and the owner
state, physical GPU update and audit commit together.

The remaining acceptance test calls the actual authorized training path with
the migrated catalog and verifies denial has no task, attempt, physical lease
or scheduling hold. It also verifies a saved wait state after a late failure,
a successful dispatch, unknown-commit reconciliation and a verified stop.
That test belongs with the training owner, whose normal entry point keeps the
SCI-101 execution path under the same authority.

## Database owner handoff and evidence

The candidate migration concatenates
[quota.sql](../../services/control/internal/modules/scheduling/testdata/quota.sql)
and [reservations.sql](../../services/control/internal/modules/scheduling/testdata/reservations.sql)
in that order. Their combined SHA-256 is
`3ff35e67c8b2e45c80b7c4293fa01c82d40a769e31361ce2f52a9141c664dab9`.
On lineage v57 this is the next contiguous migration, with 120 public tables
instead of 118. The policy table is global, so extend the scope enumeration
test for `scheduling_quota_policy`. Reservations use the existing local site and
cell defaults. Verify the standard runtime grants and the backup/restore
catalog checks alongside the version, count and digest changes. The database
owner should recompute the digests if the baseline or candidate DDL changes.

On Linux, PostgreSQL 18.6 with pgvector 0.8.7 ran all 57 production migrations
in a fresh database. The unchanged fresh and logical restore digests matched
the existing constants before either candidate table was applied. In the
same fixture, the candidate schema was applied, seeded through the borrowed
executor, exported with `pg_dump -Fc --no-owner --no-privileges`, and loaded
with `pg_restore --exit-on-error --no-owner --no-privileges` into an empty
database. The following fingerprints are the exact values returned by
`CatalogSQL` for the candidate.

| Catalog | SHA-256 |
| --- | --- |
| Fresh | `74bb081d63ccfa84daddafac1e724f7b1411858b2830f11bff03978de2d7b320` |
| Restored | `d003f7f2e803186a38d09f60a25503d205e4b6bea3fbcd0b6c49932d2bf83a4b` |

To reproduce, apply the baseline and both candidate SQL files to an empty
`lane_sch_catalog_fresh_test` database. Run
`go -C services/control test -p 1 ./internal/modules/scheduling -run '^TestPostgresReservationCatalogRoundTrip$' -count=1`
with `OWNSOLID_SCHEDULING_CATALOG_DSN_FILE` pointing to its protected local
DSN file and `OWNSOLID_SCHEDULING_CATALOG_PHASE=seed`. Dump and restore into
`lane_sch_catalog_restored_test`, then run the same test against that database
with phase `verify`. Both runs check the held attempt, released attempt,
recorded quota and local site/cell identity. Hash the exact UTF-8 text returned
by `CatalogSQL` without the psql line separator on each database. PostgreSQL
ran without a network interface and the Go catalog tests used its Unix socket
from a disconnected network namespace.

Production lineage and application entry points still require owner
integration. SCH-101 and SCI-101 remain open until that integration and
end-to-end acceptance run.
