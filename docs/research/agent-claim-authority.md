# Agent claim authority and database cost

Research for the claim path of ARC-109. The cost harness for ARC-108 lives in
`services/control/internal/modules/training/agent_cost_integration_test.go`. The
training, preparation and Registry fixtures needed to extend that harness belong
to their respective module owners. The ARC lane keeps its assertions in place.

## Existing behavior and sources

A node claim opens an authorized transaction for preflight, then another authorized
transaction for the node-owned lease. The first releases unacknowledged work and
checks the cursor against the assigned, acknowledged and in-flight sequences. The
second can also follow delegation to training or model preparation, where the owner
checks live job, input and GPU authority.

[PostgreSQL row locking](https://www.postgresql.org/docs/18/explicit-locking.html#LOCKING-ROWS)
holds a row lock until the end of its transaction. At
[Read Committed](https://www.postgresql.org/docs/18/transaction-iso.html#XACT-READ-COMMITTED),
a new statement can see a concurrent commit, so a claim must keep its node lock
from authorization through lease issuance. The existing workspace status fence
([ADR 0020](../decisions/0020-agent-call-cost-rule.md)) takes node locks in the
changing transaction and the claim reads scope after locking the node.
[PostgreSQL auto_explain](https://www.postgresql.org/docs/18/auto-explain.html)
can measure statement plans, buffers and trigger queries with the existing HTTPS
cost harness. Disable per-node timing for count comparisons to limit measurement
overhead.

No new dependency, model or data set. PostgreSQL and its bundled auto_explain
use the [PostgreSQL License](https://www.postgresql.org/about/licence/).
Go uses [BSD-3-Clause](https://go.dev/LICENSE), and the existing database driver
[pgx uses MIT](https://github.com/jackc/pgx/blob/master/LICENSE). They permit
commercial distribution with the corresponding notices and work offline. The cost
harness uses synthetic node and training fixture data.

## Chosen approach

For a PostgreSQL node-owned claim whose preflight did not write, issue the generic
lease in the same transaction. Authorization, cursor validation and issuance then
share one node lock. If preflight changed a delivery or audit row, commit it before
the generic claim, preserving the existing commit boundary if the next lease is
refused. Resync and its throttle still commit in preflight without a lease.

Keep the delegated path where training or preparation can serve the enrolled
profile. Its owners have their own lock order and, for training, verify data on
disk between two transactions. Do not pass an old authorization decision across
that boundary or keep the node lock held across file reads. Keep the legacy
database path in place until it can be measured against the actual engines.

A cached grant for the lease was rejected because a disabled node, stale cursor
or changed scope must be refused under the current node lock. Moving delegated
claims under the generic node lock was rejected because it would change the order
of workspace, task and node locks and hold a pooled connection through file I/O.
Training and preparation event batching and upload authorization cross their
module ownership boundary. An upload's reserve and commit each need to fence the
authority because the lease can be revoked while the request body is streaming.

The wire contract is unchanged. Both cursor-aware and legacy agents use the same
lease, resync, retry and permission responses before and after upgrade.

## Verification

- The cost harness must show fewer SQL statements for the node-owned empty claim
  and dispatch on both small and large fleets, without raising any limit.
- A missing, disabled or foreign node is refused, and a held claim continues to
  release its database connection while waiting.
- A lost lease reply can be redelivered, a stale cursor resyncs before leasing,
  and concurrent acknowledgement and redelivery cannot both win. A delegated task
  appearing between probes is still refused by the generic path.
- ARC-109 remains open until the dedicated event and upload paths are measured and
  updated within their owners, with revocation while streaming and replay covered.

## Measured results

PostgreSQL 18.6 with pgvector 0.8.7. The focused
`TestReadOnlyClaimChecksAuthorityOnce` records actual statements from the node
service with 50 and 3,000 seeded peers. Both sizes gave the same statement counts.

| Node-owned exchange | Before | After |
| --- | ---: | ---: |
| Idle claim | 15 | 10 |
| Facts dispatch | 47 | 42 |
| Locked credential reads per exchange | 2 | 1 |

The existing HTTPS cost suite measures idle and facts claims using its training
profile. That path still uses its delegated authority boundary, so its counts
stayed 18 and 50. The original suite failed on both revisions with the same seven
existing statement overages, including 75 training-claim statements against a
declaration of 51, 142 training-event statements against 75, and 99 training
upload-chunk statements against 76. The declarations remain in place.

The real database tests passed the ARC-006 sequence, resync, redelivery and
acknowledgement cases, a foreign credential against an unacknowledged lease,
cross-gateway task wake and release of held-claim connections. The training
dispatch interleaving test also passed.

## Shared audit lock

Migration `000054_audit_chain_scope.up.sql` locks the singleton
`audit_chain_head` row for each audit append. A successful node claim appends in
its lease transaction, so the current chain can make agent writes wait on the
same row. The HTTPS cost fixture locks several shared rows while measuring
exchanges, but its list does not yet include the audit head.

A separate PostgreSQL 18.6 probe took a shared lock on that row after creating a
pending facts task. The node's claim reached its one-second deadline after
1.002 seconds and returned `STORAGE_TIMEOUT` with no lease. After the lock was
released, the same task was leased on attempt one. This reproduces a shared lock
and confirms the failed issuance rolled back. It does not measure fleet
throughput or account for each excess statement in the cost suite.

ARC-108 needs the audit owner to measure append cost and agree how to preserve
the offline-verifiable chain without putting its lock on the fleet path. An
updated cost fixture should hold the audit head along with the other shared rows.

## ARC-108 inference heartbeat probe

Reuse [PostgreSQL 18 auto_explain](https://www.postgresql.org/docs/18/auto-explain.html)
and the existing Agent cost recorder to isolate a synchronous heartbeat carrying
an inference observation. Exercise first publication, an update of the same
node's report, and a rejected foreign credential. Use the actual deployment
owner and PostgreSQL with one node, and verify the committed snapshot. The
recorder includes nested statements with per-plan timing disabled. There are no
additional dependencies or models and the PostgreSQL and Go licensing above
applies. Hardware inputs are synthetic.

This probe is narrower than the shared HTTPS test. It excludes transport
certificate reads, deployment-bound observations, buffered liveness, and any
fleet size comparison. A trace-only probe is preferred to changing a statement
budget without evidence. Check the declared ceiling without weakening it and
record separately any nested statement that needs work by another owner.

The first report ran 14 statements, the update 10, and the foreign credential
one. The initial insert in the Deployments owner uses two cell-default lookups
and its node foreign-key check. The cold pacing cache accounts for another two
cell lookups inside its read. Use an explicit join to the one local cell for
pacing and lock only its pacing row when an administrator writes. Return the
stored `last_heartbeat_at` from the existing PostgreSQL `UPDATE ... RETURNING`
([PostgreSQL 18](https://www.postgresql.org/docs/18/dml-returning.html)) and use
that database timestamp in the reply and liveness acceptance. Keep the
node, installation, workspace and credential fences and the non-PostgreSQL
clock reads in place. The direct service probe must fit the existing ceiling
and verify that the response time equals the persisted sample time. Keep the
full transport gate and the deployment-owned insert costs separate.

With PostgreSQL 18.6 and pgvector 0.8.7, `TestInferenceHeartbeatStatementCost`
measured the following on a one-node loopback fixture. The focused pacing,
liveness, and timestamp tests also passed against this database.

| Synchronous service exchange | Statements before | Statements after | Blocks before | Blocks after |
| --- | ---: | ---: | ---: | ---: |
| First inference report, cold pacing | 14 | 11 | 98 | 92 |
| Updated report, warm pacing | 10 | 9 | 38 | 38 |
| Foreign credential | 1 | 1 | 1 | 1 |

A one-node loopback probe issued a real node certificate and sent the first
heartbeat over mTLS HTTPS through the production dispatcher. Before the
Deployments change it returned HTTP 200 with 12 statements, 16 rows and
94 shared blocks, above the unchanged ceiling of 11. The cold pin check
added one statement to the service exchange. The Deployments owner changed
the insert to use site and cell from the authenticated node row in `6ba6da92`,
removing both default lookups. See [the deployment scope proof](deployment-observation-scope.md).

Independent replay on the local PostgreSQL 18.6 and pgvector 0.8.7 fixture
passed twice with the normal mTLS transport and the unchanged statement gate.
The scoped concurrent first-write test passed twice in the same run.

| mTLS HTTPS exchange | Statements | Rows | Shared blocks |
| --- | ---: | ---: | ---: |
| Cold first report | 10 | 14 | 86 |
| Warm update | 8 | 11 | 32 |
| Foreign credential | 2 | 1 | 3 |

The direct service test now reports 9, 7 and 1 statements respectively. To
repeat the transport and concurrency checks, configure the protected local
PostgreSQL fixture and run:

```sh
go -C services/control test -p 1 ./internal/modules/deployments -run '^(TestMutualTLSInferenceObservationCost|TestInferenceObservationKeepsNodeScopeOnConcurrentFirstWrites)$' -count=2 -v
```

The same test at `f316137c` added a retained deployment and restarted the
HTTPS dispatcher over its database. Both independent replays measured 11
statements, 15 rows and 44 blocks for the current revision. After a stop
advanced the revision, a late report used 9 statements, 12 rows and 35
blocks and preserved the stored observation and GPU reservation.

The one-node mTLS gate is green. The shared two-fleet cost suite still needs
the deployment-bound case and the component, preparation and Registry
operations. No declaration or fleet acceptance result has changed.

### Scoped mTLS inference on two seeded fleets

The retained-deployment replay also ran against the same PostgreSQL and
pgvector versions with 50 and 3,000 seeded peers in separate fixture schemas.
An isolated Go test overlay extended `TestMutualTLSInferenceObservationCost`
without changing the Deployments or Training sources. Each peer had an inference
row, four finished facts tasks and four audit events. Statistics were refreshed
before measuring. A second transaction held the installation, tenant, workspace,
user and membership rows `FOR SHARE` during each request, as in
[ADR 0020](../decisions/0020-agent-call-cost-rule.md). The cost recorder was
disabled only during fixture seeding. The original mTLS, scope, persisted
observation and reservation assertions still ran.

| Exchange | SQL statements | Rows | Shared blocks |
| --- | ---: | ---: | ---: |
| Cold first report | 10 / 10 | 14 / 14 | 131 / 140 |
| Warm update | 8 / 8 | 11 / 11 | 31 / 37 |
| Foreign credential | 2 / 2 | 1 / 1 | 3 / 5 |
| Current revision after dispatcher restart | 11 / 11 | 15 / 15 | 121 / 124 |
| Stale revision | 9 / 9 | 12 / 12 | 36 / 41 |

The unchanged heartbeat ceiling of 11 and the shared row-growth and index
checks passed. This measures the cost of individual exchanges against seeded
data. It does not measure simulator traffic or concurrent fleet throughput.
The two-fleet mTLS overlay is local evidence. The shared test still needs to
incorporate those exchanges with the Deployments and Training owners.

The existing shared suite independently measured both fleets and found these
statement budget failures at the same counts on each fleet: certificate 38/13,
enrollment 57/36, facts result 41/16, training chunk 80/76, claim 75/51,
events 139/75 and stop 52/28. The publication section exceeded the local
test deadline. Reproduce the declared-cost checks without the publication
section using the protected test database and pinned CPython:

```sh
go -C services/control test -p 1 ./internal/modules/training -run '^TestAgentCallCostDoesNotGrowWithTheFleet$/^declared_cost$' -count=1 -v
```

ARC-108 remains open for the missing exchanges and the unresolved shared
budget failures. Admission and budget assertions were kept intact.

### Component exchanges on the same two fleet sizes

A separate overlay exercised the existing Systemd Components profile and its
Node Exporter task through the real mTLS dispatch path. It used 50 and 3,000
peers with component state, task and audit history, the same shared-row locks
and the unchanged Agent cost comparator. Component and host observations
were synthetic. The fixture verified a persisted failed result and exact
result replay.

| Exchange | SQL statements | Rows | Shared blocks | Statement ceiling |
| --- | ---: | ---: | ---: | ---: |
| First component report | 14 / 14 | 19 / 19 | 233 / 238 | 11 |
| Component state update | 12 / 12 | 16 / 16 | 48 / 55 | 11 |
| Foreign credential | 2 / 2 | 1 / 1 | 3 / 5 | 11 |
| Start task claim | 44 / 44 | 25 / 25 | 426 / 377 | 51 |
| Task renewal | 15 / 15 | 16 / 16 | 50 / 61 | 22 |
| Failed task result | 45 / 45 | 25 / 25 | 245 / 244 | 16 |
| Exact result replay | 8 / 8 | 11 / 11 | 27 / 33 | 16 |

The first report, update and first result exceeded their budgets. Both
heartbeat writes selected the installation and locked node again inside the
component observer, then evaluated both local cell defaults on the upsert.
The result additionally reread the task and its lease during settlement.
The audit append acquired the singleton chain-head lock, evaluated each
projection frame, and updated both the audit row and chain head.

The component owner can use the authenticated node's explicit site and cell
on the PostgreSQL upsert. Avoiding the extra reads requires the caller to
prove it holds the scoped node fence in the same transaction. Keep the active
and disabled passive-observation semantics and validate the lease before a
result changes state. Audit append and offline checkpoint compatibility
remain Audit-owned. Those three exchanges failed the strict baseline test.

### PostgreSQL component heartbeat scope reads

The current component owner rechecks the persisted node with
`PreparationHistoryWithin` before writing. Keep that check for all callers.
Combine its installation and node reads using a materialized installation
query and a dependent lateral node lookup. The installation query holds
the existing shared locks on the installation and active tenant. Only after
it returns a matching tenant does the lateral lookup take the scoped node
lock. [PostgreSQL WITH materialization](https://www.postgresql.org/docs/18/queries-with.html#QUERIES-WITH-CTE-MATERIALIZATION)
and [LATERAL evaluation](https://www.postgresql.org/docs/18/queries-table-expressions.html#QUERIES-LATERAL)
define the evaluation dependency. Verify the actual plan and lock order.

For PostgreSQL component heartbeats only, project the credential digest from
that same locked node row, and retain the constant-time comparison against the
presented credential. Keep preliminary scope discovery and the Workspace
status read after acquiring the node lock. Under [Read Committed](https://www.postgresql.org/docs/18/transaction-iso.html#XACT-READ-COMMITTED),
the Workspace check needs a fresh statement snapshot after a concurrent
Workspace change has released its node locks. An application-level cached
node or a joined Workspace status could reuse old authority. Other callers retain their separate credential read, and the other SQL
dialects retain their existing queries.

The target is at most 11 SQL statements for a cold component report and for
a warm state change on both seeded fleets. Use the real mTLS transport,
hold the shared rows, preserve the reported and stored state, and require a
foreign credential to be refused without a write. Exercise node removal or
credential replacement after discovery and before the transaction obtains
the node lock. Check that the read uses the new row or refuses, and that
component state and heartbeat time remain unchanged on refusal. Preserve
disabled-node passive observations and the existing result and audit
semantics. The v1 HTTP and session wire remain compatible with older Agents.

The existing [PostgreSQL](https://www.postgresql.org/about/licence/),
[Go](https://go.dev/LICENSE) and [pgx](https://github.com/jackc/pgx/blob/v5.11.0/LICENSE)
licences allow commercial and offline use and redistribution with their
required notices. The probe uses synthetic peers and local trust. There are no
new dependencies, model weights, datasets or runtime downloads.

A PostgreSQL 18.6 lock-order probe confirmed the materialized installation
read acquired its shared lock before waiting on a held node, and the
lateral read returned the committed successor credential. The tracked
regression passed with real HTTPS and PostgreSQL inside a Linux container
with networking disabled. The same test over the previous implementation
refused to pass the unchanged 11-statement budget.

| Measured heartbeat | SQL before, 50 / 3,000 peers | SQL after, 50 / 3,000 peers | Rows after | Blocks after |
| --- | ---: | ---: | ---: | ---: |
| First component report | 14 / 14 | 11 / 11 | 18 / 18 | 231 / 240 |
| Warm component update | 12 / 12 | 9 / 9 | 15 / 15 | 46 / 59 |
| Foreign credential | 2 / 2 | 2 / 2 | 1 / 1 | 3 / 5 |

The old credential was refused after a concurrent replacement committed.
Neither that refusal nor the foreign credential changed the persisted
heartbeat time or observation. The replacement credential could still
report passive state on a disabled node. The same disconnected fixture
passed the read-only claim, inference heartbeat and retained model
preparation scope tests. The result budget and shared audit append remain
separate. With the protected PostgreSQL fixture input, rerun the measurement
using:

```sh
go -C services/control test -p 1 ./internal/modules/nodes -run '^TestComponentHeartbeatCostAtFleetSizes$' -count=1 -v
```

After the scope change, the original seven-exchange mTLS probe reported
43 statements for component claim, 14 for renewal, 43 for the initial
result and 7 for exact result replay on both fleet sizes. Only the initial
result exceeded its declaration of 16. The result still needs a current
lease and deadline check after the owning callback. Any statement reduction
must preserve rollback when that check fails. The audit append remains
under the separate Audit authority.

## ARC-109 training task projection

Training claim, event, and output paths read the exact task under
`FOR UPDATE` and then select its command payload by the same tenant,
workspace, node and task IDs. PostgreSQL's
[locking SELECT](https://www.postgresql.org/docs/18/sql-select.html#SQL-FOR-UPDATE-SHARE)
returns the row version it has locked, so the command can be projected from
that row. Keep scope, operation, profile, catalogue digest and exact lease
validation in place. Keep the separate clock and lease-token check at its
existing point after command decoding. The credential and lease SQL checks
still run once per authority pass, with separate transactions for an upload
before and after streaming.

Limit the change to PostgreSQL. The MySQL and Dameng paths retain their
existing queries. In particular, the [Dameng locking-read documentation](https://eco.dameng.com/document/dm/zh-cn/pm/check-phrases#48-for-update-%E5%AD%90%E5%8F%A5)
describes additional locks when the projection contains a LOB. Caching the
decoded command across transactions was rejected because it can bypass current
authority and database validation. No Agent wire, schema or dependency changes
are needed. The existing PostgreSQL, Go and pgx licences above apply. The
training cost fixture uses synthetic data with the dependencies and terms
listed in [training Agent cost](training-agent-cost.md).

On the existing real HTTPS and PostgreSQL cost fixture, require the same
authority-pass assertions, one fewer statement per task projection, and
unchanged state and result semantics. Exercise training claim with a tampered
command, atomic events and exact replay, and rejection after authority
changes during a chunk stream. Measure the two seeded fleet sizes separately
from simulator throughput and leave the declared budgets in place.

The comparison used PostgreSQL 18.6 with pgvector 0.8.7 and CPython
3.12.15 on Linux arm64 with networking disabled. The baseline was
`10e50c8b`. Both 50- and 3,000-peer fixtures gave the same statement
counts. The existing `training_authority` subtest passed before and
after. No authority-pass assertion or declaration changed.

| Training Agent exchange | Statements before | Statements after |
| --- | ---: | ---: |
| Claim | 69 | 68 |
| Three events | 136 | 135 |
| Input | 33 | 31 |
| Renew | 19 | 18 |
| Stop | 49 | 48 |
| Begin checkpoint upload | 65 | 63 |
| Accept checkpoint chunk | 74 | 72 |

The focused PostgreSQL tests also passed tampered-command rollback,
ordered event replay and revocation, the normal TLS claim and renew
exchange, and refusal after authority changed during a chunk stream.
Claim, event and stop still exceed their declared statement ceilings.
The earlier [training owner measurements](training-agent-cost.md)
used a different baseline. ARC-109 remains open for the shared audit
cost and remaining training and preparation authority changes.
