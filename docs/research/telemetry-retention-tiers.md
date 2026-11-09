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
