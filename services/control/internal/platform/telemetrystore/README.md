# Telemetry stores

Clients for the dedicated stores of [ADR 0009](../../../../../docs/decisions/0009-telemetry-and-log-stores.md).
Telemetry and logs never enter the control-plane database.

- [Client](client.go) writes to and reads from the log store over its HTTP API. A query
  always carries the tenant, workspace and node scope the caller was authorized for.
- [MetricsPusher](metrics.go) sends process values to the metrics store every
  `metrics_push_interval_seconds`. They are current readings, so a failed push is counted
  and not retried; the log shows when the store stops and starts answering.
- [Writer](writer.go) moves records off the caller's path through a bounded queue, so a
  SQL transaction never waits on the network. A full queue drops the record, counts it
  and logs it; the store being down never fails or delays the work that produced it.

A dropped or unwritten record is lost by design: telemetry is not a system of record.
Anything that must survive belongs in PostgreSQL, such as audit events.

A record can outlive the transaction that produced it, because the queue cannot see a
rollback. PostgreSQL stays the authority: a reader shows a record only after confirming
what it refers to. Component logs show a record only if its task settled as succeeded.
New records become visible to queries about a second after they are written.
