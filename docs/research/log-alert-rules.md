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
