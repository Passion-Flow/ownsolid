# Component log settlement (OPS-104)

The existing node-exporter log endpoint gives platform administrators a bounded
snapshot. It fetches newest-first from VictoriaLogs and checks task settlement in
the control-plane database. This is the existing component path that can be
strengthened inside the OPS lane. The live hub, per-user budgets and other log
sources remain integration work.

[VictoriaLogs LogsQL](https://docs.victoriametrics.com/victorialogs/logsql/)
supports filtering by stream labels and limiting a newest-first result. This
bounds the candidate list, while [PostgreSQL transaction visibility](https://www.postgresql.org/docs/18/transaction-iso.html)
keeps uncommitted task state outside the reader's snapshot. The
[1Panel documentation](https://1panel.pro/docs/v2/) is a reference for giving
operators access to logs from the same product. Ownsolid uses its own log store,
HTTP endpoint and node lifecycle tasks.

Only a succeeded node-exporter logs task can authorize a body. Only a succeeded
node-exporter remove task can authorize a tombstone. Compare the exact
server-derived stream scope before looking up the task. Keep the existing read
transaction and bounded scan. Using a bare successful task ID conflates distinct
operations on the same node. Serving the store result directly would drop the
settlement and authorization check. Keep the API and store contracts in place.

A record of the wrong kind, foreign scope, or uncommitted or unrelated task must
be skipped, allowing the next eligible snapshot to be read. A genuine removal
must still hide preceding snapshots. Exercise these cases on the actual
PostgreSQL task repository through the component service. The record gate binds
scope and action. It does not attest the payload bytes or distinguish multiple
writes for the same task after a transaction retry. The live hub needs a
committed source cursor or content reference if it consumes pre-commit writes.

Existing dependencies are Go's [BSD-3-Clause standard library](https://go.dev/LICENSE),
[PostgreSQL](https://www.postgresql.org/about/licence/) and
[VictoriaLogs Community](https://github.com/VictoriaMetrics/VictoriaLogs/blob/master/LICENSE)
under Apache-2.0. Their terms allow commercial and offline use and redistribution
with the respective notices. The existing node-exporter integration fixture uses
the pinned [Apache-2.0 release archive](https://github.com/prometheus/node_exporter/blob/master/LICENSE).
This increment adds no dependency, model, dataset or runtime download.

The current component slice passed five selected tests using dedicated
loopback PostgreSQL 18.6 with pgvector 0.8.7 and VictoriaLogs 1.53.0.
The real-store test covered queue write, readback and removal. The task
repository retains a hash of the full completion, including its lease
token. For exact payload custody, the task and log-store owners need
either a committed log content reference that excludes credentials, or
a supported after-commit publication hook with delivery retry. The
component snapshot can consume either contract once it exists. Live
sources, per-user budgets and console streaming still require their
own authorized integration.
