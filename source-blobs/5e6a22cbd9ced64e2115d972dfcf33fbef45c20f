# Fleet load test

Research behind ARC-015: what "load tested with 2,000 simulated nodes at twice the planned
load" means, how it is judged, and what it does not prove. ARC-105, ARC-203 and ARC-304 repeat
it at larger sizes; ARC-106 publishes the numbers.

## How others judge scale

**Kubernetes** states scale as a promise with conditions: "If you promise to: correctly configure
your cluster / use extensibility features 'reasonably' / keep the load in the cluster within
recommended limits, then we promise that your cluster scales, i.e.: all the SLOs are satisfied."
Its indicators are "measured as 99th percentile over last 5 minutes", and the objective counts
good minutes per day: mutating API calls "99th percentile per cluster-day <= 1s", and stateless pod
startup "99th percentile per cluster-day <= 5s".
([SLIs and SLOs](https://github.com/kubernetes/community/blob/master/sig-scalability/slos/slos.md))

Its published limits are soft: "thresholds are NOT hard limits - crossing the limit results in
degraded performance", and they hold for a stated configuration.
([Thresholds](https://github.com/kubernetes/community/blob/master/sig-scalability/configs-and-limits/thresholds.md))

**ClusterLoader2**, the official test framework, declares desired states and how fast to reach
them, and encodes the objectives as measurements: `APIResponsivenessPrometheus` "verifies if API
call latencies SLO is satisfied", `PodStartupLatency` does the same for startup, a barrier that
times out marks the test failed, and `ResourceUsageSummary` fails it when a usage constraint is
violated.
([ClusterLoader2](https://github.com/kubernetes/perf-tests/blob/master/clusterloader2/README.md))

Three things carry over: thresholds are fixed before the run, a percentile is judged per window so
a good hour cannot hide a bad minute, and the result is stated with its configuration.

## The load

The planning load is one liveness update per node every 10 seconds. Twice that for 2,000 nodes is
a 5 second interval, the shortest the protocol allows, set through the pacing API: 400 heartbeats
and 400 claims a second, against an installation with default resources (control on 2 CPUs and
1 GB, PostgreSQL with a weight of 2 CPUs and 2 GB; the first three runs below had a quota of
2 CPUs). A run from one host raises the same limits as the simulator's own test; each is named in the [simulator research](agent-simulator.md).

## Thresholds

Fixed before the first run. A miss is a product finding to fix, not a number to move.

| # | Measure | Threshold | Source of the number |
| --- | --- | --- | --- |
| 1 | Every one-minute window of a ten-minute steady stretch | No failed heartbeat or claim; all 2,000 connected | P0 exit: "steady" |
| 2 | Heartbeat latency in each of those windows | p99 at most 1 s | Kubernetes mutating call objective |
| 3 | Task dispatch, from creation to a finished `collect_facts.v1`, on a steady fleet | p99 at most 5 s | Kubernetes pod startup objective |
| 4 | Full reconnect after all 2,000 lose their connections, and after a control restart | Every agent back within 300 s | P0 exit |
| 5 | Tasks running through a control restart | Every one succeeds on its first attempt | P0 exit: a restart "does not stop running work" |
| 6 | Control during the run | No restart or out-of-memory kill, no liveness flush or outbox failure logged | ADR 0022, ADR 0028 |
| 7 | Transport after a control restart | Every agent back on the stream within three minutes | Added after the first run; see below |

Database writes, WAL volume, table growth and resource use are recorded without a threshold.
ARC-106 publishes them once a measured baseline exists.

## How it runs

`TestFleetLoad` in `tests/e2e` runs in the same isolated host as the install test, selected by
`OWNSOLID_E2E_RUN=TestFleetLoad`:

1. Install with the raised limits, set the 5 second pacing, enroll 2,000 agents.
2. Steady stretch: seconds 60 to 660, with a batch of 100 tasks every minute.
3. Disconnect all agents, then partition 30% for a minute.
4. Start 100 tasks that run for 90 seconds, then restart control while they run.
5. A last stretch in which the fleet settles.

The simulator's report gains one-minute windows, so threshold 1 and 2 read the steady stretch
alone. Long tasks renew their leases and deliver their results as the agent does, including
through the outage ([static stability](static-stability.md)).

## What the runs showed

Three runs on `ownsolid-cpu` on 2026-11-07, with the configuration above.

| Threshold | Run 1 | Run 2 | Run 3 |
| --- | --- | --- | --- |
| 1, 2: steady windows | Met: no failure, worst window p99 212 ms | **Missed**: windows ending at 240, 300 and 360 s shed 3,246, 591 and 431 heartbeats with `AGENT_OVERLOADED`, p99 up to 3.1 s | Met |
| 3: dispatch p99 | 1.24 s | 1.34 s | 1.23 s |
| 4: full reconnect | disconnect 3.4 s, partition 59.5 s after the heal, control restart 17.7 s | 3.7 s, 58.9 s, 29.4 s | Met; control restart 11.2 s |
| 5: tasks through the restart | 100 of 100 on the first attempt | 100 of 100 | 100 of 100 |
| 6: control | No restart, no logged failure | No restart, no logged failure | Met |
| 7: back on the stream | **Missed**, which added this threshold: 1,324 agents stayed on HTTPS for five minutes after the restart | Met after the fix below | Met |

Steady stretch, per second: about 2,800 commits, 410 row updates (the batched last-seen writes
of ADR 0022) and 310 KB of WAL. Control used about one CPU of its two, PostgreSQL one of its two.
The simulator process used 395 MB and a quarter of a CPU for 2,000 agents.

**Fixed: a control restart moved agents off the stream.** An agent whose stream failed once while
HTTPS answered took the path to lack HTTP/2 and stayed on HTTPS for five minutes. A restart is
exactly that sequence, so two thirds of the fleet long-polled through the rest of the run. The
agent now leaves the stream only after `SessionFallbackProbes` (two) such failures in a row
(ADR 0007), and the simulator does the same.

**Open: shedding in a steady stretch.** Run 2 shed heartbeats for three minutes with nothing
injected; runs 1 and 3 did not. The liveness state refresh is not the cause: its synchronous
writes are already spread over half the refresh, about 13 a second.

Run 3 sampled every five seconds and pointed at **CPU quota throttling of PostgreSQL**. The
installer limited the database container with `cpus: "2"`, a CFS quota. In the steady stretch
PostgreSQL averaged 0.92 CPU yet reported up to 2.0 s of throttling in a 5 s sample (p90 0.8 s),
and 6.2 s around the disconnect storm. `throttled_usec` adds up across CPUs, so these are not
wall-clock seconds: the prototype below read more than 1,000 ms of it per second. Control, whose
Go runtime sizes itself to its quota, reported at most 42 ms.

A prototype on `ownsolid-cpu` isolated the limit from the product: the product's PostgreSQL image
with 10 clients at 2,000 steady writes a second, plus heavy scans that stand in for the bursts.
The scans are an assumption about the burst's shape; which real query bursts is not known.

| Limit | At 1.2 CPU: p99, max | At 1.7 CPU: p99, max |
| --- | --- | --- |
| Quota, 100 ms period | 264 ms, 990 ms | 1,623 ms, 2,011 ms |
| Quota, 10 ms period | 165 ms, 376 ms | 2,341 ms, 2,711 ms |
| Quota, pool of 2 | 596 ms, 903 ms | 3,090 ms, 3,491 ms |
| Two pinned cores | 39 ms, 114 ms | 117 ms, 220 ms |
| No limit | 1.5 ms, 17 ms | 1.8 ms, 17 ms |

With a CPU-bound neighbour on the same four cores, a weight of 2,048 held p99 at 9 ms, the
default weight at 40 ms and the old quota at 33 ms; with no neighbour on those cores, 4.1 ms
against 110 ms. A stall of two seconds is what control's seats wait before shedding, so the old
quota can produce run 2's symptom. [ADR 0038](../decisions/0038-database-cpu-weight.md) replaces
the quota with a weight.

Two runs on 2026-11-07 with the weight confirmed the first part and found a second problem.

| Measure | Weight, run 1 | Weight, run 2 |
| --- | --- | --- |
| PostgreSQL throttled in the steady stretch | 0 | 0 |
| Failed heartbeats and claims in the steady stretch | 0 | 0 |
| Worst steady heartbeat p99 | 414 ms | **1,010 ms, a miss of threshold 2** |
| Dispatch p99 | not kept | 1.38 s |

**Open: a stall of about a second.** Run 2's window ending at 660 s holds it. Every other steady
window of that run was at most 6 ms. The sample ending at 606 s shows control down from 0.87 CPU
to 0.64 and PostgreSQL from 0.93 to 0.78, so work stopped for about 1.3 s across both, with no
throttling, checkpoint start, autovacuum or task batch at that moment. Run 1's windows reached
414 ms without a visible dip. The reported p99 is a bucket's upper bound, within a quarter of the
true value, so the true figure may be just under 1 s; it still counts as a miss.

Those samples could not place the stall, so the runs now record the simulator's own timer lag per
window (`generator_lag_max_milliseconds`), and per sample the host's pressure stall times and the
test process's timer lag. Three more runs with those probes:

| Measure | Probe run 1 | Probe run 2 | Probe run 3 |
| --- | --- | --- | --- |
| Result | Pass | **Fail: thresholds 1 and 2** | Pass |
| Worst steady heartbeat p99 | 517 ms | 2,465 ms | 517 ms |
| Failed steady heartbeats, claims | 0, 0 | 2,176, 1,717 | 0, 0 |
| Dispatch p99 | 1.34 s | 3.65 s | 1.33 s |
| Longest steady generator lag, test process lag | 3 ms, 4 ms | 9 ms, 10 ms | 4 ms, 5 ms |
| Host IO wait, worst 5 s sample | 666 ms | 2,531 ms | 357 ms |
| PostgreSQL throttled | 0 | 0 | 0 |
| Full reconnect after a control restart | 16 s | 18.5 s | 14.6 s |

**Probe run 2 had disk load beside it, some of it ours.** Its refusals ran from second 480 to 660,
22:51:45 to 22:54:45 UTC. Host IO wait rose at 22:51:36 and peaked at 22:53:11, with control and
PostgreSQL down to 0.3 CPU, while a `du` over a large tree, run by hand during the test from about
22:51:07, lasted until about 22:53. Memory pressure followed from 22:54:36 during another session's
image build, and another project's test suite ran on the host throughout. The generator and the
test process stayed on time, so the delay was in control or below it. The IO wait points at the
database's disk; no sample proves that, and the timing is the only link to the `du`.

The run still counts as a failure. The install's data lives under `/srv/ownsolid`, in the isolated
container's own filesystem on the host's disk, so PostgreSQL's WAL syncs compete with whatever else
uses that disk; only `/var/lib/docker` inside it is in memory. Shedding is the designed response to
a slow database, and every agent came back by the end of the run.

**Fixed: one slow WAL sync emptied the connection pool.** Two later runs missed with almost nothing
beside them, so the disk was not the whole story. PostgreSQL's slow statement log, added to the run,
showed nine `commit` statements ending at one instant every 30 to 60 seconds, the longest after
2,176 ms. An idle-host trace of synchronous 8 KB writes gave a median of 0.16 ms and a p99 of
0.36 ms, with 16 stalls over 50 ms in ten minutes and a longest of 463 ms, so any stall of that size
is normal. The stalls did not follow checkpoints.

The log names no caller, so the waiters are inferred from the code: an agent call locks its node row,
the lock gives the transaction an ID, and so a claim that finds no work waits for the disk at commit,
about 400 times a second. Making only the
heartbeat's repeat writes asynchronous did not help: one run of three still missed. Letting any agent
call that changed nothing commit without waiting did ([ADR 0040](../decisions/0040-agent-calls-that-change-nothing-do-not-wait-for-the-disk.md)):

| Measure | Fix run 1 | Fix run 2 | Fix run 3 |
| --- | --- | --- | --- |
| Result | Pass | Pass | Pass |
| Worst steady heartbeat p99 | 14.6 ms | 807.8 ms | 3.8 ms |
| Failed steady heartbeats, claims | 0, 0 | 0, 0 | 0, 0 |
| Dispatch p99 | 1.32 s | 2.42 s | 1.25 s |
| Slow `commit` statements in the run, longest | 22, 688 ms | 166, 972 ms | 10, 576 ms |
| Moments with five or more together | 0 | 10 | 1 |
| Full reconnect after a control restart | 18.3 s | 11 s | 17.2 s |
| Other work on the host | Another project through the steady stretch | Another session's install, under three minutes after the steady stretch | Another project's containers for the last two minutes |

None of these three had the host to itself, so they do not meet the rule below. Run 2 shows that a
busy disk still slows heartbeats, though none failed.

ARC-015 asked for three runs in a row on a quiet host, one more than this note first asked, because
the old quota also passed two runs of three. They passed on 2026-11-07, with the image built from
`147eb49` plus the commits of ADR 0040:

| Measure | Quiet run 1 | Quiet run 2 | Quiet run 3 |
| --- | --- | --- | --- |
| Result | Pass | Pass | Pass |
| Worst steady heartbeat p99 (at most 1 s) | 3.8 ms | 169.4 ms | 211.8 ms |
| Failed steady heartbeats, claims (none) | 0, 0 | 0, 0 | 0, 0 |
| Dispatch p99 (at most 5 s) | 1.31 s | 1.32 s | 1.80 s |
| Full reconnect after a disconnect, a partition's heal, a control restart (at most 300 s) | 3.3 s, 57.5 s, 17.1 s | 3.3 s, 59.5 s, 11.2 s | 3.2 s, 57.7 s, 30.8 s |
| Long tasks through the restart, on their first attempt | 100 of 100 | 100 of 100 | 100 of 100 |
| On the stream at the end | 2,000 | 2,000 | 2,000 |
| PostgreSQL throttled | 0 | 0 | 0 |
| PostgreSQL CPU, average and highest 5 s sample | 1.00, 1.47 | 0.96, 1.42 | 0.95, 1.28 |
| Control CPU, average | 1.02 | 1.00 | 0.99 |
| Outbound attempts | 0 | 0 | 0 |

"Quiet" has a stated limit. Another project's test containers and processes kept being started on
the host, and a watcher stopped them within 15 seconds, without deleting anything. That happened at
seconds 850 and 867 of run 2 and 919 and 1,451 of run 3, never in the steady stretch or in the
stretch from the long tasks to the stream deadline. No other session's install changed during the
three runs.

PostgreSQL used up to 1.47 CPUs, which the old 2-CPU quota would have allowed on average: the weight
of ADR 0038 removed stalls, not a shortage. A disk busy with other work still slows heartbeats, as
fix run 2 shows. Each of these runs is a single host with a disk of that speed; a slower disk is not
measured.

## Failure modes

| Failure | Coverage |
| --- | --- |
| A stall of the load generator or host reads as control's | Generator lag per window, host pressure and test process lag per sample place it; a threshold is not relaxed by them |
| A storm's latency hides or fails the steady verdict | Windows split the run; only steady windows are judged |
| A task restarted by the outage still "succeeds" | Threshold 5 reads `attempt` and `acknowledged_attempt` in PostgreSQL |
| Simulated work ends at the outage for a simulator reason | Unit tests: a long task renews through 503s and delivers once, and a written refusal ends it |
| Control dies and restarts unnoticed | Threshold 6 reads the container's restart count and out-of-memory flag |

## What this does not prove

- Simulated nodes are read-only. Training, inference and transfers put other load on control.
- All agents come from one host, through one Docker proxy, so per-address behaviour and real
  network latency are not exercised.
- One control process and one PostgreSQL. High availability is REL-101.
- The database shares its disk only with the host's other work. The disk synced 8 KB in about
  0.34 ms on `ownsolid-cpu`; a slower one is not measured here.
- A run is 25 minutes, not a day. Slow leaks and monthly partition rollover are not covered.


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


## Current native baseline attempt

ARC-106 remains open. The unchanged TestFleetLoad on source
`821227ea5b94a8357f695c290e78af3b81c54f22` failed threshold 7 in 1,576.69 seconds.
At seconds 1,200, 1,260 and 1,320, 76 of 2,000 nodes remained on HTTPS;
only 1,924 were on streams. All 2,000 returned to streams by the 1,380-second
window and at the terminal snapshot. Preserve the three failed assertions and
five-minute fallback behavior; the terminal recovery does not accept this run.

The native Linux amd64 fixture ran the actual installer and schema-60
PostgreSQL 18.6/pgvector 0.8.7, with the same-source qualified web assets,
one control process and a network-none isolated Docker host capped at four CPUs,
6 GiB and 4,096 PIDs. It did not exercise the standard release-image factory,
KWOK, multiple cells or actual accelerators. Before admission it had 10,462,724 KiB
MemFree, 20,455,620 KiB MemAvailable, 791,739,387,904 free disk bytes and 0.10%
full I/O PSI avg10. Both Linux heavy locks were held; no other compiler was active.
All 5,068 source paths and executable modes remained byte-identical after the run.

| Indicator | Measured result in the failed run |
| --- | --- |
| Nodes / pacing / duration | 2,000 / 5 s / unchanged 25-minute generator |
| Enrollment / initial connections | 6 s / 21 s |
| Ten steady one-minute windows | All 2,000 connected and on streams; zero failed heartbeat or claim |
| Worst steady heartbeat p99 | 264.7 ms; histogram bucket upper bound |
| Steady task set | 1,000 successful facts tasks |
| Creation-to-completion median / p99 / max | 0.184 / 1.290 / 1.509 s |
| 600-second steady database rate | Approximately 24 inserted + 425 updated + 1 deleted = 450 row modifications/s |
| Committed transactions / WAL | Approximately 944 commits/s / 228 KiB/s |
| All 2,000 after full disconnect | 3.960 s |
| All 600 after partition healing | 56.363 s |
| Control restart / persisted heartbeat from every idle node | 1.3 / 28.7 s |
| Busy work across restart | All 100 retained their original acknowledged attempt; 92.3 s from first creation to last completion |
| All task leases / ACKs / results | 1,100 / 1,100 / 1,100; no resync, lost reply or revocation |
| Required stream windows | FAILED: 1,924/2,000 at 1,200, 1,260 and 1,320 s |
| Terminal connected / on stream | 2,000 / 2,000 |
| Simulator process | 238 s user, 159 s system; maximum resident usage reported as 487 MiB by the test |
| Egress | Zero product attempts; one observed fixture probe |

Database rates retain the unchanged logger's rounding; the total above sums the
individually rounded row rates. Do not publish them as exact IOPS or a successful
SLO baseline. All 136 independent statistics-state observations had the same
server-start time, null statistics-reset timestamp and track_counts=on. The
PostgreSQL container had zero restarts and no OOM kill. The owned outer container,
nested disposable data and installation scratch were removed; raw evidence stays
outside Git. The fixture image is retained only until the next qualified run.
The selected test-log SHA-256 is
`0dbefb72cce7ced365b6c1400f10c6f7d3cf01b5a47a9ea24002b354931c10da`.

The optional external route collector used ip -j, which the pinned DinD
BusyBox ip does not support. Preserve those failed diagnostic reads and do not
claim a captured route snapshot. The actual harness configured recorder routes,
started with network none and observed its positive probe. Correct only the
external reader for a subsequent run; this does not explain or waive threshold 7.

## Previously usable streams and bounded fallback

The existing algorithm counts two stream failures interleaved with successful
HTTPS heartbeats as evidence that the path lacks HTTP/2, then waits 300 seconds.
That evidence is insufficient after this same Agent session has already used a
valid stream. The observed lingering HTTPS population follows that policy; the
underlying initial transport refusals have not been individually isolated.

[gRPC status codes](https://grpc.io/docs/guides/status-codes/) can be generated by
both the application and the library, and Unavailable can represent a transient
failure. [Error handling](https://grpc.io/docs/guides/error/) retains that
distinction. Do not treat Unavailable alone, a TLS ALPN result, or a generic
HTTP error as reliable proof that the application path permanently lacks gRPC.
The [pinned transport implementation](https://github.com/grpc/grpc-go/blob/v1.85.0/internal/transport/http2_client.go)
also maps transport and HTTP failures into RPC errors. Existing retry ownership
stays with the Agent cycle; do not introduce automatic task replay.

Prototype a remembered usable-stream fact within the existing in-memory Agent
session. Initial unknown/unsupported paths retain the existing two probes and
300-second fallback. After a valid active/disabled stream heartbeat, bound a
future HTTPS fallback by the lesser of that existing 300-second window and the
last valid server-advertised maximum reconnect backoff. The existing cycle,
heartbeat pacing, jitter, operation timeout and retry hints still own attempts;
there is no background probe or additional request inside an attempt. A new
Agent process forgets the fact. It is not persisted as authority or a capability.
Use the same pure scheduling rule in the actual Agent and simulator.

Reject a larger failure count, a longer acceptance deadline, hiding HTTPS from
stream counts, or disabling fallback forever once HTTP/2 worked: the path can
change later. Reject shortening the initial unsupported-path window globally.
Wait-for-ready can help a connecting channel but cannot alone distinguish a
server refusing a current application request from protocol unavailability.

Before product changes, reproduce the old five-minute behavior with a real
verified TLS/gRPC test server, a successful stream, repeated transient refusal
while HTTPS works, and controlled clock advancement. Prove unknown HTTP/1.1
fallback unchanged, positive recovery at the bounded retry, one request per
attempt, failed HTTPS resetting the probe sequence, retained cursor and no
claim/ACK replay. Mirror the scenario through the simulator. Only adopt the
candidate after the external prototype passes; then rerun native package gates,
the strict 500-agent lifecycle and unchanged 2,000-agent test. Existing Go
BSD-3-Clause, gRPC-Go Apache-2.0 and protobuf/x/net BSD-3-Clause licenses suffice;
no dependency, model, dataset or wire field is added. Older agents retain the
old retry policy and can coexist throughout the rollout.


The external verified-TLS prototype on source 77b9b37b reproduced the old
five-minute fallback independently in the actual Client and simulator. Their
strict negative logs have SHA-256 values
`b795380a0868f3ae96c2811cd43050c4dae7aae5edeb5a273de7ed421b5dfb7e`
and `5034fb063a44665ea329fd47822ff0e35a865b2a02f006f9f93cb1d5ab371e99`.
The candidate then passed the pure-policy cases, actual Client fallback cases,
simulator recovery and unchanged initial unsupported-path cases under race,
and vet for all three packages. The Client and simulator positive log hashes are
`b5358ab28e3135d32f75acf2f1175fa72d0f84da022dc56b2318ce15e82f48bc`
and `7e2aa32570436cb6fa6f8b5a6b1484f6c73093a7f96a5e14d15f6d83c1fafac7`.
A successful valid stream still used the same cursor, one explicit claim and no
ACK replay. The initial HTTP/1.1 path retained 300 seconds. These are selected
synthetic-backend protocol checks, not the missing native 2,000-agent acceptance.
The implementation adopts that proven shared scheduling rule and adds new
process-forgetting and failed-HTTPS regressions before requalification.


The complete simulator race suite then retained a separate fixture failure:
TestHTTPSOnlyFaultMovesAgentsToLongPolling observed a correctly restored stream
at its 18-second terminal snapshot, two seconds after its 15-second fault ended.
The log hash is `f0e9299ebdbecaa8c3e2c9d8d1b2d7b682ecc8906cf20a39ed226f540ebe6626`.
Keep its original two nodes, two successful tasks, zero-stream snapshot and
exactly two initial stream opens, but collect that snapshot during the active
fault after both results and the next HTTPS heartbeat. Join cancellation before
fixture disposal. Add an independent post-heal test requiring both streams,
exactly two new successful opens and the same two successful tasks within the
advertised reconnect bound plus the ordinary paced cycle and failed-cycle
backoff. Do not accept a mixed terminal count. The original 18-second fixture
limit and 15-second fault are unchanged; all native 500/2,000-agent assertions
and timing remain unchanged. Fallback eligibility does not bypass pacing or a
server retry hint.


The adopted policy and permanent regressions passed the complete Agent contract,
Client/transport and simulator fleet race suites and all affected vet checks.
The fleet suite includes strict in-fault and full post-heal stream recovery,
new-process forgetting, cursor retention and unsupported-path behavior. No
installed acceptance is inferred. Race log SHA-256 values for contract, Agent
and complete fleet respectively are
`4896f69f4c20170d118c12cc49d813d0942492a93c92c818fe0fd16172f54981`,
`56601c15e90fe0dcfd3cf75d2b1594b41a4387c2db5ff2a8d5e7c069af9df4bf`,
`6d23b95150d34b78f7e40e7693c9075aa83c07d18a72211b7593c576134ba928`.
