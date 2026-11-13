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

## Native daily-index prerequisites

Keep the product date contract separate from an assumed upstream sentinel.
The pinned [status handler](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/app/vmselect/prometheus/prometheus.go#L575-L607)
parses an explicit day and builds that day's search interval; the literal
date=0 enters its numeric day-zero branch. Merely observing that branch does
not establish a successful all-index read. The product accepts only canonical
YYYY-MM-DD input, so a bare zero remains invalid without relying on that
upstream behavior. This source correction changes no candidate request parser
or previously recorded run.

The more important source profile boundary is
[disablePerDayIndex](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/app/vmstorage/main.go#L88-L91),
which defaults to false. The selected
[storage options](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/lib/storage/storage.go#L151-L160)
carry that setting into storage. In
[containsTimeRange/containsDate](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/lib/storage/index_db.go#L454-L499),
a disabled per-day index adjusts a matching search to the global-index range.
[TSDB status collection](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/lib/storage/index_db.go#L1344-L1392)
then uses the adjusted index prefix. An explicit query date alone therefore
does not prove that the source used its per-day index.

Before publishing the daily view, the owning installation/source profile must
retain per-day indexing and the qualified scope, scan and duration bounds.
A boolean assertion from the caller cannot establish that native profile.
Verify the actual selected startup configuration and its source identity at the
owning composition boundary. Do not present a disabled-index profile as an
equivalent daily source, and do not assume narrowing a metric repairs index
semantics. This prerequisite is still unqualified by the private service's
pure-unit runs.

Extend native qualification with distinct scoped series on two UTC days within
one native partition, so a broader index cannot accidentally match the expected
daily counts. Check totals and bounded metric/node ranks for each selected day,
scope distractors, empty days, restart and midnight/churn behavior. Use an
explicit disabled-per-day-index control to observe the native behavior and
prove that the product's supported source admission refuses that profile.
Do not require the native endpoint itself to reject it if its documented mode
returns broader index statistics. The earlier one-day measurements are unchanged
and do not cover these controls.

These are source-based preconditions and planned native controls, not a recorded
failure of an installed product or a new runtime qualification. The existing
VictoriaMetrics Apache-2.0 and Go BSD-3-Clause dependencies remain unchanged;
no model, dataset or package is added.

## Linux native daily-index qualification

The unchanged candidate 5f83cfbf9c1c24b8efae927f76a69912d52c7c68, tree
170ad1a59b80cd361ec65cbf74364ad400d8a426, passed the original four native
entries on Linux x86_64 with Go 1.27.3. The selected cf228b0c entry, test set,
two Go workers, race instrumentation, offline dependencies, private PID/proc
and loopback-only network, no host port publication and no-pull policy were
unchanged. The earlier six lightweight checks were reused, not rerun. Neither
the broader module graph nor another benchmark was executed.

Both actual flock calls returned zero with empty stderr. FD 8 and 9 retained
the original dev 29/inode 82 and dev 64512/inode 7508772 carriers for the
critical section. The locked resource recheck observed 17,132 MiB MemFree,
739 GiB available disk and full I/O avg10 0.02. The unchanged caller completed
in 170.913 seconds with raw exit zero, inside its 300-second/ten-second bound.

| Entry | Outer root selectors | RUN / PASS records | Result |
| --- | --- | --- | --- |
| cardinality | TestScopedSeriesCardinalityOffline | 2 / 2, including daily-index-profile | Exit 0 |
| history-http | TestFleetHistoryAuthenticatedHTTPOffline | 1 / 1 | Exit 0 |
| history-tls | TestFleetHistoryPrivateTLS | 2 / 2, including its existing isolated trust subprocess | Exit 0 |
| history-retention | TestFleetHistoryNinetyDayAggregates | 1 / 1 | Exit 0 |

There are four outer roots, one additional TLS subprocess root and one daily
subtest: six executions with five distinct names, zero FAIL or SKIP records.
The raw entry-summary parser counts only column-zero RUN lines, so it records
five RUN lines and six PASS lines. The sixth RUN is nested in the TLS parent's
logged subprocess output; the existing source explicitly launches that process
to initialize independent system trust. These are plain test-log records, not
invented JSON test events or six distinct test functions.

The cardinality profile verified the original scoped 10,000-series metric,
scan refusal and narrowed retry, current authority, audit and commit outcomes.
The daily profile used completed UTC days 2026-11-10 and 2026-11-11 in the same
month, with an empty 2026-11-09 control. It verified distinct nodes, metric and
node ranks, scoped distractors and missing-node handling. Exact CPU counts were
two and one; overview counts were two and three. It repeated the reads after
a graceful restart in the same data directory with actual
-disablePerDayIndex=false. A separate directory and actual true setting were
rejected at fixture admission before product binding, then independently read
as broader global-index counts. Native assertions checked the real /proc child
argv against exec arguments and fingerprinted its running executable as
aca3c49816db88c0e48012772fcf2a94eb66aaccd925b35d397b8b6f7f40fb43.
This is a native fixture result, not installed-product source admission or a
live midnight/churn qualification.

A bounded independent read of this run's owned cardinality container, after its
first measured read, returned 119 non-partition-child logical tables and 59
migration rows, with versions one through 59. It returned PostgreSQL
18.6 (Debian 18.6-1.pgdg12+2) and vector 0.8.7. The image remained the cached
Linux amd64 c29f3068 artifact. This verifies the actual 59/119 source fixture;
current main's separate 60/120 lineage is not borrowed as a run result.

The 10,000-series narrowed service read, including SQL and audit, took
18.69549 ms and returned 401 JSON bytes. Its stopped one-day store occupied
5,890,048 allocated bytes. This is one measured read and one synthetic UTC day,
not a percentile, production retention estimate or 17-million-series result.
The five-selected-series daily stores occupied 253,952 allocated bytes after
ingestion, 204,800 after restart and 245,760 in the separate global control;
those sizes describe correctness fixtures, not fleet sizing.

The existing history HTTP regression passed real TLS/CSRF/session, scope,
revocation, response and audit controls. The independent TLS child refused
an untrusted same-origin replacement without raw fallback and recovered on
the trusted replacement. The unchanged 90-day aggregate fixture persisted
1,296,000 samples in ten series, returned 1,424 points per cell and measured
483,328 allocated bytes. Its first read after native restart took 835.50061 ms;
thirty subsequent authenticated schema/count/audit-verified reads measured
median 809.3509 ms, p95 918.521037 ms and maximum 925.585413 ms. This is the
existing synthetic regression at this source, not a new benchmark or customer
latency claim.

Logs record ten VictoriaMetrics starts and ten graceful stops across the four
entries. All four acknowledged PostgreSQL containers were stopped and removed,
then independently confirmed absent. The owned process group was empty. Entry
parent 3030346, timeout 3030345 and observer 3030332 exited. Both real FLOCK
lines were present in the final pre-close FD snapshot; after explicit close,
both FDs were absent. Both original regular carrier identities and zero lengths
remained. Source and input manifests verified unchanged after execution, and
the worktree remained clean. No browser or installed service was started.

Raw evidence is
/root/lane-obs-tools/obs110-native-opportunity-v3.pbo1l1z5/result.json, SHA-256
87f30f963481f61fa531ad6c15a8355f5c62ad32fd4ef555d012fae7f49a391e,
and outer.log SHA-256
b0e1ccc8d87dc02a4b590c70ebb6f0143f8a136a41d05268389ea28647097d21.
The entry receipt /root/lane-obs-tools/obs110-daily-native.YWPw99 contains:

- cardinality.log: f4b29900c4c25f099e16ce596d6f09264acef8dae9e414e384bbc73841ded6f8.
- history-http.log: bfd2510e5f511aa58d438cfd69fa98f959d1a5fb59a126f8309b5f24cdacf914.
- history-tls.log: 1ebcd7dd09703a243c4089e62a6a8c3617ebee6630e56478b2eaeaaef42ee644.
- history-retention.log: 1212c01305325de65a0bc0ac5bab031334976d91a341e090294179d5958fa844.
- source-inputs.sha256: 267f0d2eadb60c967c7a0d5c33ec3d0b0057e9989ffcd5a1ef7b84590421395d.
- fd-before-close.json: edcc8a601f7caf7f79806151cc0c3c1b8b2f7eaa8082587737149a1f36e0c524.
- fd-after-close.json: 75074edd1cdba2b41a447c6b5bb683502a4100f378f3557450a73d705a1ba177.

The earlier raw 75 refusal remains unchanged. This run qualifies only the
native service/fixture boundary and existing history regressions at 5f83cfbf.
It does not qualify the separate d8 HTTP candidate on Linux, current-main
integration, shared cardinality contracts/dispatch, product source admission,
installed acceptance, Console or hard ingest budgets. No whole-repository gate
ran and no roadmap checkbox changes. No dependency, model or dataset is added;
the existing license terms above still apply.

## Preserved earlier candidate evidence

The complete source-scoped unit, private HTTP and admission history remains
unchanged at commit fd19b5cde02e388ffa2919d61382fcfc2ffb291e in this topic's
research note and the frozen /root/lane-obs-native-evidence worktree. It records
3cebfcef Linux units separately from d8 Mac HTTP units and from the 5f83cfbf
native run. The first typed-nil HTTP assertion failure and corrected d8 run,
two older c60 refusals with unknown holders, and the later raw 75 bounded wait
remain preserved; none is relabeled by this integration. The latter raw result
is /root/lane-obs-tools/obs110-native-opportunity-v2.yipeud6u/result.json,
SHA-256 dfee1351beb065c7739fa219f329234a716a395192bd666a9aaf8c5b45988542.
No prior qualification applies to the new baseline without its stated scope.

## Current-main integration and producer boundary

The integration baseline is ecab4c9824c534ba24823a3ed56f388a796db872.
Its monitoring and monitoringmetrics module bytes still equal their common
4f76102c baseline. The owned 5f83cfbf module delta can therefore be applied
without replacing another owner's changes. The d8 private HTTP delta is a
separate addition, not a public route registration. Preserve every frozen
source and its exact earlier qualification; an integration checkout does not
inherit a native run merely because module bytes match.

Shared inputs changed in the migration/catalog lineage, identity audit reads,
HTTP connection lifecycle and test HTTP client. Go module requirements and the
published monitoring OpenAPI did not change. The new baseline declares 60
migrations and 120 logical tables; no database run or readback has established
that scope here. The source-specific 59/119 proof above remains exact.

Use the existing bounded observation, tenant/Workspace authorization, post-I/O
recheck and committed audit path. Preserve the current shared owners' code and
contracts. First check integration byte conservation and the affected pure
query/HTTP, history and route-classification logic. Keep Linux native/public
HTTP and complete repository gates separate until an allocated resource window.
No shared startup, configuration, IAM, Web, default producer switch or API
publication belongs in this slice.

A configured URL or caller-provided daily-index boolean is not product source
admission. The next module slice must accept a typed query producer from the
trusted runtime owner, verify its actual source identity and native profile,
and refuse missing, mismatched, stopped or replaced sources without a raw-URL
fallback. Reuse the proven false-index/restart/true-control semantics above.
The owner must supply a coherent generation-bound observation and query lease,
not a policy assertion. Keep lease acquisition and cleanup bounded, preserve
request cancellation, and discard replies if the admitted source is lost.
The module must not start native stores or create a proof ledger.

The Go [RoundTripper contract](https://pkg.go.dev/net/http#RoundTripper) provides
a single-request transport boundary; it does not follow redirects by itself.
[Context cancellation and AfterFunc](https://pkg.go.dev/context#AfterFunc)
can bind an in-flight read to the source lifetime without an independent worker.
The runtime owner remains responsible for actual process/profile/endpoint
binding and process shutdown. The module owns fixed scoped query construction,
bounded decoding and release of each acquired query lease. Full startup,
source-owner integration and installed acceptance remain explicit prerequisites.
The existing Go BSD-3-Clause and VictoriaMetrics Apache-2.0 terms apply, with
no new dependency, model or dataset.

The module producer preparation uses a private Go composition owner that opens
one live, immutable generation lease per query. The lease reports the approved
inventory digest and the observed running executable digest, actual argv, source
reference and origin; it supplies the single-hop transport and a cancellation
context. The module requires matching canonical identities/digests, explicit
unique false daily-index mode, a finite configured scan cap, sufficient native
rank and whole-day retention, and a query-duration ceiling within the operation
budget. Missing authority fails before store I/O. Root must create this owner
from the installed cell and authenticated source inventory, never request JSON.

VictoriaMetrics v1.155.0 [envflag source](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/lib/envflag/envflag.go)
expands environment templates before parsing and defaults environment flag
loading to disabled. This initial profile rejects templates, ambiguous required
flag syntax, duplicate profile flags and enabled environment flag loading.
[Storage initialization](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/app/vmstorage/main.go)
passes the selected daily-index mode to storage. Alternate retention syntax and
unverified profile variants are refused rather than inferred. Tests cover
identity/profile refusal, zero-I/O refusal, lease loss and replacement, query
cancellation, response limits and deterministic cleanup. Opening/closing a
query adapter must never start or stop the native producer. The original startup
owner supplies those lifecycle facts and closes the adapter at shutdown.


### Module producer preparation

The source following integration commit 5ef9c4e8 now implements the typed
SeriesCardinalitySourceOwner/SeriesCardinalitySourceLease contract,
OpenSeriesCardinalityQuerySource and NewWithSeriesCardinalitySource. Policy
adds a canonical source_id and finite maximum_scan_series. Normal New refuses a
configured source without the concrete admitted adapter. There is no raw-URL
fallback. Startup admission makes no module store request, releases its lease,
and has an independent adapter lifetime. Every query validates a fresh lease,
limits its operation context, and closes its response body and lease. Context
loss and release errors discard the result; private owner errors are sanitized.
Close cancels query authority and never stops the native producer.

The new native-tagged fixture adapter obtains argv/running digest from the
actual child, reads the approved fixture executable independently, and observes
process exit/stop cancellation. Its true-index control is also passed to the
typed adapter as a negative. That adapter remains synthetic test composition;
it cannot prove authenticated installed source approval. No native run has
executed this changed source. The original 5f83cfbf daily proof stays frozen.

Seven new pure test roots prepare identity/profile refusal, missing/typed-nil
owner and lease refusal, per-query revalidation and zero-I/O refusal after
changed source evidence, source loss/restart/retry, bounded acquisition,
startup/policy binding, caller/generation/adapter cancellation, response/body
failure and cleanup. Existing cardinality/HTTP/history/route tests are the
necessary regression selection. Integration-tag compilation prepares the new
fixture calls without starting native services.

Pinned Go formatting/parser and whitespace checks passed. The repository layout
check passed on Linux without a compiler or service. The single new Mac
nonblocking opportunity returned raw 75 at the original permanent carrier
before compilation or test children. Its unmodified result is
/private/tmp/obs110-main-integration-pb1qgf_t/producer-logic-result.json; the
later corrections and completed source have only static verification here.
Do not count this refusal as a test failure or as qualified producer tests.
No further lock attempt, Linux compiler, VictoriaMetrics or PostgreSQL process
was started. The carrier remained unchanged and its opened FD closed.

The shared startup call site remains openMonitoring in
services/control/cmd/server/monitoring.go. ARC must provide the installed-cell
owner, call the opener with a bounded startup context, pass its result into
NewWithSeriesCardinalitySource, close on construction failure and register
Close in the existing shutdown chain. No shared configuration, startup, IAM,
public route or Web change is included. See ADR 0080 and the module README for
the exact contract. Shared schema is still 60/120; the coordinator reports only
Help/TRN documentation changes after ecab4c98, which do not justify chasing
pure-documentation main updates or repeating old native suites.

Current-main targeted race checks, integration-tag compile, native/installed
acceptance, the whole-repository gate and all remaining OBS-110 completion
criteria remain open. No roadmap checkbox changes.
