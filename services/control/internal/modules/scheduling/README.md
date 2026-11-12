# Scheduling

This module validates the adapter-neutral job intent, quota policy, queue order
and whole-job preflight. The reservation repository borrows the workload owner's
PostgreSQL transaction to hold or release the exact local attempt. A retained
foreign-cell record cannot acknowledge a local replay or release. The quota snapshot
repository also uses an expected policy revision. Enabled departments can
attribute loans to sibling workspaces and keep a reclaim pending until every
selected victim has a committed local owner release. Cohort reads refuse foreign
holds and claims, and policy replacement refuses foreign active holds. The cell adapter reads
the exact Kueue or Volcano object over verified TLS. Provision the cell's
bearer credential as a protected regular file. Rotate it by atomically
replacing the file at the same path. Each observation reads the current file
and refuses insecure replacements before contacting the cell. Runtime controllers still
own execution, and a preflight result does not reserve resources.

For unit and TLS transport tests, run:

```sh
go -C services/control test -race ./internal/modules/scheduling/...
```

For the PostgreSQL policy and reservation tests, point `OWNSOLID_SCHEDULING_TEST_DSN_FILE` at a
protected file containing the DSN of a disposable local PostgreSQL database
whose name ends in `_test`. The test creates and removes its own schema. The
candidate schema under `testdata/` does not change the installation lineage.
The [training and database handoff](../../../../../docs/research/scheduling-training-reservations.md)
has the owner transaction points, candidate catalog fingerprints and the
fresh/restore reproduction. The `repository_integration` test tag also tests
real lost commit acknowledgements with an isolated plaintext loopback endpoint.

[Borrowing and reclaim](../../../../../docs/research/scheduling-quota-borrowing.md)
describes the optional candidate schema and its isolated test.

[Wait reasons](../../../../../docs/research/scheduling-wait-reasons.md) describes
how to project an authorized decision without leaking other workloads' details.
Use `WaitReasons` on the current reservation or reclaim error,
`PreflightResult.WaitReasons` on the estimate and `QueueTurnReason` when rank
actually deferred the current entry. Use `ObserveCellWaitReasons` with the
client for the authorized attempt's bound cell and its persisted workload
reference. The adapter verifies the UID before projecting any blocker. Current
Kueue conditions distinguish waiting for quota, exceeding its ceiling, topology placement failure,
an inadmissible queue and admitted pods that are not ready. The projection
uses fixed codes and actions without controller messages or capacity figures.

The submission owner can call `EstimateSubmissionWithin` with a read-only
PostgreSQL REPEATABLE READ transaction, its authorized workspace and a trusted
`CellCapacitySample` for the database's site and cell. The sample must be
current and cover the total and reserved quantity of every requested resource.
An unavailable sample returns `SCHEDULING_CELL_CAPACITY_UNAVAILABLE`. The
estimate returns the policy revision, whole demand and fixed wait reasons.
The owner submits through the existing reservation transaction.

Set `PROJECT_ONE_TEST_DSN_FILE` to the protected loopback fixture DSN for a
disposable PostgreSQL 18.6 database with pgvector 0.8.7, then run:

```sh
go -C services/control test -race ./internal/modules/scheduling -run '^TestPostgresPreflightUsesAuthorizedWorkspaceAndCurrentCell$' -count=1
```

The test uses the installed Workspace authority and fixture cell samples.
See the [owner handoff](../../../../../docs/research/scheduling-admission.md#authorized-scope-and-cell-capacity-handoff)
for the real capacity source and transaction requirements.

For distributed work, `ReserveWithin` stores the entire replica-group demand
on one attempt. `ReleaseGangWithin` checks that the owner has supplied verified
stop or non-delivery evidence for every reserved slot. See [gang reservation
and release](../../../../../docs/research/scheduling-admission.md#atomic-gang-reservation-and-release).

`RankWorkspaceQueueWithin` uses current quota and reads aggregate reservations
for all authorized workspace queues in one tenant with one query.
If an active record in that set has foreign site/cell coordinates, it returns
`SCHEDULING_QUOTA_POLICY_UNAVAILABLE` so the owner can reconcile the records.
Pass a read-only REPEATABLE READ transaction
and the owner's durable queue entries. See [live workspace shares](../../../../../docs/research/scheduling-queue-policy.md#live-workspace-shares).

## Selected-repository queue ranking

`RankWorkspaceQueueExecutorWithin(ctx, q, tenant, entries)` borrows the owner's
selected PostgreSQL executor. The existing pgx entry point remains compatible
and uses the same validation and ranking implementation.

The owner opens its selected Store.Within with read-only access and executes
`SET TRANSACTION ISOLATION LEVEL REPEATABLE READ` as the callback's first SQL
statement. It then reads the complete authorized eligible batch, ranks it and
resolves its candidate through that same executor. The helper rejects pooled,
other-engine and wrong-mode inputs; it never opens a connection or commits.
Priority and durable enqueue sequence come from persisted server authority.

Ranking does not admit work. The workload owner rechecks current authority
and admission in a fresh write transaction. Production batch selection,
priority configuration and actual dispatch remain owner integrations.
This read interface can be used without the current queue-admission entry below. See [selected-repository ranking](../../../../../docs/research/scheduling-queue-policy.md#selected-repository-snapshot-ranking).

## Authorized workspace quota status

`ReadWorkspaceQuotaStatus` uses the selected repository and
`workspaces.Service.ReadScopeWithin` in one bounded transaction. It rechecks the
current session, source and membership, then locks policy and workspace usage
in the existing reservation order. Current members may read their workspace;
Admin uses its installed tenant scope. Cached actor workspace fields do not
select the tenant. A failed or uncertain transaction returns an empty result.

The status distinguishes `unconfigured`, `unallocated` and `configured`.
Only an actual allocation has resource rows. Each row contains the workspace's
guarantee, hard limit, held reservations and `unreserved_quota`, encoded as
decimal strings. The top-level policy revision is also a decimal string. CPU uses millicores, memory bytes and GPUs whole devices.
Unreserved quota is accounting; queue turn, borrowing, placement and physical
capacity can still prevent a job from starting. No job or sibling identity,
ancestor amount, cell capacity or historical wait code is returned.

The `http.Handler` component accepts:

- `GET /api/v1/workspaces/{workspace}/scheduling/quota` for Portal.
- `GET /api/v1/admin/scheduling/quota?workspace_id={workspace}` for Admin.

Both reject request bodies, ambiguous or unknown selectors and the wrong
audience, and set `Cache-Control: no-store`. They call the same authenticated
reader and return no success payload on failure.

These routes are **not installed** in the production dispatcher. IAM must
import the scheduling HTTP package, construct its `Handler{Workspaces: w}`
from the already installed Workspace authority, and dispatch
`schedulinghttp.Owns(path)` **before** the general Workspace routes. Forward
the same response writer, request, authenticated actor and request ID to
`Handle`; retain the owner's session/CSRF/error-envelope processing. Web must
publish the matching OpenAPI contract, generated client, translations and UI
in the same composed feature. No alternate authentication or pool is needed.

The PostgreSQL tests use the installed Workspace authority with candidate
quota tables in their own disposable schema. They do not prove production
router composition, native database role grants or an installed cell flow.
With the protected loopback `PROJECT_ONE_TEST_DSN_FILE` configured:

```sh
go -C services/control test -p 1 -race ./internal/modules/scheduling/... -run '^(TestWorkspaceQuotaStatus|TestQuotaReadRejects|TestPostgresWorkspaceQuotaStatus|TestPostgresHTTPWorkspaceQuotaStatus)' -count=1 -v
```

## Current queue admission

ReserveQueuedWithin borrows the workload owner's PostgreSQL READ COMMITTED
transaction. Call it before other scheduling writes, after retaining the owner's
current actor, Workspace, node, job and physical-resource fences. It holds the
policy shared lock, validates the complete domain and takes the exclusive tenant
transaction fence, then reads the owner's complete eligible batch through its
QueueReader. A current head is reserved through ReserveWithin, including enabled
loans and pending reclaim. A held replay returns false without another queue read.

Ordinary reservation, release and reclaim writes take the tenant shared fence.
The order is policy, tenant, requesting workspace, cohort. This uses a bounded
number of advisory locks rather than one per configured workspace. The owner
supplies its typed-policy-bounded domain and complete eligible scan. An eligible
entry outside that domain or a malformed or foreign workspace fails the transaction. The callback takes no other job row locks,
writes no state and performs no network I/O.

The owner commits dequeue, tasks, attempts, physical holds and audit with the
logical hold. Roll back on any error and publish no success before confirmed
commit. ErrQueueTurnPending has fixed wait_for_queue_turn guidance. Exact positive
release remains the owner's responsibility. This library does not install a
training selector, runtime controller, production schema or UI. Upgrade every
Control writer to the tenant-fence protocol before activating queue admission.
See [ADR 0071](../../../../../docs/decisions/0071-bound-tenant-admission-locking.md).


To qualify the component flows, configure both protected fixture DSN files above
for one disposable PostgreSQL 18.6 endpoint, then run:

```sh
go -C services/control test -p 1 -race -tags repository_integration ./internal/modules/scheduling/... -count=1
```

The tests use a persistent disposable owner queue. They cover current ordering,
owner rollback, ordinary/shared and queued/exclusive contention, replay,
cancellation, borrowing and committed reclaim release. The configured
10,000-workspace test checks constant advisory-lock allocation; it does not
qualify the SCH-111 node/job admission load target.
