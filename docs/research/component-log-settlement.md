# Component log settlement (OPS-104)

The existing node-exporter log endpoint gives platform administrators a bounded
snapshot. It fetches newest-first from VictoriaLogs and checks task settlement in
the control-plane database. The existing Admin route and agent task already
provide a source for bounded, on-demand reads.

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
sources and console streaming still require their own authorized integration.

## Bounded Admin reads

Use one active snapshot read per authenticated user per control process.
Key by the server's user identity so a second session or node cannot
multiply its share. Refuse a concurrent read with the existing
`RESOURCE_LIMIT_REACHED` problem. Release the slot when the read exits.
Pass the request context and existing HTTP request deadline through the
store and settlement reads. Keep the existing limit of 16 candidate
records. The database transaction ends before the store request starts.
After that request, repeat the ordered user, session and node scope
check even when the log store returns no records. A committed role
revocation during a store request must refuse the response.
[Go request cancellation](https://pkg.go.dev/net/http#Request.Context),
[PostgreSQL read visibility](https://www.postgresql.org/docs/18/transaction-iso.html).

VictoriaLogs supports live tailing but its stream does not encode our
task settlement or current Admin authority. Keep the existing snapshot
route. A resumable tail needs a committed, scoped log cursor or content
reference from the task producer and a registered streaming contract
that revalidates authorization through delivery. Opening the store's
tail directly would expose pre-commit records and bypass those checks.
[VictoriaLogs live tailing](https://docs.victoriametrics.com/victorialogs/querying/#live-tailing).

Use a loopback HTTP proxy to hold an actual VictoriaLogs request while
the real PostgreSQL identity fixture remains authoritative. Exercise
capacity across requests, cancellation and reuse, a bounded response,
and role revocation while both populated and empty store reads are
pending. Verify the request cancellation reaches the store transport.
The increment uses the existing Go, PostgreSQL and VictoriaLogs
dependencies and their terms above.

The bounded-read test passed on native Linux amd64 with PostgreSQL 18.6,
pgvector 0.8.7 and VictoriaLogs 1.53.0 sharing a loopback-only network
namespace. The Admin route used a settled synthetic journal result.
A second session for the same user hit the limit, the canceled request
released its slot, and the proxy observed transport cancellation.
Role revocation during populated and empty reads returned no snapshot.
The existing queue, readback and removal test passed in the same run.
