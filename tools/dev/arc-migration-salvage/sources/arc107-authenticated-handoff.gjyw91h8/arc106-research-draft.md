## Publishing fleet KPIs (ARC-106)

Reviewed 2026-11-13. A published measurement must name its source revision, actual
agent count, pacing, topology, resources, measurement windows and failure verdict.
Use the existing TestFleetLoad and simulator, without changing their timeline,
objectives or assertions. A 500-agent lifecycle drill is not the 2,000-agent load
baseline, and neither establishes the planned 10,000-node multi-cell claim.

### Sources and measurement meaning

[Kubernetes scalability SLIs/SLOs](https://github.com/kubernetes/community/blob/main/sig-scalability/slos/slos.md)
make performance conditional on configuration and load. Retain those conditions
and the fixed objectives from this note. Do not turn a single benchmark into an
unconditional service guarantee.

[PostgreSQL cumulative statistics](https://www.postgresql.org/docs/18/monitoring-stats.html#MONITORING-PG-STAT-DATABASE-VIEW)
distinguish committed transactions from inserted, updated and deleted rows.
Publish each rate separately. Database row modifications per second means the
sum of those three row-counter deltas divided by the actual sample interval;
it is not storage IOPS, SQL statement count or committed business objects.
Counters can lag until a backend becomes idle, and a statistics reset invalidates
a delta. Samples use separate short connections; no long cached transaction.
Record any reset/restart and do not publish a negative or discontinuous rate.

[PostgreSQL WAL functions](https://www.postgresql.org/docs/18/functions-admin.html#FUNCTIONS-ADMIN-BACKUP)
define LSN differences as bytes. The existing test records current-WAL-LSN
movement for the entire isolated PostgreSQL cluster. It includes maintenance
and other databases in that cluster and is not a per-agent or per-database
physical write metric. The test logger divides by 1,024; label that value KiB/s,
although the retained historical raw output says KB.

[Prometheus histograms](https://prometheus.io/docs/practices/histograms/)
distinguish bucket estimates from exact observations and prohibit averaging
precomputed percentiles. Keep every one-minute steady heartbeat window and its
failure/count verdict. The simulator reports an exponential-bucket upper bound
with ratio 1.25, rounded to 0.1 ms. The dispatch statistic is separate: PostgreSQL
percentile_cont(0.99) over persisted completed_at-created_at for the retained
steady-task IDs, in seconds, including execution and result delivery. It is not
an isolated scheduler admission latency. Publish its task count and median/max.

Full disconnect/partition recovery measures every affected agent's first good
heartbeat, with a partition timed from healing. The control-restart measurement
starts before the compose restart and waits for persisted last_heartbeat_at on
the idle agents. Busy agents are separately proved by the exact first-attempt
and acknowledgement assertions and stream deadline. Never rename idle-agent
recovery to full-fleet recovery.

### Chosen path and alternatives

Run the unchanged 25-minute 2,000-agent fixture on native Linux amd64 with its
5-second heartbeat, ten-minute steady stretch and existing fault schedule.
Retain complete raw logs, simulator report, source/binary/image identities,
recorder probes, resource samples and cleanup result. Publish a reader-facing
operations page and link it from the owned roadmap/research evidence. Reuse the
native immutable ARC-107 binaries only with their complete input identity and
fresh current web assets; do not claim the standard release factory was tested.

Reject extrapolation from a small fixture, averaging heartbeat p99 values,
calling xact_commit writes, mixing whole-run storm averages into steady rates,
and changing objectives after a failure. A failed run remains in the report and
requires diagnosis; it cannot be accepted by omitting its failing window.

### Dependencies and licenses

No dependency, model or dataset is added. The test generates synthetic identities
and facts. Existing Go and its standard library are BSD-3-Clause; gRPC-Go 1.85.0
is Apache-2.0, protobuf-go 1.36.12 is BSD-3-Clause and x/net is BSD-3-Clause.
[PostgreSQL](https://www.postgresql.org/about/licence/) and
[pgvector 0.8.7](https://github.com/pgvector/pgvector/blob/v0.8.7/LICENSE) use the
PostgreSQL License; the existing [CPython fixture](https://docs.python.org/3.12/license.html)
uses PSF-2.0. Existing container images
and their exact IDs remain those approved by the offline harness; retain their
upstream notices and existing delivery inventory. Commercial use, redistribution
with applicable notices and offline execution are permitted for these sources;
this research introduces no new artifact into the shipped inventory. The pinned
PostgreSQL 18.6/pgvector 0.8.7 fixture is the measured version, irrespective of a
newer available upstream patch.

### Failure coverage and acceptance

Require all existing TestFleetLoad assertions unchanged: no failed steady
heartbeats/claims, all 2,000 connected per steady window, heartbeat p99 at most
1 s, dispatch p99 at most 5 s, each measured recovery within 300 s, every busy
task completing on its original acknowledged attempt, all 2,000 back on streams
by the existing deadline and at the terminal report, and no unexpected control
restart, out-of-memory kill or liveness/outbox failure. Preserve recorder proof
of zero product egress and at least one observed fixture probe. A statistics
reset, missing sample, incomplete report or early cleanup prevents publication
of the affected KPI. Full repository gates are independently required.

The actual new baseline and its publication remain pending; this section is a
measurement specification, not a successful run result.
