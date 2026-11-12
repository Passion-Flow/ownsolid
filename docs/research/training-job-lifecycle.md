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
with a stale recovery pointer. Drive the operator restore invalidation and verify it withdraws
the queued recovery while preserving history and the prior release. Exercise
node disable with the production recovery owner graph and verify that an
uncertain physical lease remains fenced. CLI, UI and actual GPU execution
are separate acceptance gates.

The change adds no dependency, model or dataset. The existing Go, pgx and
PostgreSQL licensing and offline requirements are recorded in the
[scheduling model research](scheduling-job-model.md#licensing-and-offline-operation).
The fixture uses synthetic rows, node reports and stop receipts.

## Local-cell owner fence before scheduler admission

The scheduling handoff requires the selected persisted training job to belong
to this database's site and cell before any wait, failure, cancellation or
attempt mutation. Migration 30 already supplies server-owned site/cell
columns and `local_site_id()` / `local_cell_id()`. The current PostgreSQL
FIFO selector, reconciliation selector and shared locked job loader omit
those predicates. A retained foreign row with the same tenant/workspace
must remain untouched, including when its requester has lost authority.

Use the local database functions in both background selectors and the shared
locked job read. Do not accept coordinates from the request or repair a
foreign row by assigning it the current cell. Repeat the fence in the final
owner transaction: PostgreSQL [READ COMMITTED snapshots](https://www.postgresql.org/docs/18/transaction-iso.html#XACT-READ-COMMITTED)
can change between candidate selection and admission. The existing exact
job lock and confirmed-commit behavior remain authoritative. This is a
prerequisite to the scheduler's atomic admission, whose quota reservation
must remain separate from physical execution; see [Kueue workload lifecycle](https://kueue.sigs.k8s.io/docs/concepts/workload/).

A selector-only filter would miss a scope change after selection. Failing
only in scheduling would happen after earlier owner wait/failure writes.
Rewriting retained coordinates would destroy the evidence needed for
reconciliation. No migration, public API, Agent change, model, dataset or
new dependency is required. Existing Go/pgx/PostgreSQL redistribution and
offline terms are recorded above. MySQL's inherited profile is unchanged;
this acceptance covers the production PostgreSQL cell catalog.

On real disconnected PostgreSQL, move only owned fixture jobs to a foreign
site or cell. Due queued and active reconciliation selectors must skip them;
direct cancellation must return the existing not-found error. Change scope
between initial selection and each delegated transaction and require refusal
without any task, attempt, GPU lease, audit or owner revision mutation.
Return the exact fixture coordinates and require normal dispatch/cancel to
work again. These are synthetic node observations and authenticated control
transactions, not GPU execution. Keep TRN-101 open until scheduler wiring,
CLI/UI and installed execution complete.

## Queue handoff prerequisites on the selected repository

The verified SCH handoff retains original commits `43670ebf`, `c90df6cd`,
`748ea2b8`, `9709ee19` and `4708ffc3`, in order. Its `ReserveQueuedWithin`
accepts the actual borrowed `transaction.Executor` and a complete owner queue
reader. Its initial `RankWorkspaceQueueWithin` still accepts a raw `pgx.Tx`.
The selected training `transaction.Store` exposes borrowed executors and
starts read-only transactions at READ COMMITTED. A configured selected Store
can have no legacy PostgreSQL pool. The package contains no training wiring
or production migration. Its historical schema-57 fingerprints do not admit
the current catalog or an intervening GW-120/DAT-101 migration.

Continue with the owner-side local-cell fences above independently. Full
fair dispatch needs the original SCH implementation plus ARC's admitted
quota/reservation DDL, grants and fresh/restore lineage. It also needs an
additive scheduler ranking entry point on the borrowed executor. The selected
owner can begin its read-only transaction, set REPEATABLE READ before its
first data query, read the complete eligible queue and pass that same executor
to ranking. Scheduling must verify the actual read-only/isolation settings
before reading policy or usage. The existing raw-pgx entry point can retain
its contract and delegate to that checked executor path.

Do not open a different legacy pool, emulate a `pgx.Tx`, copy scheduling's
policy/usage queries into training or fall back to FIFO. A generic transaction
rewrite is unnecessary. The queue reader must remain complete and bounded by
the owning job policy, repeat current authority after scheduling locks and
preserve the existing exact physical-stop release rules. The installed
training acceptance still needs fair selection, concurrent and unknown-commit
replay, quota/queue waits, cancellation and final stop/non-delivery release.
This is an explicit shared-API and production-DDL prerequisite, not evidence
that any of those dispatch paths has run.

## Native acceptance boundary for the local-cell candidates

The local-cell code and regressions at `0b4ea8ff` passed formatting, diff
review, layout and roadmap checks; all 18 current owner README link targets
exist. The original CPU worker inputs are unchanged. One bounded acquisition
of both original Linux locks requested the combined scope/dispatch/operator
and retained CPU acceptance replay. The original preflight observed
344,899,584 free bytes, below 3 GiB, with I/O full avg10 0.0 and avg60 0.45.
It refused before the replay wrapper, Go compilation or PostgreSQL startup.
The preserved `continued-cell-pg-attempt1.log` hashes to `86470fef7c4fd1dea0a0e0995b82764bafd87535814a9ca104bf7f430366dae0`.
No new owner regression or post-rebase repository pass is claimed.

The prepared private `run-training-cell-regression.sh` runs the nine selected
training test groups in one disposable disconnected PostgreSQL fixture. Its
inputs are the retained CPU output, publication and approved model already
used by the existing replay. Run it only after both locks and the unchanged
resource preflight admit execution, then complete repository check/build and
module tests against the actual rebased source. Preserve any failing log;
do not loop the fixture while resources or an earlier lane's window block it.
The local implementation and its runtime qualification remain separate.
