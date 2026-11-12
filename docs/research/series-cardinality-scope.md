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

## Product identity and work limits

The current [scoped inventory](../../services/control/internal/modules/monitoring/scrape_inventory.go)
publishes node_id from enrolled PostgreSQL inventory. The
[scrape profile](../../services/control/internal/modules/monitoring/alerting/node-scrape.example.yaml)
retains that trusted node_id and removes arbitrary source labels. The native
probe below uses synthetic agent_id labels; it proves scoped index behavior,
not a deployed agent identity contract. An initial product drilldown must
use the existing node_id and describe its rows as nodes. Do not invent an
agent_id label or claim that this query enforces a per-agent budget.
Agent attribution and ingest admission remain explicit OBS-101/OBS-110 work.

The pinned [TSDB status handler](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/app/vmselect/prometheus/prometheus.go)
builds a UTC-day search with the server's maxTSDBStatusSeries budget.
topN limits only returned ranks. The
[status request context](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/app/vmselect/searchutil/searchutil.go)
accepts timeout and bounds it by search.maxStatusRequestDuration, while
retaining request cancellation. A product adapter must set a bounded
timeout as well as its own operation deadline. Installed store scan/time
limits must be explicit; a small topN cannot stand in for them. A native
qualification must prove that exceeding the scan budget fails rather
than returning a successful incomplete count, including with topN=1.

Reuse the existing Platform Admin diagnostics authority and active Workspace
fences before and after store I/O. Derive tenant and Workspace selectors
only from those owners, and commit the request audit before releasing data.
Expose explicit daily metric and node ranks, never arbitrary matchers,
focus labels, query-usage metadata or the raw status response. Validate a
canonical UTC date within the configured raw horizon, reject future days
and reject the special upstream date=0/all-index query. The public contract
and Console workflow still need their owning lanes before publication.

Before a product implementation, repeat the native projection with the
actual node_id label, scope distractors and a scan-limit refusal/retry.
Then cover malformed or oversized store replies, unknown query options,
bounded concurrency, deadline cancellation, current permission/Workspace
revocation, a failed audit commit, missing configuration and store failure.
No successful response may disclose data after a lost authority or commit.
The existing Go standard library is
[BSD-3-Clause](https://go.dev/LICENSE), permitting commercial use,
redistribution with notices and offline execution. No new dependency,
model or external dataset is required.

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

## Verified node identity and scan-budget boundary

The extended native fixture uses node_id and 10,000 deterministic UUIDv5
identifiers rather than short sequential agent labels. They are synthetic
identifiers, not enrolled nodes. VictoriaMetrics 1.155.0 returned 10,001
selected series out of 10,005 rows. Metric and node ranks, conflicting
tenant/Workspace/missing-tenant filters and a topN of one retained the
expected scope. A foreign-tenant query still changed the selected metric's
query-usage metadata; that metadata must remain outside the projection.

After a graceful restart over the same data with
search.maxTSDBStatusSeries=10000, an overview covering 10,001 series
returned HTTP 422 with an error and no data, even with topN=1.
Selecting the single 10,000-series CPU metric succeeded at the boundary.
A request for 99 ranks was clamped to the configured two ranks. A later
one-series queue query succeeded, demonstrating explicit narrowing and
retry without returning an incomplete overview.

The first prototype rejected the correct refusal because it expected
the precise flag name in the diagnostic. The pinned store instead uses
a generic max-search hint that mentions maxUniqueTimeseries. The revised
assertion verifies the count-limit refusal and absent success data.
A product error must give its own accurate recovery guidance rather
than relay that raw hint.

The refined fixture shared store start/readiness/shutdown helpers, closed
HTTP error bodies and refused Python optimization that would disable its
assertions. The loopback-only Linux amd64 run held both shared locks after
resource and I/O preflight. It imported 1,190,489 bytes, occupied 5,105,423
apparent file bytes after both graceful stops, and measured twenty local
warm queries with nearest-rank p50 3.13 ms, p95 6.24 ms and maximum 6.88 ms.
These figures describe a one-day, 10,005-series synthetic index without
concurrent ingestion, not the seventeen-million-series workload or an
authenticated product endpoint. Both native processes stopped.

The externally qualified fixture had SHA-256
2b8a4037631adbaf7bab54275871e7cd4aad7424256932a5ce2a40de1d1f5bcb
before adoption at
[the existing native probe](../../services/control/internal/platform/monitoringmetrics/testdata/check_cardinality_scope.py).
Product query projection, its authorized API/Console path and ingest-budget
enforcement remain open.

## Scoped service projection

Build the next read beside the existing fleet-history service. Use the same
Platform Admin/active Workspace authority, two short SQL transactions,
shared observation slots and operation deadline. Keep SQL unlocked during
store I/O. Recheck the original scope and current license and commit the
diagnostic audit before releasing either structured data or encoded JSON.
Cancellation and unknown COMMIT must still discard the successful body.
This changes no published history operation.

Add an optional typed series_cardinality policy with a private raw-store
origin, maximum UTC-day lookback and maximum ranks. All three values are
explicit when enabled. Reuse the reviewed direct HTTPS/system-CA transport
and redirect refusal. Derive the upstream timeout from the operation
deadline; keep native scan and status-duration budgets in the installed
store configuration. A missing policy must be distinguishable from a
failed or over-budget source.

Accept an explicit UTC date, an optional exact metric name and a bounded
rank limit. Never accept tenant IDs, arbitrary selectors or focus labels.
MetricsQL supports
[Unicode names and escaped literals](https://docs.victoriametrics.com/victoriametrics/metricsql/).
Build an exact __name__ matcher with a quoted string, never interpolate a
name as an expression. Verify Unicode/escaped-name selection with native
data before relying on that path. Also qualify metrics without node_id
before deciding whether an unattributed node rank can be emitted.

Project only the selected date, server-resolved Workspace ID, total series,
applied rank limit, metric-name counts and node-ID counts. Preserve an
explicit measured zero and empty arrays. Reject absent counters, invalid
numbers, duplicate ranks, impossible sums, excessive rows, partial replies,
warnings and oversized bodies. Omit query-usage metadata and all other raw
status fields. Bound both upstream bytes and the complete encoded response,
including its serving envelope, before the success audit commits.

Keep the transport adapter outside Owns until the public API owner has the
concrete result and Problem contract for publication. Native service tests
must cover real PostgreSQL, the selected metrics binary, scope isolation,
revocation during I/O, failed audit commit, retry and request-bound audit.
The existing HTTP and private-TLS history profiles must pass after shared
transport or authority code is factored. API/Console publication and ingest
budgets remain separate open parts of OBS-110.
