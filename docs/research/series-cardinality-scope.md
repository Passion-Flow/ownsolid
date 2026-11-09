# Scoped series cardinality lookup (OBS-110)

[VictoriaMetrics Cardinality Explorer](https://docs.victoriametrics.com/victoriametrics/single-server-victoriametrics/#cardinality-explorer)
ranks metric and label cardinality using its native
[TSDB status API](https://docs.victoriametrics.com/victoriametrics/single-server-victoriametrics/#tsdb-stats).
It supports an explicit UTC date, topN, focusLabel, match[] and extra_label.
[Grafana Cloud](https://grafana.com/docs/grafana-cloud/platform/cost-management-and-billing/analyze-costs/metrics-costs/prometheus-metrics-costs/cardinality-management/)
also separates overview, metric and label drilldowns and scopes them to a
stack. Use the existing local store and a bounded projection rather than a
cloud dependency, a raw series enumeration, or a copy in PostgreSQL.

An authorized Control adapter must construct the upstream request from a
server-resolved tenant and workspace with exact extra_label predicates.
Select the drilldown from an allowed set, use an explicit date and cap
topN, duration, concurrent queries and bytes. Store-side scan and time caps
bound the work, since topN only bounds the ranked response. Project
only qualified counts and names. Generic upstream head statistics, raw
label pairs and metric-name query-usage metadata need separate privacy
review. The current UI and identity surface needs an owner-approved
scope and explicit permission before this can become a product endpoint.

Qualify the source with a private single-node VictoriaMetrics 1.155.0
instance, 10,000 synthetic agents in one workspace, other workspaces and
tenants, and one row without tenant scope. Verify exact daily counts,
agent ranking, bounded response, conflicting selectors and whether the
raw response includes cross-scope usage metadata. Record sampled latency
and data-file bytes for the synthetic workload only. Repeat the
qualification for cluster deployments before using these numbers there,
since the upstream cluster endpoint may sum replicas.

The store is the existing
[Apache-2.0 release](https://raw.githubusercontent.com/VictoriaMetrics/VictoriaMetrics/v1.155.0/LICENSE).
Commercial use, offline execution and redistribution are permitted
subject to notices and license terms. The disposable probe uses the existing
[Python standard library](https://docs.python.org/3/license.html) under the
PSF license, which also permits commercial use and redistribution under its
terms. Host network tools are test prerequisites. No runtime dependency,
model or external dataset is added.

The daily index represents series present on a UTC day. It is separate
from authenticated ingest admission or a rolling active-series budget.
Per-tenant and per-agent limits still need an authoritative series identity
and an admission owner from OBS-101. Explorer integration needs the
authorized Control query contract and Console workflow.

## Verified query boundary

On loopback-only Linux amd64, VictoriaMetrics 1.155.0 ingested 10,005
synthetic series with one sample per series. The selected workspace
contained 10,001 series across 10,000 agents. The daily metric ranks and
agent focus counts matched those samples. Predicates for another tenant,
another workspace or a missing tenant returned empty ranks. A topN of one
returned one agent rank.

The raw response still included metric-name query usage across scopes.
Querying a same-named metric only in another tenant changed the selected
workspace's raw requestsCount from zero to one and populated its
lastRequestTimestamp. Its scoped series count stayed 10,000. The product
adapter must omit both usage fields and expose only verified names and
counts from the scope. Do not expose the raw TSDB status response.

Two post-ingest runs used 950,473 input bytes each and produced
4,724,324 and 4,725,717 data-file bytes after graceful shutdown. Twenty
local, warm queries per run gave p95 values of 4.31 ms and 3.36 ms
for the selected workspace. These samples measure one day on a
single-node index without a concurrent write workload.
