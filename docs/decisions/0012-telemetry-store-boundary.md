# 0012: The boundary between PostgreSQL and the telemetry stores

- Status: accepted
- Date: 2026-11-04

## Context

OBS-002 moves telemetry out of the control-plane database into the stores of
[ADR 0009](0009-telemetry-and-log-stores.md). The first data to move is the component log
snapshot, which an agent returns as a task result inside the transaction that settles
the task. That transaction belongs to the node module. An HTTP call to a store inside it
would hold a database connection on the network, and a store outage would fail task
settlement. Existing tests also require that a settlement which rolls back leaves no
visible snapshot.

## Decision

**Telemetry is not a system of record.** A store outage never stops the platform, and
`control` has no startup dependency on a store. Anything that must survive belongs in
PostgreSQL. Audit events and task events stay there.

**Writes leave the transaction through a bounded queue.** `telemetrystore.Writer` takes a
record without blocking and delivers it in the background, with bounded retries and a
bounded drain at shutdown. A full queue drops the record, counts it and logs it.

**PostgreSQL decides what is visible.** The queue cannot see a rollback, so the store can
hold a record whose settlement never committed. A reader confirms each record against
`node_tasks` and shows the newest one whose task succeeded, looking back over 16 records.
A tombstone follows the same rule, which gives removal without a delete operation.

A post-commit hook in the transaction layer was the alternative. It would touch every
settlement path in the node module for one consumer, and a process crash between commit
and hook would still lose the record, so it gives no stronger guarantee.

**Metrics are pushed, not scraped.** `control` sends its process values to the metrics
store on a fixed interval. A scrape would need the store to reach into control and would
add a metrics listener to secure. Values are current readings, so a failed push is counted
and not retried: the next one carries newer values.

**No service looks a store up by name.** Each store has a fixed address on the internal
`state` network ([ADR 0008](0008-zero-egress-harness.md)).

## Consequences

- A snapshot can be lost if control stops before delivering it, or if the store is down
  past the retries. The administrator runs the logs action again.
- A new snapshot becomes readable about a second after it is written.
- Reading logs while the store is down returns `STORAGE_UNAVAILABLE`, which is retryable.
- Tenant scope is a field on each record, added from the server's own node record and
  checked again on every record read back. The store itself has no tenant boundary, so
  nothing but the control plane may reach it: the `state` network stays internal.
