# Scheduling priority and fair sharing

[SCH-103](../product/roadmap/scheduling-and-quota.md) orders competing jobs.
[Kueue priority classes](https://kueue.sigs.k8s.io/docs/concepts/workload_priority_class/)
separate workload admission priority from pod priority. [Kueue fair
sharing](https://kueue.sigs.k8s.io/docs/concepts/fair_sharing/) compares dominant
shares when choosing between queues. The
[Dominant Resource Fairness paper](https://www.usenix.org/conference/nsdi11/dominant-resource-fairness-fair-allocation-multiple-resource-types)
explains why GPU-only ordering can disadvantage CPU- or memory-heavy users.

Rank eligible work first by the operator-resolved priority, then by the lowest
current dominant share of its queue's capacity allocation, then by a durable
enqueue sequence. Compare integer ratios exactly. Rank only queues competing
in the same policy domain. Recompute shares after a reservation when dispatch
is composed. A queue's individual runtime age and caller clock do not affect
ordering. This is a snapshot ranking rule. Kueue's decayed historical share
requires a durable usage history and remains a separate policy option.

FIFO alone favors a team that can submit the biggest backlog first. Floating
point shares can collapse distinct integers into the same rank. Strict priority
is intentional and needs limits on who can select priority classes, since an
unbounded high-priority queue can starve lower classes.

Only Go's [BSD-3-Clause standard library](https://go.dev/LICENSE) is needed.
Kueue is an Apache-2.0 design reference. No upstream code, model, dataset or
online service is used.

Exercise priority, dominant-resource switching, equal shares, near-integer-limit
quantities, invalid dimensions, and FIFO ordering with stable ties. Full
acceptance needs durable queue and priority configuration, authority, repeated
reservation and offline workload submission from a product entrance.

## Live workspace shares

Kueue separates [admission fair sharing](https://kueue.sigs.k8s.io/docs/concepts/fair_sharing/)
based on historical consumption from [preemption fair sharing](https://kueue.sigs.k8s.io/v0.20/docs/concepts/preemption/#fair-sharing)
based on dominant shares. The original [DRF paper](https://people.eecs.berkeley.edu/~matei/papers/2011/nsdi_drf.pdf)
defines the dominant share across all resource dimensions. This first ownsolid
path retains its current dominant-share comparison and reads its inputs from
the existing reservation and quota facts. Historical consumption and
cross-tenant policy remain separate from this ranking.

Rank authorized eligible entries for one tenant, with one queue per workspace.
The owner supplies its authorized job identities, priority values and durable
enqueue sequence. Read each distinct workspace's hard allocation from the
quota tree and its held resources from the reservation repository. The hard
allocation bounds actual held usage even for a workspace that borrows without
a nominal guarantee. Reuse the existing rank operation, including its error
for a queue without a capacity allocation. The owner filters eligibility and
repeats ranking after a dispatch or release.

Use the same read-only PostgreSQL REPEATABLE READ mode as submission
preflight. [PostgreSQL snapshot semantics](https://www.postgresql.org/docs/18/transaction-iso.html#XACT-REPEATABLE-READ)
keep the policy and all queue usage in one committed snapshot. Validate the
whole input scope before reading usage. Return the order without exposing
other tenants' usage or any individual job's reserved resource quantities.
Fetch held resources for all validated workspaces in one reservation query.
Use PostgreSQL's [array membership](https://www.postgresql.org/docs/18/functions-comparisons.html#FUNCTIONS-COMPARISONS-ANY-SOME)
and group the existing numeric totals by workspace and resource. Reuse the
bound UUID array that the borrowing repository already uses and keep the
tenant predicate and unreleased-row filter. Workspaces without holds have
zero usage. This keeps reservation reads constant as the number of queues
increases and sends only aggregate rows to the ranker. Dispatch still
performs its own current admission transaction.

Caller-maintained counters would diverge on owner rollback or uncertain
commit. A query per workspace adds a network round trip to each eligible queue.
Reading independently under READ COMMITTED could mix policy revisions.
Combining all tenants in this first path would require a distinct authority
and policy. The same [Go](https://go.dev/LICENSE), [pgx](https://github.com/jackc/pgx/blob/master/LICENSE)
and [PostgreSQL](https://www.postgresql.org/about/licence/) dependencies permit
commercial redistribution with notices and offline operation. Kueue remains
an Apache-2.0 design reference. This path adds no dependency, model or dataset.

Verify with isolated PostgreSQL that a committed reservation changes the
next rank, an existing read snapshot stays consistent, a release restores
the FIFO tie, a higher priority remains first, and foreign and missing
workspaces return the same error without partial results. Count actual
reservation query calls for multiple eligible workspaces and assert that an
invalid scope issues no reservation read. Durable queue membership, priority
management and the authenticated dispatch flow remain owner integrations
for SCH-103.

## Durable queue and persisted reservation boundary

The training owner's [nextCandidate](../../services/control/internal/modules/training/authority.go)
selects one due queued job using `next_dispatch_at, created_at, id`.
[DispatchNext](../../services/control/internal/modules/training/dispatch.go)
then rechecks the persisted requester, membership authority, node and job
before dispatch. Its retry time can change, so a page index or job creation
timestamp alone cannot supply `QueueEntry.Sequence`. The public training
list is a paginated, newest-first display read. The current owner contract
provides neither an eligible batch in the ranking snapshot nor a resolved
priority class. Those inputs and the final current-authority dispatch remain
with the workload owner. Reusing its due-job query as a new public queue
would duplicate its ownership and retry rules.

The scheduler can independently qualify its existing durable reservation
read. [ADR 0006](../decisions/0006-cell-and-site-scope.md) places resource
records in one cell database. A retained reservation in a different site or
cell must not silently alter this cell's workspace share. When any active
record in the authorized workspace set carries foreign coordinates, fail the
read with `SCHEDULING_QUOTA_POLICY_UNAVAILABLE` and return no rank or estimate.
Keep the records intact for the owner to reconcile. Dropping those records
from the sum could understate held resources in a mis-scoped installation.

Use PostgreSQL's [bool_and aggregate](https://www.postgresql.org/docs/18/functions-aggregate.html)
in the existing grouped numeric read, alongside the tenant, authorized
workspace set and active-row predicate. This preserves one reservation
query and the same REPEATABLE READ snapshot. Reuse the existing Go, pgx and
PostgreSQL dependencies and their licenses recorded above.

On isolated PostgreSQL, seed holds through the scheduler, change only the
owned fixture record's site or cell, and require one failed aggregate read
with no partial ordering. Verify an unrelated workspace still ranks and
that correcting the fixture record restores the original rank. The normal
module regression also checks preflight, contention, replay and release.
This qualifies persisted scheduling reads. Production queue membership,
priority resolution and the actual dispatcher integration remain open.

## Selected-repository snapshot ranking

The initial training selector owns a selected transaction.Executor, not a raw
pgx transaction. Add RankWorkspaceQueueExecutorWithin without changing the
existing RankWorkspaceQueueWithin signature. Both use the same scope,
allocation, aggregate-read and rank implementation. The selected executor
must be a borrowed PostgreSQL read-only REPEATABLE READ transaction; reject
a pool, nil context/executor, another engine or another transaction mode.

The selected Store's read-only Within starts READ COMMITTED. The owner can
issue SET TRANSACTION ISOLATION LEVEL REPEATABLE READ as its first SQL
statement in that callback, before reading eligible entries or authority.
PostgreSQL documents both the
[first-query restriction](https://www.postgresql.org/docs/18/sql-set-transaction.html)
and [stable snapshot semantics](https://www.postgresql.org/docs/18/transaction-iso.html#XACT-REPEATABLE-READ).
Read the complete authorized eligible batch, policy and shares through that
same executor. The ranking helper validates the mode; it does not alter it,
open another connection or commit. Retain the repository's sanitized errors.

A legacy DB-pool fallback would break selected-repository ownership. A
READ COMMITTED hint can mix the eligible set, policy and current shares.
Changing isolation inside the ranker can occur after the owner has already
read data. None is needed. Ranking grants no admission: after metadata/file
verification, the owner still rechecks current authority and queue turn in
a fresh READ COMMITTED owner admission transaction. Keep the
queued-authority reconciliation for entries excluded from the eligible set.

Use real selected Store.Within callbacks with first-statement isolation
setup. Read persistent owner queue rows, commit a competing hold and a new
eligible row concurrently, and verify that the existing snapshot remains
stable while a fresh snapshot observes both changes. Reject read-only
READ COMMITTED, writable REPEATABLE READ and read-only SERIALIZABLE. Preserve
foreign/missing scope refusal, one aggregate usage query, and the legacy
pgx and preflight snapshot contracts. Unit checks reject pooled and other-
engine executors before SQL. Existing Go, pgx and PostgreSQL licences above
cover this additive interface; no dependency, model or dataset is added.


### Independent interface qualification

The selected-store implementation begins a read-only READ COMMITTED transaction
and calls the operation without issuing a query first. Its caller can therefore
set isolation before the eligible-batch read. The PostgreSQL 18 transaction
documentation above confirms that this must precede the first query, not merely
the ranker's first query.

This interface does not depend on the separately retained queued-admission
implementation. Reuse the mainline quota/reservation fixture and persist a small
owner queue in its disposable schema. Read that queue through the selected
executor, commit a competing reservation and enqueue through another connection,
then compare the retained and fresh snapshots. Assert that ranking changes no
owner state, revision or reservation. Keep the original broader candidate intact;
do not copy its pending admission helpers into this path just to obtain a fixture.

The current mainline pgx ranking and submission-preflight tests qualify the
shared snapshot guard after extraction. Run the two selected-store PostgreSQL
flows alongside those five existing regressions. The interface is accepted only
when all seven real flows and the required repository/module gates pass.
Production eligible-batch authority, durable priority configuration, current
write admission and the authenticated UI flow remain owner integrations.


## Atomic queue admission and bounded tenant fencing

The published read interface does not make the final write decision atomic.
ReserveQueuedWithin must read the current eligible batch after excluding
competing reservation mutations, rank it from current shares and reserve the
selected head in the owner's existing READ COMMITTED transaction. Exact held
replays return false without reading an owner row that may already be dequeued.
The owner commits physical holds, dequeue, attempts and audit with that hold.

The inherited candidate locked every supplied workspace before reading the
queue. The current typed Workspace policy permits up to 10,000 workspaces.
That makes lock allocation and SQL round trips grow with the configured domain.
PostgreSQL documents its finite
[shared advisory-lock pool](https://www.postgresql.org/docs/18/explicit-locking.html#ADVISORY-LOCKS).
Use one tenant transaction-level read/write fence instead. Existing reservation,
release and reclaim writers acquire its shared side; queued admission acquires
its exclusive side. Different tenants retain independent fences. Hash collisions
can only serialize unrelated tenants, never grant capacity or authority.

PostgreSQL's
[transaction advisory functions](https://www.postgresql.org/docs/18/functions-admin.html#FUNCTIONS-ADVISORY-LOCKS)
provide shared/exclusive conflict semantics, reentrancy and automatic release at
transaction end. Keep the order policy, tenant, requesting workspace, cohort.
The cohort never takes another workspace fence. Current quota-status reads
retain their existing policy/workspace readback fences; snapshot ranking remains
read-only and acquires no admission fence.

Validate and normalize the complete server-owned workspace domain before the
owner callback. It must include the requested workspace even for a held replay.
The callback derives tenant/local scope, eligibility, priority and positive
sequence from persisted owner authority, uses only the supplied executor, takes
no other job row locks, and performs no write or network call. It returns all
currently eligible rows or fails on the owning typed policy's bounds. Never
filter it by the earlier domain: a newly eligible out-of-domain workspace must
force a retry rather than disappear. Unknown/cancelled/foreign attempts publish
no success. A non-head returns SCHEDULING_QUEUE_TURN_PENDING with fixed guidance.

Reuse ReserveWithin after a head is selected. It already enforces hard quota,
exact attempts, enabled borrowing, pending reclaim, durable commit and loan
attribution. Duplicating those decisions in the wrapper could lose funding
or admit before positive victim release. The old O(workspaces) lock protocol,
global policy-exclusive locking, separate ranking transactions and owner-side
usage counters are rejected. See [ADR 0073](../decisions/0073-bound-tenant-admission-locking.md).

Acceptance uses real isolated PostgreSQL: non-head refusal; head/dequeue in one
commit; owner/audit rollback; complete-domain refusal; concurrent same-attempt
replay; cancellation; exact release with owner terminal state; lost COMMIT
acknowledgement; and retained foreign scope. New cases prove that ordinary writers
block behind an exclusive tenant fence, another tenant proceeds, rollback releases
it, and 10,000 configured workspaces do not require 10,000 advisory locks. Enabled
borrowing must retain exact loans, signal reclaim and wait for committed release
before nominal admission. These are component fixtures, not actual training/UI
acceptance. The production owner still owns authority, task delivery and cutover.

The existing Go/UUID BSD-3-Clause, pgx MIT and PostgreSQL licenses permit commercial
redistribution with notices and offline operation. Kueue remains an Apache-2.0
reference, including its separate nominal, borrowing and lending limits. This
change adds no dependency, model, data set, production table or runtime download.


### Component qualification

Source 5ee946b6 passed 45 real PostgreSQL cases and 66 top-level tests across
scheduling, its HTTP component and cell adapter, with Go race instrumentation,
PostgreSQL 18.6 and pgvector 0.8.7 on Linux amd64. The network namespace had no
default IPv4 or IPv6 route; the database had no container network or host port.

The current queue tests verified owner dequeue/hold commit and rollback, current
shares after ordinary and queued writer waits, concurrent replay, cancellation,
reader/scope failure, exact positive release, borrowing attribution and reclaim,
and a suppressed real COMMIT acknowledgement. The configured 10,000-workspace
domain held exactly two advisory lock tags: tenant and selected workspace.
This is a lock-allocation result, not SCH-111 node/job admission latency evidence.

The same source passed the complete Control module suite, make check and make
build on macOS arm64, and the complete Control module suite and make check on
Linux amd64. Both used Go 1.27.3 and offline dependencies. Linux build admission
was deferred by shared-host I/O pressure; it is not recorded as a completed run.

With both protected fixture DSN variables in the module README configured:

```sh
go -C services/control test -p 1 -race -tags repository_integration ./internal/modules/scheduling/... -skip '^TestPostgresReservationCatalogRoundTrip$' -count=1 -timeout 8m -v
```

The separate fresh/dump/restore catalog harness was explicitly excluded; this
change has no production DDL. The fixtures use a privileged test principal, not
the native serving role. Production grants/readiness, complete eligible-batch
authority, persisted priority configuration, current authenticated dispatch,
all-writer protocol cutover and the installed UI flow remain open. SCH-103 stays
unticked.
