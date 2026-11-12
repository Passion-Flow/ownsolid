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

The source at `145852e7` rebases these owners onto main `95f85dbb` and its
updated model-import implementation. On disconnected Linux amd64 PostgreSQL
18.6 at schema 59, all nine retained-input and owner groups passed together,
including all eight site/cell owner subcases, paired dispatch/operator and
actual CPU accepted-result readback. `make check`, `make build` and the four
ordinary Go owner packages (training, datasets, mongodatasets and
modelpreparations) also passed on that exact source. The Go and repository
gates used loopback-only network namespaces with offline dependency caches.
No new worker computation or physical production stop ran.

The immutable raw logs are retained in
`/root/lane-trn-cpu-admission.RaaYxrR7`:

- `schema59-trn-rebased-145852e7.log`: SHA-256 `4adb47604619805c33467dbf4492884842494419beee48b8fd18a54a3911c603`.
- `schema59-trn-gates-145852e7.log`: SHA-256 `dab17bd21f96b6d782ec7d2eea0809f80f005e28445591f3ba72010f5eb508bf`.
- `schema59-trn-build-after-io-145852e7.log`: SHA-256 `1cd830f11f44547aba7cbedc7a94bdf3c5cf4f14f04a19d67c47ec12e164790c`.
- `schema59-trn-final-modules-145852e7.log`: SHA-256 `9c222c04692015ee9be0b127ad89caa37b5d5f7939034d810f70718aa24f3695`.

The first schema-59 replay at `8495818a` passed the eight neighboring groups
but the new owner snapshot compared a UUID audit target with text. The
correction was test-only SQL; the corrected group passed at `be30d3f4`, and
the refreshed nine-group run above passed without relaxing product fences.
The earlier raw log SHA-256 is
`f0a50d7789e70be7b945725348200b4bf70d0b6920b3d8f993abfb92eb66a336`;
the correction/check log SHA-256 is
`ca07474c280c2c60ac1252dc22b8a6c64c44f10bb68dd46b2cb7230fc75362e2`.
The original pre-compilation refusal at 344,899,584 free bytes is preserved
in `continued-cell-pg-attempt1.log`, SHA-256
`86470fef7c4fd1dea0a0e0995b82764bafd87535814a9ca104bf7f430366dae0`.

Each admitted phase used both original Linux locks and the unchanged 3 GiB
free-memory, 12 GiB available-memory, 15 GiB disk and 5-percent I/O guards.
Refused preflights started no test or build. Passed phases were retained;
only pending phases were requested after actual holder/resource changes.
The maintained replay includes all nine groups and passes Bash syntax.

The existing 20 training Python unit tests passed with no skips at `e24d9abd`
in the retained complete 37-package CPU environment: Python 3.12.15, exact
Torch 2.14.1+cpu and the original approved Qwen tokenizer assets. The
network-none, read-only-root container exited 0 with OOMKilled=false and was
removed. The raw `post-sso-trn-e24d9abd.log` SHA-256 is `e1ddec2fffbe925ce74fd0d1c4849daf9ebec4c6600bc845c813f47c56d71e64`.
Python, data-worker and training-contract bytes remain unchanged from the
original `096c50b8` computation source. Main's new SSO implementation changed
Go inputs; the following PostgreSQL preflight refused before startup at
I/O full avg10 15.89 and avg60 15.82 percent. Fresh Go acceptance after that
rebase remains separate from the completed `145852e7` Go qualification.
The actual installation, five worker invocations and step-one recovery
retain their independent source/input scope. Installed Agent/control/UI,
signed media and NVIDIA qualification, and the fair-queue prerequisites
above remain open; no roadmap box is ticked by this component acceptance.
