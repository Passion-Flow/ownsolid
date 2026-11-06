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
1 GB, PostgreSQL on 2 CPUs and 2 GB). A run from one host raises the same limits as the simulator's
own test; each is named in the [simulator research](agent-simulator.md).

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

Run 3 sampled every five seconds and points at **CPU quota throttling of PostgreSQL**. The
installer limits the database container with `cpus: "2"`, a CFS quota. In the steady stretch
PostgreSQL averaged 0.92 CPU yet spent up to 2.0 s of a 5 s sample throttled (p90 0.8 s), and
6.2 s around the disconnect storm. Up to ten pooled backends run at once on the host's 16 cores,
so they spend the period's 200 ms of quota in about 20 ms of wall time and every query then
waits out the rest of the 100 ms period. Control, whose Go runtime sizes itself to its quota,
was throttled for at most 42 ms. A worse stretch of the same stalls would hold control's seats
past their two-second wait, which is what run 2 shed with. This is a hypothesis with
supporting evidence, not yet a proven cause.

Next step: prototype a database limit that does not stall (CPU shares or a cpuset instead of a
quota, or a pool sized to the quota), rerun, and keep ARC-015 open until two runs in a row pass
with the cause fixed. The installer's resource settings are `deploy/` code (lane C).

## Failure modes

| Failure | Coverage |
| --- | --- |
| A storm's latency hides or fails the steady verdict | Windows split the run; only steady windows are judged |
| A task restarted by the outage still "succeeds" | Threshold 5 reads `attempt` and `acknowledged_attempt` in PostgreSQL |
| Simulated work ends at the outage for a simulator reason | Unit tests: a long task renews through 503s and delivers once, and a written refusal ends it |
| Control dies and restarts unnoticed | Threshold 6 reads the container's restart count and out-of-memory flag |

## What this does not prove

- Simulated nodes are read-only. Training, inference and transfers put other load on control.
- All agents come from one host, through one Docker proxy, so per-address behaviour and real
  network latency are not exercised.
- One control process and one PostgreSQL. High availability is REL-101.
- A run is 25 minutes, not a day. Slow leaks and monthly partition rollover are not covered.
