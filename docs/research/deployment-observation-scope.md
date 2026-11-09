# Deployment observation scope

## ARC-108 first inference heartbeat

The [existing transport trace](agent-claim-authority.md#arc-108-inference-heartbeat-probe)
counts 12 statements for the first mTLS inference heartbeat against an
unchanged ceiling of 11. PostgreSQL evaluates the two omitted column
defaults on the observation upsert. The [cell schema](../../services/control/migrations/postgresql/000030_cell_scope.up.sql)
obtains each default from the local cell. The machine owner already
selected the authenticated node's site and cell in the transaction.

[PostgreSQL INSERT](https://www.postgresql.org/docs/18/sql-insert.html)
documents default evaluation and the atomic conflict update. Supply the
two insert columns from the existing node identity. Keep one upsert,
the scoped node foreign key and the existing conflict predicate.
Update observations and receipt time while preserving previously
stored scope. Reject a missing server-resolved scope before a
PostgreSQL write. The native MySQL and Dameng paths have different
schemas and keep their current SQL. A preliminary scope lookup, a
read-then-write sequence, and a statement ceiling change are unnecessary.

Use [PostgreSQL auto_explain](https://www.postgresql.org/docs/18/auto-explain.html)
with nested statements enabled to compare the same cold mTLS exchange
before and after the change. Exercise the actual HTTPS handler and
certificate checks, confirm the stored site and cell, and measure a warm
update and foreign credential separately. Exercise simultaneous first
writes under PostgreSQL and a retained row that keeps its original scope.
The fixture uses existing Go, pgx and PostgreSQL dependencies and
synthetic hardware metadata. No dependencies, model files or datasets
are added. The existing [Go](https://go.dev/LICENSE), [pgx](https://github.com/jackc/pgx/blob/master/LICENSE)
and [PostgreSQL](https://www.postgresql.org/about/licence/) terms permit
commercial use, offline use and redistribution with the applicable notices.

## Offline verification and ARC handoff

On Linux arm64 with PostgreSQL 18.6 and pgvector 0.8.7, the production
dispatcher answered a real certificate-backed mTLS heartbeat. The same
one-node fixture and nested recorder gave these results.

| Exchange | Statements before | Statements after | Rows before | Rows after | Shared blocks before | Shared blocks after |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Cold first report | 12 | 10 | 16 | 14 | 94 | 86 |
| Warm update | 10 | 8 | 13 | 11 | 40 | 32 |
| Foreign credential | 2 | 2 | 1 | 1 | 3 | 3 |

The original trace contains the two default reads followed by the
scoped foreign-key check and observation upsert. Both default reads
are avoided by the new insert. The scoped conflict predicate, foreign
key and certificate checks stay in place. A separate PostgreSQL test
changed the disposable schema's defaults and submitted simultaneous
first observations in two transactions. Both completed and the one
stored observation used the persisted node's site and cell. A later
write updated a retained row while preserving its original scope.

The existing TLS model-file test had an outdated assertion on the
delivery timeout. It failed on the unchanged baseline too. Its
assertions now distinguish a late delivery from reaching the task
deadline, as required by [ADR 0032](../decisions/0032-static-stability.md).
The production lease path is outside this patch.

Run `TestMutualTLSInferenceObservationCost` and
`TestInferenceObservationKeepsNodeScopeOnConcurrentFirstWrites` in the
deployments module with `PROJECT_ONE_TEST_DSN_FILE` set to a disposable
loopback PostgreSQL database. The first test uses the declared heartbeat
budget of 11 and disables parameter logging. ARC can use this commit for
the shared two-fleet cost run. This evidence covers one node and the
three listed transport exchanges.

## Deployment-bound heartbeat

A deployed model's report also reads the owning deployment and updates
its current revision. The transport budget still applies after a Control
restart when the PostgreSQL receipt and deployment survive but the
pacing and liveness caches start empty. Measure this case separately
from a new hardware row. Reuse the same production HTTPS dispatcher,
certificate, installed deployment and nested recorder. A current report
must update the exact deployment without moving the GPU reservation.
An older revision must not overwrite the retained deployment observation.
The code paths and wire contract are unchanged unless the measurement
finds a new excess.

The actual deployment service committed an intent and the node task
completed with a protocol fixture observation. After restarting the
dispatcher over the same database, the first current-revision mTLS
report used **11 statements, 15 rows and 44 shared blocks**. It updated
the stored observation and retained the GPU reservation. After an
authorized stop advanced the revision, the delayed report used **9
statements, 12 rows and 35 shared blocks** and left the retained
deployment observation and reservation intact. These are real
PostgreSQL and TLS exchanges with synthetic GPU and runtime metadata.
