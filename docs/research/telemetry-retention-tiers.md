# Fleet telemetry retention tiers (OBS-109)

[ADR 0009](../decisions/0009-telemetry-and-log-stores.md) keeps the
open-source VictoriaMetrics edition. Use its
[single-node retention period](https://docs.victoriametrics.com/victoriametrics/single-server-victoriametrics/#retention)
for each store and the
[vmalert recording rules remote write target](https://docs.victoriametrics.com/victoriametrics/vmalert/#recording-rules)
for retained aggregates. Read high-resolution data from the existing short
retention store. Write compact cell-level aggregates to a separate instance
with its own longer retention, configured independently by the operator.
Do not use Enterprise per-series retention filters or downsampling.

VictoriaMetrics and vmalert v1.155.0 are already selected
[Apache-2.0 components](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/LICENSE).
Their license permits commercial use, redistribution with notices and offline
operation. No new dependencies, models or datasets are used.

The first rules record observed and reachable node counts by site and cell
every minute. The source is the trusted per-node scrape health series used
by OBS-104. Missing or stale input yields no output for that scope. An
observed cell with all targets down has a positive observed count and a
measured zero reachable count. Avoid an unconditional zero fallback because
it would hide the absence of a scrape source. Use independent query endpoints
rather than falling back silently to raw data for long-range queries.

The [scale qualification](telemetry-scale.md) shows why these aggregates are
useful. The example adds at most two series per cell per site and samples them
once a minute. It does not aggregate every metric family. The test must ingest
raw node samples into one store, evaluate with real vmalert, find exact scoped
counts in the other store, and confirm raw samples did not enter the long tier.
Deployment wiring, retention selection and console query routing still need
to consume this policy before the whole roadmap item can close.

## Verified path

TestFleetRollupsWriteOnlyAggregatesToTheLongTier passed in a Linux amd64
namespace with only loopback. One VictoriaMetrics instance used two-day
retention and the other 90-day retention. With one healthy and one failed
node in the first cell and one failed node in the second, vmalert recorded
observed/reachable pairs of 2/1 and 1/0. The unscoped series produced no
aggregate. Raw samples stayed in the short tier, and aggregates were only
present in the long tier. The test exercises tier routing and retention
configuration without waiting for the expiry periods.

## Scoped history read

Use the [Prometheus range response](https://prometheus.io/docs/prometheus/latest/querying/api/#range-queries),
VictoriaMetrics [repeated exact label filters](https://docs.victoriametrics.com/victoriametrics/single-server-victoriametrics/#prometheus-querying-api-enhancements)
and [MetricsQL last_over_time and label_set](https://docs.victoriametrics.com/victoriametrics/metricsql/).
The Go transport uses the [BSD-licensed standard library](https://go.dev/LICENSE);
no new dependency, model, data set or external service is introduced.

Keep the site/cell operational series and record a separate pair grouped by
installed tenant, workspace, site and cell. Assign those labels at the trusted
scrape/discovery boundary and overwrite untrusted copies. Require exactly one
target per enrolled node. At both tiers use fixed expressions, repeat both
server-authorized UUIDs as filters, and verify the returned scope. Use explicit
45-second raw and 60-second rollup sample windows. Missing intervals are absent
from the result; a measured zero has a positive observed count. Reject partial
responses and mismatched count pairs. Neither unscoped series nor a substitute
tier can answer a scoped request.

Place the read beside the existing Admin monitoring snapshot in its Service.
Share the current actor/installation/workspace fence. Run store I/O between
two short selected SQL transactions and recheck the same scope, license clock
and audit commit before publishing. Keep the current mounted HTTP contract
until its owner adds the presentation contract. Only telemetry aggregates
leave the monitoring stores; SQL retains the diagnostic audit and authority.

Use the database clock to select one tier for the whole request. Select raw
only if the start and its 45-second source window are within the configured
raw horizon. Keep the step aligned to the tier cadence. Widen it for large
ranges to bound chart points, while retaining a single source window at each
returned grid point. Empty results, absent configuration, a disconnected
store, foreign scope, and a revocation before publication must each be
distinguishable by the caller. The fixture must use actual PostgreSQL,
VictoriaMetrics and vmalert on loopback only and demonstrate that a failed
long tier produces zero requests to the raw tier.

At 10,000 nodes scraped every 15 seconds, this one raw family adds 57.6 million
samples/day. The documented five-cell layout adds 14,400 scoped rollup samples
per day (1,296,000 over 90 days), plus the same count if legacy operational
rollups are retained. Rollups scale with distinct tenant/workspace/site/cell
tuples, so five cells per workspace produce the same volume for each
workspace. These are sample counts. The scale qualification above records
measured disk use and query latency for the full raw shape; neither that
snapshot nor this projection establishes retention bytes or long-range p95.

VictoriaMetrics may fill recently evaluated range points with the last
settled value by default. The [pinned query implementation](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/app/vmselect/prometheus/prometheus.go)
and native fixture require a positive explicit latency offset. Control
requests 1 ms after clipping the evaluation grid to its configured settling
delay, so the store does not synthesize the returned tail.

### Verified scoped service path

On Linux amd64, TestScopedFleetHistoryOffline passed with PostgreSQL 18.6,
pgvector 0.8.7 and two VictoriaMetrics 1.155.0 instances. Native vmalert
1.155.0 replayed the production recording rules to the long store. The
namespace had only loopback and no IPv4 or IPv6 default route. The service
rejected foreign workspaces and a Portal session, isolated both selectors
within the result, and discarded a read after mid-query scope revocation.
An observed cell with all targets down returned a measured zero, and
missing intervals stayed absent. Unavailable and unconfigured long tiers
each returned their own error and generated zero raw-store requests.

The same run queried five cells of 2,000 simulated nodes each. A two-minute
raw read took 63.0 ms and a two-minute rollup read took 5.7 ms, including
SQL authorization and audit on the test host. Two snapshots of one input
family plus scope distractors allocated 7,188,480 bytes in raw and 413,696
bytes in rollup after shutdown, including index, cache and filesystem
allocation. The sample is a local fixture with a two-day raw store, a
90-day rollup store and one-hour routing horizon. It does not measure
retention expiry, 90-day query latency, sustained ingestion or storage
growth. PostgreSQL and both metrics stores stopped after the run.

## Private TLS deployment handoff

The [single-server VictoriaMetrics guide](https://docs.victoriametrics.com/victoriametrics/single-server-victoriametrics/)
and the pinned 1.155.0 binary expose native TLS at the listener.
[vmalert](https://docs.victoriametrics.com/victoriametrics/vmalert/#configuration)
accepts distinct CA files for its datasource and remote-write transport.
Control loads the same protected monitoring JSON through
`monitoring.LoadEnvironment` at startup. Its query transport uses
[Go's system certificate pool](https://pkg.go.dev/crypto/x509#SystemCertPool)
and does not use an ambient HTTP proxy or follow redirects. The TLS
handoff therefore needs native server certificates with matching SANs,
independent storage volumes, two private origins and the applicable site
CAs in the Control trust store. The evaluator's CA paths are separate
from the Control trust bundle. TLS key references and files remain local.

Qualify that connection contract before changing installation wiring.
Run two pinned VictoriaMetrics processes with native TLS and separately
issued private CAs in the loopback-only fixture. Use the same loader
as the startup owner, replay the checked-in rule with the pinned
vmalert and verify the Admin service reads both tiers. Redirecting the
long origin to a server with an untrusted issuer must yield a long-tier
error without delivering a query or committing a successful diagnostic
audit. Keep historical series in the same two stores. Go, PostgreSQL
and the Apache-2.0 VictoriaMetrics components use the licenses already
listed above. The test issues its own disposable certificates.

DEL still owns packaging, certificate rotation and the deployment ACLs.
Give the collector raw write access and the evaluator raw read and
long write access. Give Control only the two query routes. Keep imports,
administrative and metrics-store listeners private. This TLS fixture
validates the route and trust chain. It does not qualify installed
network policy, client identity or 90-day retention.

### Verified private TLS handoff

`TestFleetHistoryPrivateTLS` ran on Linux amd64 inside a
loopback-only namespace. PostgreSQL 18.6 with pgvector 0.8.7 supplied
the scope and audit. Two VictoriaMetrics 1.155.0 instances served
native TLS from independent directories with separately issued
CAs. The vmalert 1.155.0 process used the explicit datasource and
remote-write CA files to replay the repository's recording rule.
Control's protected startup configuration and private process trust
bundle reached both raw and rollup through the production history
reader. An unrelated certificate caused the long-tier unavailable
result before the query arrived. No successful audit was written for
that attempt. Both stores and PostgreSQL stopped.

Run the existing history fixture with the `history-tls` selector
and the pinned image and binary inputs in the
[monitoring README](../../services/control/internal/modules/monitoring/README.md#long-retention-for-fleet-health-aggregates).
The previously measured 10,000-node raw and rollup sizes and latency
remain the query-scale evidence. The TLS fixture exercises deployment
trust with a small sample. DEL still owns installed ACLs, certificate
lifecycle and isolated volumes.

## Bounded Admin HTTP candidate

The existing [monitoring adapter](../../services/control/internal/modules/monitoring/http/handler.go)
receives a cell-relative path and an already authenticated, Origin-checked
Admin actor from the identity dispatcher. Keep that registration and its
current snapshot contract intact. Stage a separate history method on the
same adapter so DEV can review it before adding any route, generated
contract, capability or localization. The method must retain both of the
history service's current authorization fences and its diagnostic audit.
The browser must never submit a tenant, tier, selector, store URL, or step.

Use a GET with exactly one Workspace ID and the two canonical
[RFC 3339 UTC timestamps](https://www.rfc-editor.org/rfc/rfc3339.html#section-5.6)
`from` and `to` at second precision. Bound the encoded query to 512 bytes.
The [Prometheus range API](https://prometheus.io/docs/prometheus/latest/querying/api/#range-queries)
allows user-selected queries and steps. Keep those server-owned here.
Return the selected tier, the evaluated grid and sparse cell points.
A zero reachable count with a positive observed count stays present.
Keep the existing 4 MiB upstream limit and add a 1 MiB successful JSON
body limit to the candidate. Encode and measure the exact
[JSON message](https://pkg.go.dev/encoding/json#RawMessage) before the
second authority fence and audit. Hold the encoded value until commit.
A transport-side length check after the service returns would record a
successful audit for a response rejected by the adapter. Streaming
partial success would disclose data before that decision.

Use a candidate-local status mapping until the public registry is
extended. Reuse the canonical registry for existing codes. Keep range
errors at 400, an oversized projection at 422, and unconfigured or
unavailable tiers at 503 with distinct codes. These are the response
semantics in [RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.html);
DEV owns publication through the existing
[problem details](https://www.rfc-editor.org/rfc/rfc9457.html) writer.
Never send store response text as a public error. Narrowing the time
range is the recovery path for a response over budget.

Exercise the candidate with the real Admin and Portal identities,
PostgreSQL, native raw and rollup stores and vmalert. Test malformed
and ambiguous HTTP requests before repository access. Recheck workspace
authority during an in-flight query and preserve the no-fallback case.
Use a controlled Prometheus-compatible response to hit the byte limit
without claiming a fleet scale result. Require no success body or
successful audit on denial. The same run measures the existing
10,000-node fixture and retains the recorded storage evidence. Encoding
does not change what is ingested or retained. No dependencies, models
or data sets are added.

### Verified bounded HTTP candidate

`TestScopedFleetHistoryOffline` exercised the unregistered handler
with actual Admin and Portal sessions, PostgreSQL 18.6, pgvector 0.8.7,
two VictoriaMetrics 1.155.0 stores and native vmalert in the
loopback-only namespace. Both tiers produced bounded JSON with the
selected grid, scoped cells and measured zero. The matching request
audit was committed for each successful body. A foreign workspace,
a Portal session and a mid-query workspace revocation produced no
successful body. The 24-cell over-budget response returned
`MONITORING_HISTORY_RESPONSE_LIMIT` with status 422 and no successful
audit. Failed, absent and over-budget long-tier reads made no
raw-store requests. Unit tests verified malformed inputs, a closed
public route and the candidate code/status pairs.

The 10,000-node two-minute fixture read took 70.6 ms raw and
6.9 ms rollup, including authority, encoding and test audit readback.
The stopped stores allocated 7,192,576 and 409,600 bytes respectively.
These are local fixture numbers for two snapshots of one input family
plus scope distractors. They do not establish production retention
size or long-range latency. Both stores and PostgreSQL stopped.

DEV owns dispatch through the existing Admin entrance, the public
problem registry, scoped OpenAPI and generated clients. DEL owns the
private store, CA and retention inputs. The candidate can be rerun
using the default `history` selector.

## Certificate replacement at the same long origin

The pinned [VictoriaMetrics listener](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/lib/httpserver/httpserver.go)
reloads its configured certificate and key files. A TLS connection
authenticates the peer during its
[handshake](https://www.rfc-editor.org/rfc/rfc8446.html#section-4.4.2).
Use the native listener's `-http.disableKeepAlive` in the fixture so
each request must prove the current certificate. This fixture setting
is unnecessary for ordinary operation. Established TLS connections
retain their negotiated trust until they close.

Before switching issuers, provision the old and next CA in the
authorized clients' trust bundles. Go's system roots are process
state. Restart Control or use the installed trust-update procedure
when that bundle changes, and qualify vmalert's separately configured
trust. The fixture preloads both permitted issuers and keeps the
origin and Control reader unchanged while the long listener reloads
first an unauthorized certificate and then the permitted replacement.
A bounded probe verifies each new leaf with its fixture CA so that
the readiness check cannot pass on the previous certificate.

Exercise the bounded HTTP candidate with real Admin authority.
During the unauthorized replacement require the long-tier
unavailable problem and no successful audit. Read the pinned raw
[range request counter](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/app/vmselect/main.go)
to prove no fallback. Confirm that the authorized raw request still
works, then check the same long origin again after replacement.
This tests listener reload and authorization with preinstalled trust.
Certificate issuance, process containment, trust distribution and
connection draining remain DEL's deployed acceptance. No
dependencies, models or data sets are added.

### Verified certificate replacement

`TestFleetHistoryPrivateTLS` kept the same protected configuration,
Control reader and native long listener through two certificate
replacements in the loopback-only Linux amd64 fixture. Initial vmalert
replay used the independent CA files. A handshake confirmed the
unauthorized leaf was active before the next long query. The candidate
returned 503 with `MONITORING_LONG_TIER_UNAVAILABLE`, no body and no
successful audit. VictoriaMetrics's raw range counter did not move.
A fresh raw read still returned data and committed its own audit.
The same long origin recovered with the pretrusted replacement and
committed exactly one success audit. PostgreSQL and both stores stopped.

This exercises trusted issuer overlap at process startup. DEL owns the
installed trust rotation, including vmalert's remote-write trust update
and draining already established connections when old trust is removed.
Run the `history-tls` selector with the pinned binaries to repeat it.

## Admin route integration

The source at main 203109c7 still owns only the snapshot in
`monitoring/http.Owns`. The normal identity handler already checks
the cell-relative monitoring family, request budget, Admin audience,
Origin and authenticated session before calling `Monitoring.Handle`.
Registering the exact FleetHistory path in the monitoring adapter can
therefore reuse that chain. Coordinate publication with DEV's scoped
OpenAPI, generated clients, Help and common problem registry before
claiming that the route is available.

The [shared success writer](../../services/control/internal/modules/identity/http/handler.go)
encodes `{"data": body}`. Go's
[Encoder.Encode](https://pkg.go.dev/encoding/json#Encoder.Encode)
appends a newline. The 1 MiB response budget must include those ten
bytes, in addition to the prepared data. The cap decision stays before
the second authority check and successful audit. A unit test must
encode the exact shared wrapper at the accepted boundary and reject
one additional byte. The existing 4 MiB store reply and time-grid
bounds continue to bound input work.

Use a small offline fixture through identityfixture.New,
identityhttp.New, a verified TLS listener, the public cell-scoped URL
and a real session cookie. Do not construct a trusted actor at the
history adapter in that test. Check no session, Portal, wrong cell,
foreign Workspace, revoked session and mid-query Workspace revocation.
Then exercise raw and long results, failed or unconfigured long,
response-budget refusal and successful retry against the actual
PostgreSQL and two native stores. Assert problem codes through the
shared writer, not the candidate-only mapper. Check the request ID
against committed audit, security headers and the complete response
size. Keep large-fleet fixtures separate from this route acceptance.
The existing Go, PostgreSQL and VictoriaMetrics dependencies and
licenses apply. No model or data set is added.

The existing protected monitoring file remains the typed startup
input. DEL owns installing raw/long stores, evaluator, CA trust,
retention and network permissions. ARC owns the selected serving
wrapper and installation lease if that composition changes. A normal
module HTTP fixture does not establish an installed delivery.

### Verified authenticated module entry

On 2026-11-12, source 93f9ed8b, based on the reusable schema and public
Problem registry at 9a56176e, passed
TestFleetHistoryAuthenticatedHTTPOffline with the race detector on Linux
amd64. The namespace had only loopback and no external route. The fixture
used PostgreSQL 18.6 with pgvector 0.8.7, the original identity dispatcher,
real CSRF and password login, issued session cookies and verified DNS/SNI
TLS. It composed the module from the dispatcher's actual owners and a
protected monitoring file. It did not construct an actor at the history
adapter or bypass an initialization failure.

Two native VictoriaMetrics 1.155.0 stores held the raw and recorded data;
native vmalert replay supplied the long-tier aggregates. Five synthetic
node-health series at two timestamps included a down node and foreign
tenant/Workspace controls. Both returned tiers matched the formal
FleetHistoryResponse schema, preserved measured zero and sparse points,
and returned an empty array for missing data. This small profile does not
repeat or replace the [10,000-node and 17-million-series qualification](telemetry-scale.md#linux-amd64-qualification).

The normal shared writer preserved the six public history problems,
including the complete-response cap, failed or unconfigured tiers and an
invalid range. Portal, Portal-token reuse at the Admin origin, no session,
wrong cell, unscoped path, foreign Workspace, Origin and extra query inputs
were refused. A Workspace disabled during native I/O and a session ended
through the actual Logout owner before publication released no data or
successful read audit. A stale cookie did not reach the raw store. Restored
Workspace authority, fresh login and repaired store access allowed retry.
Each successful response had exactly one committed audit for its server
request ID. Long-tier faults and missing configuration made no raw request.
The oversized native-protocol reply and outages were explicit fault doubles;
successful data came from the native stores.

Seven successful small reads, including TLS, schema validation and the
fixture's SQL audit readback, measured 12.12 to 27.08 ms with the race
detector. After graceful stop, the five-series fixture used 258,048 raw
and 294,912 long allocated bytes. These are one short fixture's observations,
not production p95, representative retention sizes or a large-fleet query
claim. The existing scale note remains the source for measured fleet storage
and query costs.

The same source also passed the separate history-tls selector with the race
detector: an untrusted replacement leaf at the same long origin was refused,
raw remained readable without a long fallback, and a pretrusted replacement
restored rollup reads. Both selectors stopped their two native stores and
removed their disposable PostgreSQL containers. Owned module race tests
passed before the test-only import and revocation-fixture fixes. The complete
public operation, SDK/Help publication, installed delivery and Console
acceptance remain separate; this run establishes the registered module
entry and shared response writer.

### Transaction problems in the public contract

The [shared Problem registry](../../services/control/internal/platform/httpserver/problem.go)
also preserves STORAGE_DEADLOCK, STORAGE_LOCK_TIMEOUT, STORAGE_CANCELLED,
STORAGE_TIMEOUT and STORAGE_COMMIT_ROLLED_BACK as 503 responses.
[Service error normalization](../../services/control/internal/modules/monitoring/service.go)
can produce them from the selected transaction owner. Keep those codes in the
public Problem enum and operation error declaration as well as the existing
STORAGE_COMMIT_OUTCOME_UNKNOWN and STORAGE_UNAVAILABLE. A strict generated
client must parse every preserved refusal before deciding what to retry.
Unknown vendor failures still collapse to STORAGE_UNAVAILABLE.

The normal native history profile above does not inject these five database
outcomes. Their contract/SDK parsing tests belong to DEV's publication
qualification. Whole public operation acceptance still requires that corrected
contract, bilingual explanations and the same registered module source.
