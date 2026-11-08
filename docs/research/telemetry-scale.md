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
