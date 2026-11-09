# Scoped node scrape budgets (OBS-101)

The first supported exporter path uses the installed node exporter and a
private vmagent, with exactly one target per enrolled node. The node's
identity and tenant/workspace/site/cell labels come from trusted inventory
export. The exporter cannot change them. Configure HTTPS, disable redirects,
ignore exporter timestamps, and filter metric and label names before writing
to VictoriaMetrics. Keep remote write and inventory paths private. The
operator must provision endpoint identity, trusted CA and egress control.

[Prometheus scrape configuration](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#scrape_config)
distinguishes a response-body cap, a sample cap and label limits.
[vmagent's native limiter](https://docs.victoriametrics.com/victoriametrics/vmagent/#cardinality-limiter)
tracks unique post-relabel series per target and exports its per-target
budget, occupancy and discarded samples. In the
[pinned source](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/lib/promscrape/scrapework.go)
the limiter is a [process-local Bloom filter](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/lib/bloomfilter/limiter.go)
refreshed every 24 hours. False positives, agent restart, target remapping
and duplicated scrapers can admit extra series. Use it as a load guard
rather than a durable hard quota or an authorization boundary. A
per-scrape sample limit alone does not cover churn across successive
scrapes. A global remote-write limit can throttle unrelated targets.

The profile also limits each response before the sample cap because
vmagent needs a whole body to enforce both scrape limits. A drop in the
distinct-series budget leaves known series flowing. An oversized scrape
fails as a unit. Use a named metric and label allowlist for the first
node-exporter profile and record the separately owned GPU, service and
app additions.

The [pinned Bloom filter](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/lib/bloomfilter/filter.go)
uses 16 bits per budgeted series. At 2,000 series and 10,000 targets, that
is about 40 MB for one filter generation, before overlap on refresh, Go
object costs, scrape buffers, TLS, disk queues and remote-write batches.
The [10,000-node store test](telemetry-scale.md#linux-amd64-qualification)
measured the raw store's ingest, query latency and allocated bytes with
17 million series. Target scraping and sustained retention at that
size require a separate workload.

The existing [VictoriaMetrics 1.155.0 license](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/LICENSE)
covers vmagent and VictoriaMetrics under Apache-2.0, and the
[node exporter license](https://github.com/prometheus/node_exporter/blob/v1.12.1/LICENSE)
is Apache-2.0. Go is [BSD-3-Clause](https://go.dev/LICENSE). They allow
commercial use, offline operation and redistribution subject to notices.
The loopback fixture uses generated exporter records and introduces no
dependency, model or external dataset.

The acceptance must use actual vmagent and VictoriaMetrics on loopback
with local TLS, two independently labeled targets, stable samples before
churn, and a noisy target that attempts spoofed scope and new series.
Observe discarded samples only for the noisy target, continued samples
from both targets, and no spoofed scope labels or unsupported families
in the store. Qualify the response/sample limits separately. A product
claim also requires enrollment-driven trusted inventory, exporter TLS
provisioning, installed job definitions, and the remaining producer
families and budget enforcement.

## Verified scoped scrape path

On Linux amd64 with loopback only, TestScopedNodeScrapeBudgets passed with
vmagent and VictoriaMetrics 1.155.0. Both HTTPS targets used the fixture CA.
The test waited for stable samples, then introduced new series and
exporter-controlled tenant, workspace, node and job labels. The noisy
target reported dropped series while both targets continued to send their
stable values. Only the discovery labels were stored and the unapproved
metric family was absent. A separate sample overflow failed the noisy
scrape, the target recovered on a conforming scrape, and an oversized
response body failed it again. The other target stayed healthy. Both
telemetry processes and the HTTPS fixtures stopped on test exit.
