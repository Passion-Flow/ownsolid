# Borrowing and reclaim within a department

SCH-109 lends idle guaranteed resources between workspaces in an enabled
department. [Kueue cohorts](https://kueue.sigs.k8s.io/docs/concepts/cohort/)
share unused nominal quota. [ClusterQueue configuration](https://kueue.sigs.k8s.io/docs/tasks/manage/administer_cluster_quotas/)
defines nominal quota, borrowing limits and lending limits separately.
[Kueue preemption](https://kueue.sigs.k8s.io/docs/concepts/preemption/)
lets a queue reclaim nominal quota from borrowing workloads and orders
eligible targets by policy. Its [workload API](https://github.com/kubernetes-sigs/kueue/blob/main/apis/kueue/v1beta1/workload_types.go)
distinguishes eviction from completion.

The first ownsolid path pools direct sibling workspaces under an explicitly
enabled department in one tenant. The existing workspace burst limit and the
sum of child ceilings remain hard bounds. A borrower uses its own nominal
capacity first. Other workspaces lend only their unused, uncommitted guarantees.
Record exact lender and resource quantities on the admitted attempt. Keep
that funding until the attempt's existing release path records positive stop
or never-delivered evidence. This path keeps unused burst limits within
their own workspace and keeps borrowing inside the tenant and department.

When a whole request fits its workspace guarantee but outstanding loans
occupy that guarantee, persist one pending reclaim per workspace. Select only
attempts carrying loans from that workspace, newest loan first using a
database-assigned order. Traverse until all affected resource deficits are
covered. This slice defines recency because it has no authenticated
preemption priority input. A pending reclaim prevents new loans from its
lender and other admissions in that workspace until it admits or is
cancelled. The exact waiting attempt cannot reserve until all planned victims
have a committed release. Cancelling a plan leaves every victim hold intact.

Borrowing and reclaim use the existing PostgreSQL READ COMMITTED owner
transaction. The lock order is policy, shared tenant fence, requesting workspace and then cohort.
A cohort never locks another workspace. A physical release only decreases
usage, and a subsequent statement must observe its committed row before using
the capacity. Policy changes affecting an enabled cohort require it to be
idle so old funding cannot be reassigned to a new policy.
[PostgreSQL explicit locking](https://www.postgresql.org/docs/18/explicit-locking.html)
and [READ COMMITTED](https://www.postgresql.org/docs/18/transaction-iso.html)
supply the lock and visibility semantics.

A read-only plan would let new borrowing consume reclaimed guarantees before
the claimant retries. Releasing resources at planning time would double
allocate live work. A cross-tenant pool would require separate authority and
policy. The first slice persists the plan and leaves stop delivery, grace
periods, priority eligibility, audit and rollout to the runtime owners.

The existing [Go](https://go.dev/LICENSE) and [UUID](https://github.com/google/uuid/blob/master/LICENSE)
BSD-3-Clause libraries, [pgx](https://github.com/jackc/pgx/blob/master/LICENSE)
(MIT) and [PostgreSQL](https://www.postgresql.org/about/licence/) permit
commercial use, redistribution with their notices and offline use. Kueue
is an Apache-2.0 reference. This slice adds no dependency, model or dataset.

Use real isolated PostgreSQL to prove exact loan attribution, whole-job
limits, no double lending under concurrent admission, deterministic reclaim
order, unchanged held usage while waiting, idempotence across lost commit
acknowledgements, cancellation and stale release. Candidate DDL stays separate
from the SCH-101 handoff. Runtime activation and production catalog acceptance
are required before SCH-109 ticks.

## Owner integration and candidate schema

On the existing reservation path, `ReserveWithin` returns
`ErrReclaimRequired` when outstanding loans block an otherwise nominal
request. The workload owner first rolls back any provisional physical hold.
It can persist the exact waiting attempt through `RequestReclaimWithin`
in a borrowed transaction with the corresponding wait state and audit.
After a confirmed commit, the owner reconciles the ordered victim list with
its exact attempts and existing stop hooks. A repeated pending plan can list
an already stopped victim, so runtime delivery must be idempotent. Once
every selected victim has a committed `ReleaseWithin` write, retry the same
reservation. Admission consumes the plan in the same transaction as the
new hold. `CancelReclaimWithin` withdraws the exact claim while the owner
retains authority over the job and any stops it already requested.

Apply [borrowing.sql](../../services/control/internal/modules/scheduling/testdata/borrowing.sql)
after the two SCH-101 candidate tables in an isolated schema. It adds
per-attempt loan allocations and a database-assigned order to reservations,
plus a cell-scoped reclaim table. The two existing SCH-101 candidate files
retain their original bytes and fingerprints. Enable `QuotaNode.Borrowing`
on an idle department only after this follow-on DDL is present.
The database owner still assigns the production migration version, updates
the table count and grants, and verifies fresh and restored catalog digests.
The runtime owner still chooses eligible victims and wires stop delivery,
proof, audit and the wait states.

For the PostgreSQL test, set `OWNSOLID_SCHEDULING_TEST_DSN_FILE` to a protected
DSN file for a local disposable database ending in `_test` and run
`go -C services/control test -p 1 -race ./internal/modules/scheduling/... -count=1`.
The fixture creates isolated schemas and applies the follow-on DDL only to
the borrowing tests. To exercise the real PostgreSQL acknowledgement-loss
proxy, use an isolated plaintext loopback endpoint and add
`-tags repository_integration`.

The SHA-256 of the follow-on candidate SQL is `ea20adf6825b04990f8f8679e1eb86035450fdc2ea4f4a8b484c07beb20be24c`.

## Cell scope of loans and release evidence

The cohort uses the local reservation database defined by
[ADR 0006](../decisions/0006-cell-and-site-scope.md). Every active hold and
pending reclaim read into that cohort must have the current database's site
and cell. A foreign record returns `SCHEDULING_QUOTA_POLICY_UNAVAILABLE`
without a loan plan. Exact claim replay also checks both coordinates before
returning victims. A foreign claim conflicts with the local attempt.

A committed `released_at` on a different cell's victim cannot authorize
this cell's reclaim. The positive release read must count only the selected,
tenant-scoped, local victim records. Otherwise a record from another cell
could prematurely satisfy the release barrier. Keep the wait state until
all exact local records prove release. Loan attribution and claim state
updates also require local coordinates.

Policy replacement reads every held reservation before allowing a changed
workspace ceiling. Reject foreign records there as well, preserving the
current revision and allocation. Silently excluding them could discard
held usage during a mis-scoped restore. Cell reconciliation remains the
resource owner's operation. The existing locks, SQL predicates, PostgreSQL
boolean aggregate and synchronous commit behavior suffice, with no schema
or dependency changes.

Test each coordinate independently in disposable PostgreSQL records.
Refuse foreign cohort holds, pending claim replay and cancellation,
and a released foreign victim. Restore fixture coordinates and verify
normal admission and exact replay. Keep the existing concurrent borrower,
rollback and acknowledgement-loss tests. A separate policy test must
retain its original revision on a foreign hold and succeed after fixture
correction. These tests qualify the transaction boundary. The physical
stop source, runtime owner and production migration remain required.


## Queue-admission compatibility

The queue wrapper must delegate a selected head to ReserveWithin. That operation
already calls borrowingForReservation and recordBorrowingWithin, including pending
reclaim and positive local-release barriers. Queue ranking is not an alternative
loan allocator. Test a queued borrower and a nominal lender together, including
rollback and exact held replay, so a queue integration cannot silently bypass
loan attribution or reclaim.

The planned bounded admission fence in [ADR 0071](../decisions/0071-bound-tenant-admission-locking.md)
extends the write order to policy, tenant, requesting workspace, cohort. Ordinary
reservation/release/reclaim writers take the tenant shared fence; current queued
admission takes its exclusive side. Cohort reads still lock no other workspace.
Production activation requires all Control writers to use that protocol and the
serving principal to execute both transaction advisory-lock functions. The
existing reservation, loan and reclaim candidate DDL is unchanged.
