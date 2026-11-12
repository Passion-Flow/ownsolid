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
name as an expression. The native shape qualification below verifies Unicode/escaped-name
selection and metrics without node_id. Preserve missing-node omission; do
not invent an unattributed row or accept an empty node identifier.

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

## Fleet-scale operating boundary

The existing [fleet qualification](telemetry-scale.md#linux-amd64-qualification)
contains 10,000 synthetic nodes with 1,700 series each: 17 million distinct
series and 153 million samples over nine rounds. Its 4,483,944,448 allocated
bytes include index and cache, and its first raw cell query took 16.888 seconds.
Those measurements justify avoiding unbounded raw enumeration for an interactive
explorer. They are not a daily TSDB-status benchmark or a production retention
size estimate.

The cardinality fixture is deliberately smaller: 10,005 series on one UTC day,
5,105,423 apparent file bytes and a twenty-query warm p95 of 6.24 ms before
Control authorization and auditing. Do not multiply those bytes into a fleet
retention estimate or present that latency as a seventeen-million-series result.
The daily index and retained sample bytes depend on label churn and compression;
representative long-running ingestion is still required for that sizing.

A bounded exact-metric drilldown can select the 10,000 node series for one metric
without scanning every metric in the fleet. Enforce the installed native scan
and status-duration budgets even when topN is one. If an overview exceeds them,
return an unavailable result with recovery by selecting an exact metric; do not
return a partial count, increase the scan cap automatically or fall back to raw
series enumeration. The service's operation deadline, shared observation slots,
four-MiB upstream cap and one-MiB complete JSON cap bound Control work and transfer;
they do not prove the source's full-fleet scan latency. A full-fleet daily overview
remains unqualified at the seventeen-million-series shape until measured there.

The next native service profile must measure the narrowed 10,000-series read,
including real PostgreSQL authority and committed audit, and verify native scan
refusal followed by that explicit narrowed retry. Installed source budgets and
the Console's recovery guidance remain prerequisites for publishing this path.

## Verified native projection shape

A separate loopback-only Linux amd64 prototype used the pinned VictoriaMetrics
1.155.0 binary (SHA-256 aca3c49816db88c0e48012772fcf2a94eb66aaccd925b35d397b8b6f7f40fb43).
Five imported samples selected four scoped series: two CPU node series, one app
series without node_id, and one Unicode metric name containing a quote and a
backslash. Another tenant had the same Unicode name as a scope distractor.

The unfiltered scope returned totalSeries=4 and two node ranks summing to three.
Selecting the app metric returned totalSeries=1 and an empty focus-label rank.
Therefore missing node identity is omitted by the native index. Product node
ranks contain canonical node UUIDs only; preserve an empty array for an app-only
view and never infer an unattributed count from the truncated rank remainder.
An explicitly empty or malformed node identifier is invalid source data.

Exact __name__ matchers using either escaped Unicode or raw UTF-8 literals
returned only the one scoped Unicode metric, with its exact original name.
A missing metric returned measured zero and empty ranks. The source's auxiliary
label and query-usage fields remain outside the product projection.

The prototype held both shared locks after memory, disk and I/O checks, and the
native process stopped before the isolated network namespace exited. Its source
SHA-256 was 460dcad730628d3d58f8753b1de363d3b078441cf2a1785db3490f2eb8276342;
this five-sample shape qualification is not a scale, installed-product or
retention benchmark. The owning Go native service profile must retain these
cases so the product projection can be qualified without the external prototype.

## Exact metric count integrity

The pinned [status handler](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/app/vmselect/prometheus/prometheus.go#L567-L618)
uses one filtered search for the total and ranked metric counts. Its
[index implementation](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/lib/storage/index_db.go#L1360-L1485)
counts matching metric-name rows into that total and the metric ranks. An exact
metric selector therefore has at most one metric rank, whose count must equal
totalSeries. The configured native topN cap cannot truncate that one rank
because the handler enforces a minimum of one.

Reject an exact-metric reply whose rank undercounts its positive total, rather
than projecting contradictory successful counts. Add a malformed-source case
for that refusal and retain the existing measured-zero case. Do not apply the
same equality rule to node ranks: missing node_id is valid and bounded node
ranks can omit the remaining nodes. General overviews also retain bounded
metric ranks. This is source projection validation, not an ingest budget or
new scale measurement.
