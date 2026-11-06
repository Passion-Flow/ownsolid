# 0038: The database gets a CPU weight, not a CPU quota

Status: accepted (ARC-015). Research: [fleet load test](../research/fleet-load-test.md).

## Context

The installer limited the PostgreSQL container with `cpus: "2"`, which Docker turns into a CFS
quota of 200 ms per 100 ms period. A quota is spent by parallelism as well as by load: several
backends on a many-core host can use the whole period's quota in a few milliseconds, and every
query then waits for the next period, including the short ones that hold control's seats.

The fleet load test saw PostgreSQL throttled while it averaged under one CPU of its two, and one
run in three shed heartbeats for three minutes with nothing injected.

## Decision

**PostgreSQL gets `cpu_shares`, 1024 for each CPU in `resources.postgres_cpus`, and no quota.**
It may use idle CPUs, and when the host is contended it keeps its weight against its neighbours.
The configuration key stays, so an existing `install.yaml` still applies; its meaning changes
from a ceiling to a share. A count above 256 CPUs is refused, because Docker has no larger weight.

The memory limit and the process limit stay. Control and the telemetry stores keep their quotas:
control's Go runtime sizes itself to its quota and was throttled for at most 42 ms in a sample,
and neither store is on the path of a heartbeat.

## Rejected

Measured on `ownsolid-cpu` with the product's PostgreSQL image, a steady 2,000 writes a second and
one heavy scan a second, at about 1.2 CPU on average. Latency is that of the steady writes.

| Limit | p99 | Maximum |
| --- | --- | --- |
| Quota, 100 ms period (the old setting) | 264 ms | 990 ms |
| Quota, 10 ms period | 165 ms | 376 ms |
| Quota, pool cut to two connections | 596 ms | 903 ms |
| Two pinned cores | 39 ms | 114 ms |
| No limit | 1.5 ms | 17 ms |

- **A shorter period** shortens each stall but not their total, and at 1.7 CPU it was no better
  than the old setting (p99 2.3 s against 1.6 s).
- **A pool sized to the quota** moves the wait into the pool and was worse than doing nothing.
- **A cpuset** cannot stall, but it needs host-specific core numbers in the configuration and
  keeps the database off idle cores. Its p99 of 39 ms compares with 4.1 ms for a weight confined
  to four cores.

## Consequences

- A runaway query can use more than two CPUs of the host. On a host that also runs workloads, a
  CPU-bound neighbour pinned to the same four cores left the steady writes at a p99 of 9 ms with
  a weight of 2,048, against 33 ms with the old quota and 40 ms with the default weight.
- `postgres_cpus` no longer caps the database, so results from an installation state what the
  database actually used, not only the configured number.
- Applying the change recreates the PostgreSQL container once.
