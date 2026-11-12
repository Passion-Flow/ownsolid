# Component journal alerts and service error budgets (OBS-111)

## Component journal alert

[VictoriaLogs integrates with vmalert through LogsQL stats queries](https://docs.victoriametrics.com/victorialogs/vmalert/).
Use its native group type and the existing Alertmanager, with one expression
over the managed node exporter's existing VictoriaLogs records. The server
binds the tenant, workspace, node and component to the authenticated result
and validates the [structured journal body](../../api/agent/v1/maintenance.go).
[systemd journalctl](https://www.freedesktop.org/software/systemd/man/258/journalctl.html)
defines priorities zero through three as error or higher.

Select node exporter streams and a five-minute collection window before JSON
decoding. Use the [LogsQL field_max and unpacking operators](https://docs.victoriametrics.com/victorialogs/logsql/)
to inspect only the latest snapshot per tenant, workspace and node.
Ignore tombstones, snapshots without a validated source or availability,
missing scope, non-error entries and entry timestamps outside the five
minutes before collection. A node counts once even if the agent returns
eight copies of an error or an operator requests repeated snapshots. Alert
at three affected nodes per workspace. Only the stable scope, severity and
count reach Alertmanager. Journal text, task IDs and node IDs are absent.
Use the result as a warning about recent collected evidence. A quiet window
may mean the operator did not request a snapshot.

Run vmalert and the ingestion and query interfaces on the private operator
network, behind the same access boundary as the existing component-log
store. Only the authenticated Control ingestion path may write these
streams. Log body fields cannot supply routing scope. Route using both
tenant and workspace and keep the evaluator's shared store credential out
of tenant-facing clients. Use the existing tenant receiver and silence
mechanisms.

Prefer native evaluation over a Go poller, which would duplicate parsing,
retries, notification state and monitoring. A text grep would misclassify
journal messages, and counting raw entries would count repeated snapshots
and multiply alert instances. An error percentage over these snapshots
has no valid request denominator. Keep log data in VictoriaLogs and use
no SQL mirror. Continuous collection and its retention and producer rate
limits belong to OBS-102.

No runtime dependency is added. The existing
[VictoriaLogs 1.53.0](https://raw.githubusercontent.com/VictoriaMetrics/VictoriaLogs/v1.53.0/LICENSE),
[vmalert 1.155.0](https://raw.githubusercontent.com/VictoriaMetrics/VictoriaMetrics/v1.155.0/LICENSE),
and [Alertmanager 0.34.2](https://raw.githubusercontent.com/prometheus/alertmanager/v0.34.2/LICENSE)
are Apache-2.0. These releases allow commercial use, offline operation
and redistribution subject to the license and applicable notices. The
existing Go test runtime uses the [Go BSD license](https://go.dev/LICENSE).
The fixture reads the rule with the repository-pinned
[Go YAML 3.0.5](https://raw.githubusercontent.com/yaml/go-yaml/v3.0.5/LICENSE),
which carries MIT and Apache-2.0 terms and can be used and redistributed
commercially and offline subject to those terms. The test uses generated
records under the repository license, with no external model or dataset.

Qualify the exact rule against the pinned VictoriaLogs binary before
installation. The native test must insert the Control record format,
verify threshold and tenant/workspace isolation, verify latest-snapshot
deduplication, cover malformed and stale records and tombstones, and
observe both firing and resolved delivery in Alertmanager. Benchmark the
rule with 10,000 node streams and record dataset bytes, query latency
and the limits of that result. The rule does not store any recording
series, and its output cardinality is at most one result per workspace
with an affected node threshold crossing.

The isolated VictoriaLogs prototype selected exactly three affected nodes
from 16 records across two tenants and two workspaces, with duplicates,
a tombstone, missing tenant scope, future and stale entries, and warning
priority all excluded as applicable. It used the actual stats API.

## Verified Linux path

The isolated Linux amd64 run used VictoriaLogs 1.53.0, vmalert 1.155.0
and Alertmanager 0.34.2 with a loopback-only interface. The Control
telemetry client inserted component-log records. The shipped expression
generated one firing alert for each of two tenants that crossed the
threshold. A tombstone resolved the first tenant's alert while the second
stayed active. The test checked exact counts, tenant and workspace
boundaries, newer clean snapshots, old and future entries, malformed
records and suppression of repeated node entries. Raw journal text did
not reach the Alertmanager webhook fixture. The test used a two-second
evaluation interval in place of the production minute to shorten the run.

The isolated scale fixture loaded 10,000 node streams in 100 workspaces
across ten tenants, with one snapshot of eight entries per node. Unique
synthetic message text varied by node and repeated within a snapshot.
VictoriaLogs stored 3,651,719 data-file bytes after flushing 19,819,000
JSON-line bytes. Twenty local queries returned all 100 groups with 100
nodes each. Client-observed p50 was 113 ms, p95 was 140 ms, and maximum
was 185 ms with GOMAXPROCS=2 and a 256 MB VictoriaLogs cache budget.
These are post-ingest queries against a single synthetic window on one
host. Concurrent ingestion, long retention and customer data have not
been measured for this rule.

## SLO burn rate

The [Google SRE workbook](https://sre.google/workbook/alerting-on-slos/)
combines a short and long window at each budget burn threshold. To add an
ownsolid service SLO, the service owner must expose a stable, authoritative
count of eligible requests and a matching count of failed requests at the
same tenant, workspace and operation boundary, with documented outcomes,
retries and no-traffic semantics. The monitoring lane can then use these
counters to test short/long windows, missing and zero traffic, restarts,
overlapping alert suppression and recovery. Journal snapshots cannot
supply that numerator or denominator.

### Initial platform probe SLO

The existing HTTP recorder merges 4xx and 5xx as http_error and has no
request-level tenant/Workspace eligibility contract. Do not derive a request
SLO from that snapshot. Keep the request-driven path above pending its owning
service's authoritative numerator and denominator.

A separate initial path can measure the existing trusted platform readiness
probes. [Grafana's availability SLI examples](https://grafana.com/docs/plugins/grafana-slo-app/latest/sli-examples/availability/)
distinguish successful requests from successful synthetic observations and
calculate a gauge probe SLI from successful binary samples divided by all
observed samples. This is a behavior reference; no Grafana runtime, cloud
service, SDK or source is introduced. Use the already selected local Blackbox
Exporter, vmagent, VictoriaMetrics, vmalert and Alertmanager. The scope is one
site and one operator-assigned endpoint. It measures that private observer's
reachability, not customer request success, node availability or model output
correctness. Never present it as the request-driven SLO.

Prototype the exact MetricsQL outside product code first. The
[MetricsQL raw-sample rollups](https://docs.victoriametrics.com/metricsql/)
provide count_eq_over_time and count_over_time. For each window, count samples
exactly equal to zero as failures and divide by all observed probe samples.
Require all samples to be exactly zero or one, exactly one historical series
for the site/endpoint, and nonempty trusted site_id and endpoint labels. Drop
instance, target addresses and other source labels from the alert projection.
Reject duplicate series rather than weighting an endpoint by accidental scrape
replicas. Missing samples are not synthetic successes or an observed zero.

Use an explicitly selected 99.9 percent, 30-day policy for the prototype, with
the SRE workbook's 14.4-times budget threshold over both one hour and five
minutes, or six-times over both six hours and thirty minutes. Combine both
pairs in one alert expression so overlap produces one alert per scoped
endpoint. The production operator policy must explicitly select its goal,
period and scrape cadence; these prototype values do not establish a customer
SLO or add a default contract. A minute-level evaluation and a 30-second
scrape provide the initial cadence. Require at least 90 percent of nominal
samples in each window and a sample no older than 90 seconds. Sparse or stale
observations are unknown and cannot establish healthy service. The separate
OBS-206 pipeline-health path must expose loss of telemetry; a burn alert's
absence is not evidence of health.

The current probe example supplies two endpoints, independent of node count.
For an explicit 10,000-node shape with two to five cells and the same two
endpoints per cell, there are four to ten monitored endpoints. At 30-second
cadence this adds 11,520 to 28,800 probe-success samples per day; the existing
three probe metrics and scraper up contribute 46,080 to 115,200 samples per day
for those four selected families. Additional collector self-metrics, other jobs
and customer telemetry are excluded from that calculation.
These are inventory/cadence calculations, not measured storage or latency.
No recording series, node-ID dimension or SQL telemetry mirror is required.
Measure the native rule's on-disk bytes and query latency for that exact shape
before claiming capacity, recording the requested cache mode and its limits. Keep any measurements under competing
fleet ingestion separate from quiet-store measurements and from the existing
17-million-series qualification.

The native prototype must compare exact production-window queries with known
synthetic histories: fast and slower burn, overlapping pairs, recovery of the
short window while the long window remains breached, healthy data, observed
failure zeros, empty observations, partial coverage, stale data, nonbinary values, duplicate
series and identical endpoint names in different sites. Assert projected labels
and one result under overlap. Query-engine acceptance is distinct from live
vmalert/Alertmanager delivery and from product/Console admission. Until those
checks run, leave the expression outside product code and leave OBS-111 open.

Existing VictoriaMetrics/vmalert and Alertmanager versions and Apache-2.0
terms above apply. [Blackbox Exporter 0.29.1](https://github.com/prometheus/blackbox_exporter/blob/v0.29.1/LICENSE)
and vmagent 1.155.0 are Apache-2.0 as well. The external prototype uses only
the Python standard library under the
[PSF license](https://docs.python.org/3/license.html), allowing commercial and
offline use and redistribution under its terms. Synthetic samples use the
repository's license. No model, external dataset, new dependency or copied
proprietary implementation is introduced.

The prepared external inputs are
/root/lane-obs-tools/probe-slo-prototype-v1/probe-slo.metricsql, SHA-256
5a2776d00642c07cfc378d29693f547b66311519fd3c204c0bad3532475c9c4c,
and observe-probe-slo.py in the same directory, SHA-256
7c782387ee8142e02c99844db61d9fa62f5d6e73303fea09c35eea51ab3afe75.
The observer requires an already admitted loopback-only Linux PID namespace
with its own proc mount; it is not an admission runner and opens no lock. The existing admitting parent must keep
actual shared-lock custody until namespace and child exit. The observer binds
the pinned native binary hash, asserts its actual proc executable and arguments,
and starts two bounded, sequential disposable stores. It checks explicit
started/ready/stopped facts and a clean exit, retaining raw logs and data files.
No native process, namespace, compiler, installer request or extra lock admission
was started for preparation. Only Python AST syntax inspection has passed; the
MetricsQL expression and every native expectation remain unqualified.
