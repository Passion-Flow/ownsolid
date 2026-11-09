# Scoped node scrape budgets (OBS-101)

The first supported exporter path uses the installed node exporter and a
private vmagent, with exactly one target per enrolled node. The node's
identity and tenant/workspace/site/cell labels come from trusted inventory
export. The exporter cannot change them. Configure HTTPS, disable redirects,
ignore exporter timestamps, and filter metric and label names before writing
to VictoriaMetrics. Keep remote write and inventory paths private. The
operator must provision endpoint identity, trusted CA and egress control.

[Prometheus scrape configuration](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#scrape_config)
distinguishes a response-body cap, a sample cap and label limits.
[vmagent's native limiter](https://docs.victoriametrics.com/victoriametrics/vmagent/#cardinality-limiter)
tracks unique post-relabel series per target and exports its per-target
budget, occupancy and discarded samples. In the
[pinned source](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/lib/promscrape/scrapework.go)
the limiter is a [process-local Bloom filter](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/lib/bloomfilter/limiter.go)
refreshed every 24 hours. False positives, agent restart, target remapping
and duplicated scrapers can admit extra series. Use it as a load guard
rather than a durable hard quota or an authorization boundary. A
per-scrape sample limit alone does not cover churn across successive
scrapes. A global remote-write limit can throttle unrelated targets.

The profile also limits each response before the sample cap because
vmagent needs a whole body to enforce both scrape limits. A drop in the
distinct-series budget leaves known series flowing. An oversized scrape
fails as a unit. Use a named metric and label allowlist for the first
node-exporter profile and record the separately owned GPU, service and
app additions.

The [pinned Bloom filter](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/lib/bloomfilter/filter.go)
uses 16 bits per budgeted series. At 2,000 series and 10,000 targets, that
is about 40 MB for one filter generation, before overlap on refresh, Go
object costs, scrape buffers, TLS, disk queues and remote-write batches.
The [10,000-node store test](telemetry-scale.md#linux-amd64-qualification)
measured the raw store's ingest, query latency and allocated bytes with
17 million series. Target scraping and sustained retention at that
size require a separate workload.

The existing [VictoriaMetrics 1.155.0 license](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/LICENSE)
covers vmagent and VictoriaMetrics under Apache-2.0, and the
[node exporter license](https://github.com/prometheus/node_exporter/blob/v1.12.1/LICENSE)
is Apache-2.0. Go is [BSD-3-Clause](https://go.dev/LICENSE). They allow
commercial use, offline operation and redistribution subject to notices.
The loopback fixture uses generated exporter records and introduces no
dependency, model or external dataset.

The acceptance must use actual vmagent and VictoriaMetrics on loopback
with local TLS, two independently labeled targets, stable samples before
churn, and a noisy target that attempts spoofed scope and new series.
Observe discarded samples only for the noisy target, continued samples
from both targets, and no spoofed scope labels or unsupported families
in the store. Qualify the response/sample limits separately. A product
claim also requires enrollment-driven trusted inventory, exporter TLS
provisioning, installed job definitions, and the remaining producer
families and budget enforcement.

## Verified scoped scrape path

On Linux amd64 with loopback only, TestScopedNodeScrapeBudgets passed with
vmagent and VictoriaMetrics 1.155.0. Both HTTPS targets used the fixture CA.
The test waited for stable samples, then introduced new series and
exporter-controlled tenant, workspace, node and job labels. The noisy
target reported dropped series while both targets continued to send their
stable values. Only the discovery labels were stored and the unapproved
metric family was absent. A separate sample overflow failed the noisy
scrape, the target recovered on a conforming scrape, and an oversized
response body failed it again. The other target stayed healthy. Both
telemetry processes and the HTTPS fixtures stopped on test exit.

## Trusted discovery publication

The node and component contracts carry enrollment scope and lifecycle.
The existing node exporter recipe listens on loopback. It carries no
authoritative remotely reachable TLS endpoint or collector identity.
Keep endpoint assignment with the operator. The protected assignment
file pins one workspace and maps enrolled node UUIDs to HTTPS target
IP and port values within explicit private or loopback CIDRs. Use a
JSON object keyed by node UUID to fit 10,000 full IPv6 endpoints within
the protected reader's 1 MiB cap. Use an
IP certificate SAN and the collector's private CA. A protected file
supplies no tenant, site or cell labels. Do not infer an address from
agent facts, accept redirects or use DNS aliases in this first path.

Use the selected PostgreSQL repository and the current Admin diagnostics
fence for the installed tenant and Workspace. Intersect the bounded
approved IDs with active enrolled nodes in that exact scope. Site and
cell come from their existing persisted inventory. An active node stays
listed while its exporter is unavailable so the scrape can report it.
Disabled, removed and foreign nodes leave no target. A disabled
Workspace can produce an empty list for its current platform Admin.
Do not expose inventory publication over the existing HTTP endpoint.
Audit the authorized snapshot before handing it to the private writer.

[VictoriaMetrics file service discovery](https://docs.victoriametrics.com/victoriametrics/sd_configs/#file_sd_configs)
accepts a JSON array of target groups. Publish one file per Workspace
as nodes-<workspace UUID>.json, using a dedicated operator-owned local
directory per installation. Limit input to 10,000 endpoints per file
and use one bounded SQL query. Reject duplicate node IDs, duplicate
canonical addresses, invalid labels and endpoints outside the
allowlist. Serialize publication through the local directory lock.
Write and sync a private temporary file and rename it inside the
opened root. [Linux rename](https://man7.org/linux/man-pages/man2/rename.2.html)
provides atomic visibility and [directory fsync](https://man7.org/linux/man-pages/man2/fsync.2.html)
is required for durable directory entries. A sync error after rename
leaves publication status uncertain. Failures before rename preserve
the prior complete file. Explicit empty output withdraws all targets
in that Workspace at the next file discovery poll.

Use the existing Go and PostgreSQL dependencies and licenses.
Go, the [pinned x/sys file-lock support](https://github.com/golang/sys/blob/v0.49.0/LICENSE)
and the existing [UUID generator](https://github.com/google/uuid/blob/v1.6.0/LICENSE)
use BSD-3-Clause. These allow offline commercial use and redistribution
with notices. No new
dependency, model or external dataset is introduced.

In a loopback-only namespace, enroll synthetic nodes in PostgreSQL,
publish a protected assignment, and let the pinned vmagent and
VictoriaMetrics binaries scrape an HTTPS fixture. Confirm authoritative
scope labels and the absence of foreign targets. Disable a node, refresh,
and verify withdrawal from actual vmagent target discovery. Verify that
a denied actor and malformed configuration cannot replace the existing
file. Exercise concurrent publisher admission and output directory
protection with focused tests.

The installer and endpoint owner still provision the collector, TLS
relay or exporter, private CA, per-Workspace file binding and refresh
supervisor. File discovery observes the last complete snapshot while
the publisher is unavailable. The installed supervisor must stop stale
collection when it cannot reestablish trusted inventory. A refresh
does not retract historical samples or guarantee immediate removal
before the configured discovery poll.

### Verified enrolled inventory

On Linux amd64 with loopback only, TestNodeScrapeInventoryThroughVmagent
used PostgreSQL 18.6 with pgvector 0.8.7, vmagent 1.155.0 and
VictoriaMetrics 1.155.0. A different Workspace's enrolled node was
omitted even though the protected assignment listed it. Portal and
revoked Admin authority and an out-of-policy address left the previous
file unchanged. The stored sample carried the exact enrolled scope
despite conflicting labels in the HTTPS response. The collector withdrew
the disabled node on its next poll. Refreshing a disabled Workspace
published an empty array.

The same test used 10,000 synthetic active node rows and full IPv6
endpoint strings. The protected assignment was 890,120 bytes and the
published file was 2,690,002 bytes. Policy parsing, the PostgreSQL query,
the diagnostic audit and durable publication took 247.7 ms in that run.
The collector was stopped before that measurement. This measures
inventory generation on one test host. The scrape and retention limits
remain as described above. The PostgreSQL and telemetry fixtures stopped.
