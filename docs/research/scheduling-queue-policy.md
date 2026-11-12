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
