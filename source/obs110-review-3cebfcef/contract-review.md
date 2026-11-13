# OBS-110 public contract review

Scope: read-only planning for service source
3cebfcef9f321364862a11d8b773c714facb6f68, based on 4f76102/schema 59.
No public route, OpenAPI, SDK, Help, locale, ownership or product source was
changed. The reviewer did not run product tests. The supplied Mac unit receipt and
raw log were independently hashed and inspected; Linux and native protocols remain
unqualified as described below.

## Names and parameter contract

Keep the proposed cell-relative GET /admin/monitoring/series-cardinality.
The public path can follow the existing fleet-history scope:
 /api/v1/sites/{site_id}/cells/{cell_id}/admin/monitoring/series-cardinality
Use operationId adminSeriesCardinality, parallel to adminFleetHistory.
Use SeriesCardinalityResponse for the data envelope and SeriesCardinality,
MetricCardinality and NodeCardinality for its models. The repository configures
oapi-codegen response-type-suffix=HTTPResponse, so the operation wrapper is
AdminSeriesCardinalityHTTPResponse. The initial review's default-suffix collision
rationale was incorrect and is withdrawn; avoid the actual configured wrapper
name when naming components. No public type has been generated for this route.

| Query | Public form |
| --- | --- |
| workspace_id | Required canonical lowercase nonzero UUID; authority and tenant remain server-derived. |
| date | Required canonical YYYY-MM-DD UTC calendar day, with date format and ten-character shape. Reject missing, invalid-calendar, date=0 and non-day spellings. Future/outside-policy days are range errors. |
| metric | Optional exact, case-sensitive, nonempty name. Preserve Unicode, whitespace, quotes and backslashes; do not trim or reinterpret as an expression. Enforce 1024 UTF-8 bytes on the server. |
| limit | Optional canonical positive ASCII decimal matching ^[1-9][0-9]*$, 1 through 100 and no greater than the effective configured maximum. Omission uses that maximum. Explicit empty, zero, negative, leading-zero, plus, exponent and excess forms fail with VALIDATION_FAILED/400. Translate only omission to private Limit=0; do not clamp. |

Use a parse-error-aware query parser, as existing HandleFleetHistory already
does. Reject duplicates, unknown keys and malformed escaping before store I/O.
An invalid optional metric must never disappear into an unfiltered overview.
Do not accept tenant, selector, match[], extra_label, focusLabel, node_id or
upstream topN aliases.

OpenAPI maxLength counts characters, not UTF-8 bytes. A maxLength of 1024 is
only an outer character bound; description and byte-aware server validation
must retain the actual 1024-byte rule. An optional annotation can record that
rule, but generated clients must not be described as enforcing an extension
they ignore. Never lower the character bound to 256 or reject Unicode to
approximate the byte limit. In future wire coverage, 341 copies of U+754C
are 1023 bytes; 342 are 1026. Preserve percent, plus, quotes and backslashes
through one query decoding and one exact-name matcher encoding.

The existing fleet-history raw-query budget is only 512 bytes. It cannot be
copied unchanged: percent encoding can turn a valid 1024-byte metric into up
to 3072 query bytes before the remaining parameters. Give the new adapter a
separate bounded raw-query budget that admits the documented encoded maximum.

## Response meaning

All six data fields are required: workspace_id, date, total_series, rank_limit,
metrics and nodes. Arrays are present and non-null, including empty results.
Use integer/int64 with 0..9007199254740991 for total_series; rank series_count
is positive with the same upper bound. rank_limit is 1..100 and describes the
Control projection bound, not a guarantee that this many rows exist.

Both arrays are independently bounded by rank_limit and are not paginated.
Metric counts and node counts are overlapping dimensions: never add them
together. Each dimension's sum is at most total_series. For an exact metric,
a positive total has one matching metric row equal to the total; this exact-selector equality is stronger than general truncated rankings. Measured
zero has empty ranks. Node ranks may be incomplete both because of rank
limits and because legitimate app metrics have no node_id. Never invent
unknown-node, unattributed or remainder counts. Keep the service's descending
count order and ascending name/UUID tie-break.

This is the native daily index view, not current active agents, a transactional
fleet snapshot, an ingest admission budget or billing usage. Today can change
between reads. Do not promise the full-fleet overview latency from the small
native prototype.

GetSeriesCardinalityJSON returns the data object's RawMessage, not an already
wrapped response. encodeMetricsJSON reserves the serving owner's
{"data": ...} envelope and newline when enforcing the 1 MiB body budget.
Keep the existing history adapter/original dispatcher path so exactly one
envelope is written. The limit describes encoded UTF-8 JSON body bytes,
including that envelope, not HTTP headers or TLS framing.

## Error names, HTTP status and recovery

Keep the four proposed names.

| Code | HTTP | Recovery contract |
| --- | --- | --- |
| MONITORING_CARDINALITY_NOT_CONFIGURED | 503 | Configure the private daily-index source, lookback and rank policy, then retry. No fabricated zero. |
| MONITORING_CARDINALITY_UNAVAILABLE | 503 | Retry explicitly; exact-metric narrowing may help, while a high-cardinality single metric can still exceed the scan budget. Have an operator check source availability and scan/time budgets. Do not expose raw source errors or enumerate series as a fallback. |
| MONITORING_CARDINALITY_RANGE_INVALID | 400 | Choose a valid UTC day within the configured horizon. Do not silently widen the horizon or change the date. |
| MONITORING_CARDINALITY_RESPONSE_LIMIT | 422 | Lower the output rank limit and/or select an exact metric, then retry. Return no partial success. |

Per the service owner's corrected publication contract, malformed/missing parameters,
explicit empty metric, noncanonical/zero limit and excess UTF-8 bytes use
VALIDATION_FAILED/400, matching the shared convention and history. Authorization, missing scope, concurrency,
license and SQL outcomes retain the current history classifications, including
409 STORAGE_COMMIT_OUTCOME_UNKNOWN and the five native transaction errors.
Keep request_id and the actual HTTP status consistent with the Problem body.

A lower rank limit is response-size recovery; it does not reduce the native
index scan. Do not offer it as the remedy for native scan exhaustion. The shared
UNAVAILABLE code combines transient and query-budget causes, so SDK/Help must
not promise that blind automatic retries resolve it. Exact-metric narrowing is
explicit; no raw-index fallback, automatic cap increase or arbitrary selector.

At this fixed source the four codes are not yet in public problemStatuses.
That is an expected publication task while Owns has no new route: add and test
the HTTP mappings and append the same four codes to the public monitoring
Problem enum together. A service-only error test is not a public status test.

## Validation status resolution and wire boundary

The owner initially proposed VALIDATION_FAILED/422, but corrected that proposal
to 400 for the independent private HTTP candidate. This agrees with fixed source
3cebfcef, whose shared PublicProblem registry and history declaration use 400.
No shared mapping change is required. Keep the distinct
MONITORING_CARDINALITY_RESPONSE_LIMIT/422 classification unchanged.

ProblemWithPolicy derives the status from e.Error() and its registry; merely
returning a status alongside an error from a private handler is not wire proof.
The owner states that the new private candidate is unregistered and untested;
its exact commit/tree and original authenticated dispatcher still need their
own qualification. The supplied 3cebfcef Mac unit result does not cover that
later HTTP candidate. Do not expose the route or four special codes early.

Keep limit typed as an integer in OpenAPI. Its canonical ASCII decimal spelling
is checked by the HTTP adapter; a string-only pattern keyword on an integer
schema does not establish that wire rule.

## SDK and Help projection checks

The pinned oapi-codegen 2.8.2 maps format=date to openapi_types.Date. Pinned
runtime 1.8.0 serializes that value with DateFormat=2006-01-02 in query parameters.
The pinned openapi-python-client 0.29.1 uses datetime.date and isoformat().
This supports a proper date-only projection without a generator change. It is
source inspection, not a generated cardinality-client wire receipt: public work
must still assert the exact YYYY-MM-DD query, including a leap day, and preserve
the metric through the generated client's own query encoder. Do not reuse the
history date-time input or silently strip a datetime's time zone.

The current Help schema projection retains format, description, required,
minimum/maximum and maxLength. It drops pattern and arbitrary x-* extensions.
It also retains the parameter's description, but does not project the operation's
free-form description into the operation index. Consequently place the UTF-8
byte unit, canonical positive ASCII limit spelling and omission-only policy
default in the relevant parameter descriptions. A byte-limit extension alone
will not reach current Help. This does not require broadening the shared Help
schema whitelist or inventing an integer pattern. Verify both entrances' output
and localized recovery text when the operation is actually published.

## Verified supplied Mac unit evidence

The supplied receipt and raw Go JSON log at
/private/tmp/obs110-mac-unit-qkhcpg9a were independently hashed and inspected.
The receipt fixes source 3cebfcef9f321364862a11d8b773c714facb6f68 and tree
0f5c800140e1036a157de62ba4c339b4e2780468. Go 1.27.3 on darwin/arm64 ran the
monitoring subtree and monitoringmetrics with -json -race -p 2 -count=1,
GOPROXY=off and GOSUMDB=off: exit 0 in 8.771 seconds. Four packages passed,
46 top-level tests and 139 pass events were present; all seven cardinality
top-level tests passed. The four relay command packages had no tests.

Raw log SHA-256:
fcd057faad562d94984f05a85e393764e39838ecfc17a8ae7eb10f74e18b9342.
Receipt SHA-256:
9a4d682bb796ecb9092f4b396b84828489d08cf2b7241ee2a53eb38c3a65c3bc.
The copied mac-unit-proof.json preserves the receipt bytes. The raw log was
inspected at the supplied local path and is not copied into this review package.
This evidence is Mac unit testing only; Linux, real PostgreSQL, pinned native
VictoriaMetrics, original authenticated dispatcher and installed acceptance
remain open. The source stays private and no public route is registered.

## Required handoff before public work

Retain the verified Mac unit receipt, then obtain Linux and native receipts on an
exact source and schema, with real PostgreSQL and pinned VM 1.155.0. Then qualify the original authenticated
dispatcher, not a shortcut handler: malformed/duplicate query, byte boundaries,
zero, app-only nodes=[], Unicode exact selection, scan refusal followed by
narrowing/retry, revocation during I/O, audit failure/unknown commit and absent
success data. Preserve history and private-TLS regressions after shared helper
factoring.

The public owner can then add the route contract, regenerate Go/Python and
Web/Help, verify all four Problem decoders and prepare localized recovery copy
with the Web owner. SDK/Help must state the effective-policy default and UTF-8
byte unit; neither 100 ranks nor a lookback example is a universal default.
Installed scan/status-duration budgets and raw-horizon alignment need the
delivery owner. None of this closes per-agent ingest admission or OBS-110.

## Primary references

- [Pinned VM status handler](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/app/vmselect/prometheus/prometheus.go#L562-L618):
  separates date and rank handling from the scan budget.
- [JSON Schema maxLength](https://json-schema.org/draft/2020-12/json-schema-validation#section-6.3.1):
  the standard bound measures characters, not encoded bytes.
- [OpenAPI 3.1 Schema Object](https://spec.openapis.org/oas/v3.1.0.html#schema-object):
  retains those JSON Schema semantics.
- [Go URL.Query](https://pkg.go.dev/net/url#URL.Query):
  parse errors require ParseQuery rather than silent pair dropping.
- [RFC 9457 status](https://www.rfc-editor.org/rfc/rfc9457.html#section-3.1.2):
  the response status and Problem status must agree.

- [Pinned Python date property](https://github.com/openapi-generators/openapi-python-client/blob/v0.29.1/openapi_python_client/parser/properties/date.py)
  and [date wire template](https://github.com/openapi-generators/openapi-python-client/blob/v0.29.1/openapi_python_client/templates/property_templates/date_property.py.jinja).
- [Pinned Go date mapping](https://github.com/oapi-codegen/oapi-codegen/blob/v2.8.2/pkg/codegen/typemapping.go)
  and [runtime query serialization](https://github.com/oapi-codegen/runtime/blob/v1.8.0/styleparam.go).

Local fixed-source references: tools/codegen/sdk/generate.py,
tools/codegen/web-help.mjs, series_cardinality.go,
series_cardinality_store.go, metrics_observation.go,
http/fleet_history.go and platform/httpserver/problem.go. No dependency,
model or dataset was added by this review.

## Independent private HTTP candidate review

Source 2dfbb85e79f81c5fec09664d5137323acb2701d1 was inspected separately
from the frozen 3cebfcef unit candidate. Its parser bounds raw query input at
4096 bytes, admits the fully percent-encoded 1024-byte metric plus the remaining
parameters, distinguishes omission from explicit empty values, preserves exact
metric text, rejects duplicate/unknown/malformed options, and accepts only a
canonical date-only value and canonical positive rank spelling. Intrinsic
Validate keeps private empty-metric/zero-limit defaults while bounding UTF-8
bytes and fixed ranks. The configured rank/date horizon remains service-owned.

The shared observation path checks canonical workspace and request UUIDs before
SQL or store admission, and rechecks authority/audit after the store read.
Owns and default Handle still exclude the new path. Generic validation remains
400 and the four special public mappings are still absent. This is static code
inspection; no new public route, SDK or Help output is qualified, and the older
Mac service unit receipt does not cover this later private HTTP code.

The later private HTTP source d8ad7e790e2d08a267df819ce8547225983fc08d
changes the new test's typed-nil assertion relative to 2dfbb85e; the reviewed
product parser and service validation files are identical. The supplied
http-unit-proof.json and raw log were independently hashed and inspected:
Darwin arm64 Go 1.27.3 race checks passed both selected packages in 9.062
seconds, with seven top-level tests and 49 pass events. Receipt SHA-256 is
b8511e24c4b2ace105e34d07f004654c8b9e956f310951d362a9e691fd582b3b;
raw log SHA-256 is
fc6712d61c151be59d4fa52202c6c10cf421dcc4afc81067692dbdcd088f14f2.
The copied private-http-unit-proof.json preserves the receipt bytes. These
checks leave Linux/native/original-dispatcher publication boundaries open.

## Native index-mode qualification

Pinned VM 1.155.0 declares disablePerDayIndex=false in vmstorage/main.go.
The index_db.go containsTimeRange path admits the global range when that flag
is true, and containsDate substitutes globalIndexDate. A date-only request by
itself therefore does not establish per-day cardinality semantics. The default
is source evidence, not proof of an installed process's effective flags.

Before final Help/statistic wording, preserve the exact native version and
startup arguments and qualify two UTC dates in one data partition with distinct
series populations. Assert both date results and their difference; a one-day
fixture or an equal population cannot distinguish a global-index result. If the
installed mode disables per-day indexing, report that limitation explicitly
instead of describing the response as a daily series count. No runtime flag or
public contract is changed by this review.

Primary source: [VM vmstorage flag and storage options](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/app/vmstorage/main.go)
and [pinned index date/range handling](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/lib/storage/index_db.go).

The owner reports a later Linux race pass recorded by research-only 261a6fd2
on diagnostic copy 6f70. Its exact source and raw receipt have not been
independently verified here; the Mac evidence and open native/dispatcher
boundaries above are unchanged. No earlier failed Linux run is superseded by
this report alone.

