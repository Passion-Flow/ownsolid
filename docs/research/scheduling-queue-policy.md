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

## Queue turn and reservation in the owner transaction

The installed owner's write transaction is PostgreSQL READ COMMITTED.
The existing read-only ranking operation cannot be used inside it.
Add `ReserveQueuedWithin` to the scheduling package. It takes the
already-authorized exact attempt and intent, the complete workspace
domain, and a `QueueReader` callback which reads current eligible owner
rows through the supplied executor. It returns the normal reservation
changed/replay result. It returns `SCHEDULING_QUEUE_TURN_PENDING` for a
present candidate deferred by the current rank, without another job's
identity or usage. A missing selected entry is an invalid queue snapshot.

Use the existing policy shared lock, then acquire the existing workspace
advisory locks in canonical ID order for the whole supplied domain.
Validate every workspace under the installed tenant policy before calling
the reader. Invoke the reader after acquiring those locks, so a waiter
observes the preceding dispatch's committed queue state. Read current
quota shares and holds and reserve the selected head through the existing
`ReserveWithin` in the same transaction. Preserve exact held replays even
after the owner's queued row has transitioned. No new persistent queue or
admission counter is needed.

[PostgreSQL transaction advisory locks](https://www.postgresql.org/docs/18/explicit-locking.html#ADVISORY-LOCKS)
are reentrant within one transaction and end with it.
[READ COMMITTED](https://www.postgresql.org/docs/18/transaction-iso.html#XACT-READ-COMMITTED)
provides a new statement snapshot after a lock wait. Sharing the existing
workspace locks also excludes ordinary reservation/release writes for
those workspaces while ranking and admission run. Policy replacement waits
for the existing shared policy lock. A separate ranking transaction would
leave a race before admission. A global exclusive policy lock would
serialize unrelated tenants. Lock only the authorized domain and retain
the original quota, borrowing and positive-release checks.

Call this operation before other scheduling writes in the transaction.
The owner first retains its normal selected actor, node, job and physical
GPU fences. The queue reader must only read, must not acquire other jobs'
row locks, and must not open a second transaction, commit or use network
I/O. It returns the complete currently eligible set or fails explicitly.
The reader derives scope and priority from persisted server authority.
Its unique sequence represents the order of its durable enqueue keys
within this batch. It cannot use HTTP array order or a browser timestamp.

Exercise real PostgreSQL records for owner queue state and scheduling
holds in one borrowed transaction. Verify a fair head, a non-head with no
write, same-attempt replay after dequeue, owner failure rollback,
concurrent consumers, a reader invoked only after preceding dequeue
commits, a real committed dequeue with its wire acknowledgement suppressed,
cancellation before admission, retained holds while cancelling and positive
release with the owner transition in one commit. A late owner failure must
roll back both that release and the terminal state. Foreign tenant/workspace
or reservation scope must leave
queue state, owner revisions, policy revision and holds unchanged.
These fixtures establish transaction behavior. The actual training
reader and dispatcher call sites remain owner integrations.

The implementation uses the existing Go, pgx, UUID and PostgreSQL stack
and the licenses recorded above. Kueue remains an Apache-2.0 design
reference. No dependency, model, dataset or runtime download is added.

## Training owner patch contract

The following methods are integration requirements for the training owner.
They are absent from the installed training service. The scheduling lane
implements only the admission operation and its borrowed-transaction tests.

```go
// Exported by package scheduling.
type QueueReader func(context.Context, transaction.Executor) ([]QueueEntry, error)

func ReserveQueuedWithin(
    ctx context.Context, q transaction.Executor,
    attempt Attempt, intent JobIntent,
    workspaces []string, read QueueReader,
) (bool, error)
```

The training owner must implement these service methods.

```go
func (s *Service) schedulingWorkspacesWithin(
    ctx context.Context, q transaction.Executor, tenant string,
) ([]string, error)

func (s *Service) schedulingEntriesWithin(
    ctx context.Context, q transaction.Executor, tenant string,
) ([]scheduling.QueueEntry, error)

func (s *Service) nextFairCandidateWithin(
    ctx context.Context, tx pgx.Tx, tenant string,
) (candidate, error)
```

In `training/authority.go`, replace the initial `nextCandidate` FIFO
selection with `nextFairCandidateWithin` before claiming fair dispatch.
Open a fresh read-only REPEATABLE READ transaction on the selected
PostgreSQL owner pool. Read the complete eligible batch with
`schedulingEntriesWithin` through its borrowed executor, call
`RankWorkspaceQueueWithin` with that same transaction, then resolve the
first ranked job to the existing `candidate` fields through the same
snapshot. No ranked entry means the existing no-candidate outcome. Do
not use a different legacy pool or silently bypass scheduling on another
engine.

Keep `DispatchNext`'s metadata freeze and file verification. Its final
admission still rechecks current rank in a fresh READ COMMITTED write
transaction. An initial FIFO selector plus final non-head refusal would
cycle through old jobs before reaching the fair head; the refusal gate
alone does not implement the owner's fair candidate selection.

Keep the owner's queued-authority reconciliation for jobs excluded by a
revoked requester or inactive Workspace. Eligible-only selection must not
silently strand such rows. Any failure or cancellation transition still
runs through current owner scope and locks; foreign site/cell rows remain
untouched. Add that regression beside the real fair-selection test.

Both owner readers must use the installation tenant, local site/cell,
active Workspace and current persisted requester authority. The workspace
reader supplies the complete dispatch domain. The entry reader runs
through the executor passed by scheduling and selects only due queued
rows across the complete tenant dispatch domain. Do not filter this reader
by the previously supplied workspace list: if an eligible workspace appeared
since that list was read, returning it lets scheduling reject the stale
domain and the owner retry. It must preserve the existing durable
`next_dispatch_at,created_at,id` order in its unique positive sequences.
A complete ordered batch can assign ordinals from those keys. A truncated
page cannot. Preserve current default priority zero until the owner
provides a persisted, authorized priority-class mapping. Bound the
complete scan with the owning policy's job limits and fail on overflow
instead of silently omitting competing work.

The owner must also put local scope predicates on its selected persisted
job read before any wait, failure or revision update. `nextCandidate`
currently lacks that predicate. `loadDelegatedWithin` uses the selected
job lookup before `dispatchSnapshotWithin`; that lookup must refuse
foreign site/cell rows. The scheduling operation cannot authorize a
previous owner mutation after the fact.

In `training/dispatch.go:reserveDispatchWithin`, keep the existing
`training_dispatch_reservation` savepoint and physical GPU acquisition.
Before `Nodes.QueueTrainingWithin`, derive `Attempt` and `JobIntent`
from `w`, `j` and `snapshot.command` as in the existing
[reservation handoff](scheduling-training-reservations.md#training-owner-handoff).
Read the workspace domain, then invoke `ReserveQueuedWithin` with the
reader closure bound to `w.TenantID`. A local queue-turn or quota wait
rolls back the savepoint before recording the existing queued wait state.
Foreign scope, malformed data, storage failure and stale identity return
an error and roll back the owner transaction. They may not update a
revision, task, attempt, physical hold or scheduling hold. An exact held
replay returns false and must trigger an owner state reread, never a
second task insertion. A confirmed admission stores the task, attempt,
job transition and audit with the scheduling hold in that one commit.
Unknown commits publish no success; retry through current owner state.

The scheduling call uses this closure shape inside the existing
savepoint, with `attempt` and `intent` derived from the locked values:

```go
domain, err := s.schedulingWorkspacesWithin(ctx, q, w.TenantID)
if err != nil {
    return false, err
}
changed, err := scheduling.ReserveQueuedWithin(
    ctx, q, attempt, intent, domain,
    func(ctx context.Context, owner transaction.Executor) ([]scheduling.QueueEntry, error) {
        return s.schedulingEntriesWithin(ctx, owner, w.TenantID)
    },
)
```

This snippet shows the call boundary only. The owner must handle the
changed/error result and perform the state, task, attempt and audit
writes described above.

Keep `Cancel`'s queued-to-cancelled transition free of a hold. A
preparing/running cancellation remains cancelling until positive physical
stop evidence. In `RecordStop`, add the existing `ReleaseWithin`
beside the exact physical release after validating the frozen command,
attempt, node and stop receipt. Add the same call only to
`ReconcileNext`'s proven never-delivered branch. A missing post-cutover
reservation is an integrity error. Existing holds require a deliberate
cutover reconciliation before activating the production hooks.

The owner replay must extend `TestActualPairedTrainingDispatch` in
`dispatch_repository_integration_test.go` and the existing stop/cancel
regressions. It must use the admitted production catalog and current
runtime grants. Verify fair selection from actual queued training rows;
duplicate and concurrent dispatch; cancellation before and during
admission; task or audit failure after reservation; unknown commit replay;
exact physical stop and never-delivered release. Verify that a foreign
selected job or held record changes no owner revision or dispatch state.
The scheduling fixtures persist owner queue state for rollback and
concurrency tests. They do not replace this real training acceptance run.
