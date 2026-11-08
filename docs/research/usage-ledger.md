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
