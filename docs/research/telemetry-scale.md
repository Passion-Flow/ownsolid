# Telemetry scale qualification (OBS-108)

[ADR 0009](../decisions/0009-telemetry-and-log-stores.md) qualified the store
selection with 17 million synthetic series in a short ingest-only run.
OBS-108 makes the fixture reproducible and measures queries during sustained
ingest. It is a VictoriaMetrics qualification on Linux amd64. The separate
fleet-control load tests exercise agent enrollment and control traffic.

## Sources and licensing

The pinned VictoriaMetrics v1.155.0
[open-source license](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/LICENSE)
is Apache-2.0. It allows commercial use, offline operation and redistribution
under its license and notice requirements. The harness uses only the
[Go standard library](https://go.dev/LICENSE) (BSD-3-Clause) and generated
samples. No third-party libraries, models or datasets are added.

VictoriaMetrics documents the
[Prometheus exposition import and instant-query APIs](https://docs.victoriametrics.com/victoriametrics/single-server-victoriametrics/#how-to-import-data-in-prometheus-exposition-format).
The HTTP import can accept a batch even if parsing or storage discards rows.
The harness verifies actual store counters as well as results from the query
API. The upstream [capacity estimator](https://docs.victoriametrics.com/vmestimator/)
provides sizing from actual metric samples when a deployment has them.

## Approach and acceptance

Generate 10,000 nodes in five cells, each with 1,700 distinct series.
Use a fixed timestamp per round and send bounded batches over the same
Prometheus import path the control plane already uses for its process metrics.
Use deterministic changing values and stable labels. Measure initial series
creation separately from repeated rounds on a 15-second schedule. Check exact
accepted sample and created series deltas on a disposable, initially empty
store. Query counts for one node, one cell metric and one fleet metric under
concurrent ingest. Record wall time, interval overruns, query latency, peak
memory and storage size. Report the synthetic sample volume and observed
environment explicitly.

Run on the pinned image in a network namespace with loopback only. A rejected
batch, partial ingestion, missing data or failed query must fail the harness.
A smaller fixture validates the generator and queries without a full load
run. Do not replay a captured customer payload or use an unmaintained external
generator. A many-hour soak and a measured production-retention window need
representative label churn and value distributions, which this fixture does
not provide.

## Linux amd64 qualification

On 2026-11-09, TestVictoriaMetricsFleetScale passed on ownsolid-cpu against
VictoriaMetrics v1.155.0, with loopback as the only interface and no default
route. The store and generator each used GOMAXPROCS=6 on the shared 16-core
host. The store used an 8 GB cache allowance.

| Measurement | Result |
| --- | --- |
| Nodes, cells and new series | 10,000 nodes, five cells, 17,000,000 series |
| Exact persisted samples | 153,000,000 over nine rounds, zero invalid rows |
| Initial series creation | 189.920 s, 89,512 samples/s |
| Steady ingest | Eight rounds at 1.48 to 2.42 million samples/s, zero 15-second overruns |
| First node, cell and fleet queries | 4.510 s, 16.888 s, 1.758 s |
| Node query during a write round | 2.566 to 10.307 s for 1,700 series |
| Cell and fleet query batches started alongside writes | 7.463 to 25.466 ms for one metric across 2,000 nodes and 22.071 ms to 2.612 s for one metric across 10,000 nodes |
| Storage after graceful shutdown | 4,483,944,448 allocated bytes, including index and cache |
| Peak store RSS | 10,165,673,984 bytes |

The query batch starts with the node query and evaluates the cell and fleet
queries afterward. Some later requests can finish after the write round.
Rollup result caching was disabled for these queries. The test also passed
the five-node smoke profile.

At a 15-second interval, this fleet shape produces 1.133 million samples/s
and 97.92 billion samples per day. The measured disk size reflects nine
samples per series plus indexing and caches. It does not establish a
production retention budget. A retention estimate needs the data-file growth
rate from a longer run with representative value changes and label churn,
plus separate index, cache and merge headroom. The observed cold cell and
node latencies support using bounded queries and recording rules for
interactive summaries rather than fleet-wide raw scans.
