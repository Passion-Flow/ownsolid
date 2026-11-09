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

The one-node mTLS gate is green. ARC-108 still needs deployment-bound
observations and component, preparation and Registry operations in the shared
two-fleet cost suite. No declaration or fleet acceptance result has changed.
