# Durable usage ledger

## Upstream practice

TEN-106 records usage attributable to the authenticated tenant and workspace.
[CloudEvents](https://github.com/cloudevents/spec/blob/main/cloudevents/primer.md#id)
identifies an occurrence with a source and an immutable event ID.
[OpenMeter](https://openmeter.io/docs/metering/overview) applies usage events to
subjects and separates collection from aggregation. [Slurm accounting](https://slurm.schedmd.com/accounting.html)
records job usage against associations. [OpenTelemetry's metrics model](https://opentelemetry.io/docs/specs/otel/metrics/data-model/)
distinguishes delta and cumulative measurements and explains resets.

## Transaction boundary

Store each measured delta once under a trusted producer and event ID. The
producer persists the canonical event identity and contents with its source
transition or retains a durable outbox entry until successful acknowledgement.
The ledger's unique key arbitrates concurrent writers. An identical replay is
a successful no-op, and any change to attribution, occurrence time or quantities
is a conflict. Do not reserve a deduplication key in a separate transaction or
use a TTL. PostgreSQL documents [conflict arbitration](https://www.postgresql.org/docs/18/sql-insert.html)
and [the next statement's READ COMMITTED snapshot](https://www.postgresql.org/docs/18/transaction-iso.html).
Use synchronous commit for accepted ledger writes because
[asynchronous commit](https://www.postgresql.org/docs/18/wal-async-commit.html)
can acknowledge changes lost during a crash.

The source provides measured deltas in whole GPU microseconds, tokens, storage
byte-seconds and requests. A producer measures duration from an authoritative
counter and survives restarts by persisting the event before acknowledgement.
The ledger never derives usage by subtracting wall-clock timestamps and never
uses them as a deduplication cursor. Record a stable occurrence timestamp with
microsecond precision for reporting. A wall-clock regression can reorder
occurrences and cannot erase or duplicate a committed event. Reporting sums
integer values in PostgreSQL numeric arithmetic over half-open time windows.

Metrics backends are suited to operational estimates. They cannot share a
transaction with source ownership state. A separate ingestion broker and
deduplication cache add two failure boundaries for P1. A timestamp cursor
would discard valid events following a clock reset. The first acceptance path
uses the existing PostgreSQL transaction, with no external service. Producer
changes remain necessary for events the ledger never receives.

## Licensing

Use the existing [pgx](https://github.com/jackc/pgx/blob/master/LICENSE) (MIT),
[Go](https://go.dev/LICENSE) (BSD-3-Clause), [UUID](https://github.com/google/uuid/blob/master/LICENSE)
(BSD-3-Clause) and [PostgreSQL](https://www.postgresql.org/about/licence/)
(PostgreSQL License). Their notices permit commercial redistribution and
offline use. OpenMeter, Slurm and the telemetry specifications are references
only. No model, dataset or dependency is added.

## Acceptance

Prove concurrent duplicate ingestion, conflicting replay, scoped totals,
multi-metric atomicity, lost commit reply, connection loss before commit and
backward event time against a real PostgreSQL server with no external route.
Test totals beyond 64 bits and refusal of negative quantities. The SQL in the
module's test fixture qualifies transaction behavior. The product item also
requires the production migration and grants, authorized producer hooks and
operator access with an end-to-end usage reconciliation.

## Gateway receipt intake

The distribution service's Report operation authenticates the machine
credential, locks the authorized workspace and its exact admitted request,
checks the admission's token bounds, and stores the canonical v1 outcome
with its SHA-256 and audit event in one transaction. Report replay uses the
same request ID and digest. The request row retains the tenant, workspace,
gateway and original server-side admission time. The outcome itself carries
no occurrence timestamp or scope. See the [gateway contract](../../api/gateway/v1/protocol.go)
and the [receipt verification evidence](gateway-usage-history.md).

Accept the same scoped identity and receipt acknowledgment in the borrowed
PostgreSQL transaction. Read the exact reported row, verify its persisted
digest against the acknowledgment and recompute the digest with the existing
strict gateway codec. Keep the canonical request ID as the event ID under a
fixed gateway producer. Use the persisted admission time for both request
and token rating. Report arrival can be delayed, and replay must not move a
request across rate cutovers. The admission time is a stable accounting
anchor for the whole request. It does not estimate token generation time.
Keep credentials, identity from request bodies, and client quantities out of
the intake. A history or CSV export cannot share the source transaction and
does not supply the machine's verified scope and digest at this boundary.

A reported request counts once. Null usage preserves the request count and
increments the unmeasured-token request count. Only a verified non-null
receipt supplies token totals. A valid zero completion count still leaves
known prompt and total tokens. The v1 wire contract rejects zero prompt
tokens, so a zero total is not a valid gateway receipt. Preserve these
distinctions in the report and budget. An unmeasured request makes the
known subtotal incomplete even when all observed quantities have rates.
A known subtotal at or above the limit is still sufficient to flag the
limit. The [OpenTelemetry token convention](https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/)
also distinguishes input and output counts and defines the token metrics
around available usage. It is a reference, with no runtime dependency.

Prove scoped receipt retry, altered digest and foreign-scope refusal,
atomic rollback, valid zero completion, null usage and an older admission
delivered after the rate cutover in disconnected PostgreSQL. Compare the
budget with the report from the same snapshot. The production call belongs
in distribution Report after canonical persistence or replay verification
and before the outer transaction returns success. The database owner must
ship the ledger, including the unmeasured-request count. Historical
reconciliation and coverage of requests without a report remain prerequisites
to budget enforcement. This path uses the existing gateway codec, pgx,
UUID and PostgreSQL under the project and dependency licenses above.

## Unnumbered PostgreSQL migration handoff

[ADR 0006](../decisions/0006-cell-and-site-scope.md) and the installed
[scope migration](../../services/control/migrations/postgresql/000030_cell_scope.up.sql)
make the database's local site and cell the authority for cell-owned rows.
Give the candidate ledger both non-null columns and the same database
defaults. Put site, cell, tenant and workspace before time in the reporting
index. Retain the unique producer and event key so an existing charge in a
different scope conflicts instead of being counted again.

Preserve the `testdata/ledger.sql` path and the four inputs to
`AppendGatewayReceiptWithin` used by the optional Report transaction in
`eeb0e9a6`. Report owns machine authorization. The metering reader requires
a reported gateway row matching that gateway, workspace, tenant, local site
and local cell. It keeps the source location with the verified receipt
and checks it again in the insert statement. These checks use the
database's location functions, with no request-supplied site or cell.
[Read Committed statement snapshots](https://www.postgresql.org/docs/18/transaction-iso.html#XACT-READ-COMMITTED)
explain why a separate read alone would leave a gap if the local registry
changed before insertion. The write keeps the established receipt row lock
and needs no additional privilege on the cell registry. Scoped totals and
showback filter both coordinates, including when a restore contains rows
from another cell.

The unnumbered up file creates a ledger on the established predecessor.
It leaves existing workspace and gateway history in place. Verified
predecessor receipts can be projected through Report replay after the
production owner enables the hook. The down file takes the ledger's
exclusive lock and refuses a nonempty table, keeping accrued usage durable.
Run the down file within a transaction. The
[PostgreSQL locking contract](https://www.postgresql.org/docs/18/sql-lock.html)
keeps the emptiness check and removal protected from concurrent writes.

Qualify native schema 55 through the current canonical migration, then apply
the candidate in a disposable schema. Prove predecessor data survives,
the numbered lineage stays intact, local replay is a single event, and an
occupied ledger cannot be removed by the down migration. Exercise a
nondefault local site and cell, a valid foreign gateway receipt, and a
foreign ledger row sharing the same tenant and workspace. Verify that
foreign rows neither enter the ledger nor affect reports. Re-run the
gateway owner's optional TLS Report regression against these same SQL bytes.

ARC owns the formal number, table count, grant verification and accepted
fresh and restored fingerprints. The published table and migration ledger
stay at the existing lineage until ARC integrates the candidate. The
gateway owner enables receipt projection only with the accepted schema.
Budget enforcement remains separately gated on an authorized effective
policy and complete and pending usage. This uses the existing Go, pgx and
PostgreSQL stack under the licenses recorded above.

An isolated PostgreSQL 18.6 run also exported a real version 55 fixture
with `pg_dump -Fc` and restored it into a new test database with pgvector
0.8.7. The verification overlay substituted that restored pool for the
source pool in `TestPostgresLedgerCandidateUpgradesFromSchema55`. The test
then ran the canonical upgrade and readiness check, applied the same
unnumbered SQL and retained the old workspace and original migration
checksums. One append and one replay left a single event. Refusing the
nonempty down migration preserved the event. The archive, restore and
test ran inside the owned disconnected database fixture.

## Serving principal and ledger grants

The installed PostgreSQL role comes from
[prepare-runtime-access](../../services/control/internal/operations/databaseaccess/access.go).
Its current grant catalog applies table-level SELECT, INSERT, UPDATE and
DELETE to ordinary business tables. The
[serving privilege check](../../services/control/internal/platform/database/runtime_privileges.go)
requires those grants for ordinary tables and rejects table ownership,
TRUNCATE, trigger administration and grant options. A ledger-specific
least-privilege policy therefore needs a matching exception in ARC's
grant provision, upgrade inspection and readiness contracts. The
unnumbered candidate can be qualified with the current role and the
ordinary grant for the new table without admitting the candidate catalog.

The metering statements need SELECT on the ledger and INSERT on only
`producer`, `event_id`, `tenant_id`, `workspace_id`, `occurred_at`,
`gpu_microseconds`, `tokens`, `storage_byte_seconds`, `requests` and
`unmeasured_token_requests`. Existing schema USAGE, EXECUTE on
`local_site_id()` and `local_cell_id()`, and SELECT on the three columns
they read in `cells` remain prerequisites. Reading the trusted gateway
receipt uses existing SELECT on `gateway_requests` and its row-lock
authority. PostgreSQL's [SELECT contract](https://www.postgresql.org/docs/18/sql-select.html)
requires UPDATE on at least one selected table column for FOR SHARE.
The gateway owner already holds the full permission for its Report
transition. No additional cell-registry write grant is required for
metering. The [GRANT contract](https://www.postgresql.org/docs/18/sql-grant.html)
allows restricting INSERT to the listed columns.

Keep the statement-level append-only guard for the existing broad role.
Check each new row's coordinates against the database-local authority
before insertion, including explicit SQL or COPY values. The
[trigger contract](https://www.postgresql.org/docs/18/trigger-definition.html)
keeps this invoker-rights check in the same transaction. Resolve the
cell registry in the ledger table's own schema, so a session's temporary
table cannot substitute coordinates. The ledger's select predicates
continue to isolate retained rows from other cells.
Current Control credentials are a trusted service principal with existing
access to the cell registry and other business state. Application
authorization and verified receipt identity remain mandatory before
using them. A runtime credential with broad Control access cannot stand
in for a caller's authority.

On disconnected PostgreSQL 18.6, the installed SCRAM role appended,
replayed and read with both grants. The test refused a wrong password,
an ungranted principal, explicit foreign scope, a shadowed local lookup,
mutation, TRUNCATE, trigger disable and replication-mode bypass. The
unnumbered candidate still failed catalog admission. The optional GW
Report transaction from `eeb0e9a6` ran in a source overlay with the same
candidate SQL, authenticated receipt input and installed role. Under
the narrower grant it appended and replayed an authenticated receipt.
Invalid credentials and a persisted foreign-cell source produced no
successful acknowledgment or ledger entry. Candidate DDL and fault
injection ran only as the fixture owner.

Before a rollback, quiesce the Report projection and other ledger
writers. ARC must verify the exact predecessor schema and runtime grant
contract and run the down SQL under the installation lock in a
transaction. The exclusive table lock protects the empty-ledger
precondition. An occupied ledger requires a compatible roll-forward or
a separately verified recovery that preserves its records. This uses
the existing Go, pgx and PostgreSQL stack and its recorded licenses.
