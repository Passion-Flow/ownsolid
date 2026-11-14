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
