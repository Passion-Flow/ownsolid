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
totalSeries. With an admitted positive native topN cap, truncation cannot remove that
sole rank.

Reject an exact-metric reply whose rank undercounts its positive total, rather
than projecting contradictory successful counts. Add a malformed-source case
for that refusal and retain the existing measured-zero case. Do not apply the
same equality rule to node ranks: missing node_id is valid and bounded node
ranks can omit the remaining nodes. General overviews also retain bounded
metric ranks. This is source projection validation, not an ingest budget or
new scale measurement.

## Native fixture visibility

The [JSON stream parser](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/lib/protoparser/vmimport/stream/streamparser.go#L17-L47)
waits for its concurrent callbacks before returning. Successful import and
force-flush responses still do not themselves assert the daily index counts
needed by the scope test. The qualified shape prototype therefore waited for
its exact scoped count before checking drilldowns.

Retain that explicit fixture boundary in the service profile: poll bounded
native daily counts for all seven imported metric/scope combinations, including
the scope distractors and missing tenant. Start product observations only
after every count matches. Reject an excess count or invalid native response;
do not retry a failed product assertion or silently reimport data. Use one
five-second deadline, bounded replies and cancellation-aware waits. Keep these
fixture reads outside the product-read proxy so they cannot satisfy its
authorization or request-audit assertions. This adds no product behavior or
new dependency and does not qualify the service until the native profile runs.

## Coordinated HTTP projection requirements

Keep the existing read private until the API owner coordinates the route,
Problem registry, generated SDKs and recovery text. The HTTP projection should
follow history's original Admin authentication, Origin, selected-cell and request
ID ownership. Accept exactly one workspace_id and one YYYY-MM-DD UTC date, with
optional exact metric and rank limit. Reject repeated or unknown options,
selectors, tenant/focus-label inputs, encoded alternate paths and GET bodies.
Use the same complete data envelope budget before committing the success audit.

An omitted metric selects the daily overview; an explicitly empty metric is
VALIDATION_FAILED. Preserve a nonempty metric literally without trimming or
normalization. An omitted limit selects the configured maximum. An explicit
limit must be canonical positive decimal and fit both the hard limit and the
configured maximum; empty, zero, negative and leading-zero forms are invalid.
Internal Go empty-metric/zero-limit defaults remain unchanged: the private HTTP
adapter distinguishes omission from explicit input. The frozen candidate's Mac
unit qualification is recorded below; public registration and generated wire
contracts remain open.

The service's metric-name bound is 1,024 UTF-8 bytes. JSON Schema
[maxLength](https://json-schema.org/draft/2020-12/json-schema-validation.html#section-6.3.1)
counts characters and cannot by itself express that byte bound. Preserve the actual
limit in a machine-readable extension and recovery text, with multibyte wire
cases above and below the byte boundary. Date serialization must remain
YYYY-MM-DD in both generated clients, independently of history's RFC3339
date-time encoding. Verify the complete generated wire request before publication.

Both metric and node ranks are bounded views; their sums may be smaller than
totalSeries. The exact-metric rank must equal that total. Missing node_id does
not justify an invented unknown-node row. Selecting a narrower metric is a
recovery option, not a guarantee: one high-cardinality metric can still exceed
the source's scan/time budget. Keep that refusal explicit without automatic
limit increases or unbounded enumeration. These read semantics do not enforce
per-tenant or per-agent ingest budgets.

The repository pins oapi-codegen 2.8.2 and openapi-python-client 0.29.1. The latter's
[date property](https://github.com/openapi-generators/openapi-python-client/blob/v0.29.1/openapi_python_client/parser/properties/date.py)
uses datetime.date, and its [wire template](https://github.com/openapi-generators/openapi-python-client/blob/v0.29.1/openapi_python_client/templates/property_templates/date_property.py.jinja)
uses isoformat. Retain explicit generated Go and Python wire checks for the new
operation; inspecting generator source is not proof of the projected clients.
This preparation adds no dependency or generated artifact.

## Scoped service unit evidence

The private service candidate at
3cebfcef9f321364862a11d8b773c714facb6f68 (tree
0f5c800140e1036a157de62ba4c339b4e2780468) passed the following pure unit command
on Go 1.27.3 darwin/arm64, with race detection and existing offline dependencies:

```sh
go -C services/control test -json -race -p 2 -count=1 -timeout=240s \
  ./internal/modules/monitoring/... ./internal/platform/monitoringmetrics
```

The run completed in 8.771 seconds with exit zero: four tested packages,
46 top-level tests and 139 passing test events including subtests. All seven
series-cardinality service tests passed. Four relay command packages contained
no tests; no test was skipped. Integration-test environment variables were
removed before the run. The isolated checkout was clean at the same commit and
tree before and after execution.

Evidence lives outside the repository at
/private/tmp/obs110-mac-unit-qkhcpg9a: unit-proof.json SHA-256
9a4d682bb796ecb9092f4b396b84828489d08cf2b7241ee2a53eb38c3a65c3bc and
unit-tests.jsonl SHA-256
fcd057faad562d94984f05a85e393764e39838ecfc17a8ae7eb10f74e18b9342.
The persistent regular-file workstation flock covered the test and its process
group; the group exited before release and the lock file was retained. No
VictoriaMetrics, PostgreSQL, browser or installation service was started.

This qualifies that source's Mac pure units only. It does not qualify a later
HTTP adapter, Linux modules, the native store/PostgreSQL profile, generated
clients, public dispatch, installed acceptance or the Console flow. OBS-110
remains unticked.

## Private HTTP unit qualification

The private adapter at d8ad7e790e2d08a267df819ce8547225983fc08d (tree
7ec015f2f87d362f1c943dcb811699f69f45856a) implements the query-presence,
canonical-day, UTF-8 byte and rank bounds above. It remains outside Owns and the
default dispatcher. Shared schema, Problem registry and generated clients were
not changed.

The initial 2dfbb85e HTTP unit run returned the expected ACCESS_DENIED, status
zero and nil RawMessage, but its test reused an any variable from the public
dispatcher. Boxing that typed nil made the test incorrectly report a response.
The corrected test binds the private method's concrete return type and retains
the same error, status and nil-response assertions. The first run's exit-one
proof and raw events remain unchanged at
/private/tmp/obs110-http-unit-70d_wjmn: http-unit-proof.json SHA-256
06e36f5b24805ec54cef3d1f0fbf1430d0e03e9d66fd48bdae3a93c0e83154e8 and
http-unit-tests.jsonl SHA-256
265cc4333c07a1d7392f7daae2a4913e50f7bb25152598d90469f17bcc44cf16.

On Go 1.27.3 darwin/arm64, the corrected frozen source passed two offline race
commands, each with -p 2, -count=1, -json and -timeout=120s:

```sh
go -C services/control test -race -p 2 -count=1 -json -timeout=120s \
  ./internal/modules/monitoring/http
go -C services/control test -race -p 2 -count=1 -json -timeout=120s \
  -run '^TestSeriesCardinalityQueryValidation$' ./internal/modules/monitoring
```

Both commands exited zero in 9.062 seconds total: seven top-level tests and
49 passing events including subtests, with no failed or skipped tests. The HTTP
package included the existing snapshot/history boundary regressions. The other
command checked the intrinsic service query validation affected by the exported
Validate method. No unchanged full suite was rerun.

The isolated checkout was clean at the exact commit and tree before and after
execution. The persistent regular-file workstation flock covered both commands
and their process groups, which exited before release. The same lock file was
retained. No VictoriaMetrics, PostgreSQL, browser or installation was started.
Evidence at /private/tmp/obs110-http-unit-_wbpygdx:

- http-unit-proof.json SHA-256
  b8511e24c4b2ace105e34d07f004654c8b9e956f310951d362a9e691fd582b3b.
- http-unit-tests.jsonl SHA-256
  fc6712d61c151be59d4fa52202c6c10cf421dcc4afc81067692dbdcd088f14f2.

Earlier lock-busy admissions started no compiler or tests and are recorded
separately from the completed runs. The frozen Linux 3cebfcef unit entry also
refused admission at its global-heavy flock with exit 75, before resource
preflight, compilation or tests. The original log at
/root/lane-obs-tools/obs110-service.hJ8NtB/check.log has SHA-256
953b27ae5dcf9bbfc1fca7cbf802ab55713921af7e8e053158160dc7cff3af1c;
admission.json in that directory has SHA-256
d0a308292d67a1d2e580bd8253ed049639145baa84f06a416a01453d0423d7ae.
That refusal supplies no Linux unit or native qualification. The explicit pinned Linux gofmt check passed
all three candidate files; the noninteractive SSH default PATH did not expose
gofmt. These are Mac pure-unit and formatting results only. Linux service/native
PostgreSQL-store qualification, public registration, complete generated wire
contracts, installed acceptance, Console and hard ingest enforcement remain open.

## Generated date and recovery contract

The locked Go runtime is oapi-codegen/runtime 1.8.0. The generator's
[2.8.2 default type mapping](https://github.com/oapi-codegen/oapi-codegen/blob/v2.8.2/pkg/codegen/typemapping.go)
maps string/date to openapi_types.Date and string/date-time to time.Time.
Its [client template](https://github.com/oapi-codegen/oapi-codegen/blob/v2.8.2/pkg/codegen/templates/client.tmpl)
uses StyleParamWithOptions for styled query parameters. The runtime's
[known-type handling](https://github.com/oapi-codegen/runtime/blob/v1.8.0/styleparam.go)
formats types.Date with DateFormat, while time.Time uses RFC3339Nano.
[DateFormat](https://github.com/oapi-codegen/runtime/blob/v1.8.0/types/date.go)
is YYYY-MM-DD. Therefore the default string/date schema has a proven source
path for a date-only query. A type override or an accidental date-time schema
would change that path. This source review did not generate or execute the new
operation; retain full Go and Python request-wire tests against the handler.

Construct the Go value with the selected UTC calendar day. Date formatting
does not reinterpret an arbitrary local time as a UTC date. Keep both optional
parameters absent unless supplied. Do not put a fixed limit default into the
schema: the default comes from the installation's rank policy. Test that nil/
Unset omits each option, but a pointer/value containing zero or an empty metric
is actually sent and refused. Use multibyte metric values and escaped literal
characters to check the complete percent-encoded request, not only model JSON.

Coordinate these public mappings with the existing history conventions before
registration; the current private handler still returns owning-service errors:

| Code | Planned HTTP status | Recovery |
| --- | --- | --- |
| VALIDATION_FAILED | 400 | Correct canonical date/rank, required scope, literal metric bytes or query presence. |
| MONITORING_CARDINALITY_RANGE_INVALID | 400 | Select a UTC day inside the configured raw horizon. |
| MONITORING_CARDINALITY_NOT_CONFIGURED | 503 | Configure the protected raw-index policy, then retry explicitly. |
| MONITORING_CARDINALITY_UNAVAILABLE | 503 | Restore the trusted source or select a narrower metric after scan refusal. |
| MONITORING_CARDINALITY_RESPONSE_LIMIT | 422 | Select a narrower metric or fewer ranks; do not raise the fixed response cap. |

The range mapping follows the published MONITORING_HISTORY_RANGE_INVALID 400
contract. Authority, cancellation, busy and commit errors retain the shared
history mappings and release no successful body. This table is publication
input, not a change to the shared Problem registry. The existing
[SDK dependency review](sdk.md#licenses) covers the pinned Apache-2.0 generator
and runtime, commercial/offline use and redistribution notices. No dependency,
model or dataset is added by this review.
