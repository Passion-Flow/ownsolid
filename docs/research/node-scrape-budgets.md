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

## Collector lease and authority loss

[VictoriaMetrics file discovery](https://docs.victoriametrics.com/victoriametrics/sd_configs/#file_sd_configs)
and [Prometheus file discovery](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#file_sd_config)
refresh local target lists. Neither file format expresses a deadline.
A stale successful snapshot stays readable when the publisher stops.
Keep the discovery format and TLS scope unchanged. Bind the collector's
process lifetime to successful, fresh publication through a private
supervisor. Before launch require a fresh authorized publication in the
same run. Begin each lease before attempting the authorization and
publication, so a slow refresh cannot extend the prior deadline.
Allow renewal only when the full operation returns successfully
before both deadlines. Cancel and join the collector on a rejected
refresh or expiry. Use Go's
[monotonic duration measurements](https://pkg.go.dev/time#hdr-Monotonic_Clocks)
within the running supervisor. No package, model or dataset is added.

A per-workspace collector process avoids interrupting other scopes.
A file timestamp can be changed independently of authorization.
Publishing an empty file on error still depends on a working file
system and delays cancellation until the next discovery poll. A process
deadline is independent of a blocked refresh. The supervisor does not
promise retraction of history or of already queued samples.

DEL owns the systemd unit or equivalent process containment. A
[bound unit with ordering](https://github.com/systemd/systemd/blob/main/man/systemd.unit.xml)
can stop the collector after unexpected supervisor exit. Use
[control-group termination](https://github.com/systemd/systemd/blob/main/man/systemd.kill.xml)
or a qualified equivalent and a bounded stop deadline. Do not run
the collector as an independently restarted process reading the last
file. Mount the exact inventory and protected TLS settings per scope.
The deployed publisher must reacquire authorized authority at each
refresh, with policy controlling the refresh interval and maximum age.
A revoked role, expired session or blocked refresh must stop the
collector and require a new successful publication before restart.
Test forced supervisor death separately in the installed environment.

Qualify the boundary with real vmagent and verified HTTPS inside the
loopback-only PostgreSQL and VictoriaMetrics fixture. Observe scrape
requests at the endpoint. Keep the old file intact, revoke the Admin
role in PostgreSQL and verify the collector exits. Restore authority
and verify that a fresh publication can start a new collector. In a
separate run stall refresh until lease expiry and verify that the
collector stops despite the stale file. Require no further exporter
requests after process shutdown over multiple scrape intervals.
The published inventory and 10,000-target database query are unchanged.
Renewal adds one existing bounded refresh per workspace each interval.
At 10,000 targets the earlier 247.7 ms run measures that refresh
cost. It does not measure dedicated process overhead or the capacity
of a production site with many workspaces.

### Verified collector invalidation

The loopback-only Linux amd64 fixture used PostgreSQL 18.6 with
pgvector 0.8.7 and vmagent and VictoriaMetrics 1.155.0. The collector
sent real TLS scrape requests before both fault injections. Removing
the stored Admin role caused the next publication to fail, the
collector to stop and a new lease to refuse startup. Restoring the
role permitted a fresh publication. A later refresh was held until
after the current lease expired. The collector stopped before that
refresh unblocked. Both old discovery files remained byte-identical.
The exporter received no new HTTPS requests for 2.2 seconds after
each collector exited with a one-second scrape interval. The runner
stopped all owned processes and PostgreSQL.

The same run regenerated the 10,000-target inventory in 479 ms
from the 890,120-byte protected input to the 2,690,002-byte discovery
file, with the collector stopped. The lease changes refresh and
shutdown only. It does not change the number of samples, retention,
disk format or query path measured in
[the store qualification](telemetry-scale.md#linux-amd64-qualification).

## Systemd deployment containment

Use one foreground authorization supervisor and its vmagent child in
the same systemd service cgroup. The supervisor is the main process.
[systemd service lifecycle](https://github.com/systemd/systemd/blob/v255/man/systemd.service.xml)
can treat main process exit as the end of the service, and
[systemd kill settings](https://github.com/systemd/systemd/blob/v255/man/systemd.kill.xml)
stop all remaining members and bound the stop deadline. Disable automatic
restart and cgroup delegation for this boundary. An independently
restarted vmagent or a service whose lifetime follows any remaining child
could keep scraping the last successful inventory after supervisor death.

This is a drop-in for the authorized supervisor's unit. It supplies no
activation or target addresses. The base installation still needs an
operator-provisioned enrolled endpoint assignment and IP SAN, a private CA,
scope binding, and a foreground service that invokes the existing
authorization and lease on every publication. Pin the protected scope,
scrape configuration and CA for the lifetime of a run. A replacement
requires that run to stop and a new authorized publication before start.
Keep the collector child in the same cgroup and use a non-root service
identity. Empty, missing, unbound or stale authority must never select
targets by inference. The current Compose and Helm profiles leave the
collector disabled.

Reuse the host systemd and the previously tested Linux amd64 vmagent
1.155.0. The [systemd license inventory](https://github.com/systemd/systemd/blob/v255/LICENSES/README.md)
documents the existing host component's terms. The Apache-2.0
vmagent and BSD-licensed Go conditions above still apply. The fixture
uses generated TLS and synthetic metrics and adds no shipped dependency
or dataset.

Verify the actual drop-in under systemd in a recorded network namespace
with a real vmagent and a TLS loopback exporter. A controlled approved
assignment supplies the fixture authority. Reuse the existing lease and
inventory file reader and writer. Require initial TLS and a current
approval before spawning vmagent in the fixture. Prove the positive
scrape and refuse missing or expired approval, a foreign workspace and
an untrusted certificate. While scraping, kill the supervisor main PID,
let approval expire, and atomically replace a pinned configuration file.
Require an empty cgroup, no new scrape over multiple intervals, no
automatic restart, and an unchanged last published discovery file.
Keep the fixture proof separate from production enrollment, application
and GPU publishers and live authenticated Control composition.

### Disconnected systemd and vmagent acceptance

On `ownsolid-cpu` (Linux amd64, systemd 255.4), the drop-in ran with
vmagent 1.155.0, SHA-256
`8fa27fc1c4f4e690dceafb30b3c884ef70eabd718e1b6cc5892279977d8571a2`,
and the repository lease and publisher helpers at `8617a8b2`. The
network namespace had loopback for the TLS fixture and a TUN recorder
as its only IPv4 and IPv6 default route. TCP over IPv4 and IPv6 and a
UDP DNS query established that outbound attempts reached the recorder
and could not leave. The capture contained those three probes only.

Missing, expired and foreign approval and an untrusted TLS exporter
all left the existing inventory untouched and spawned no collector.
In three separate runs, the approved TLS fixture received real scrape
requests and both processes shared the service cgroup. `SIGKILL` of
the supervisor, approval expiry and an atomic same-bytes replacement
of the pinned scrape configuration each emptied the cgroup. After
each event, request counts stayed flat for more than two scrape
intervals, the inventory bytes were unchanged and systemd reported zero
automatic restarts. A manual start with the expired approval was also
refused. The fixture removed its runtime units, network and processes.

The replay source archive has SHA-256
`13bd94fa286cbb5e65d96448b409e57dbe9e99faf38f646d14477765a664b1a8`.
The transcript has SHA-256
`77d689910119400343f932f7e87b3fbcf4fdf3e19bcb2b727ad3dd37fecb7251`.
The approval and TLS exporter were generated test inputs. Production
startup still requires the foreground supervisor wired to authenticated
Control publication and a real protected registration for the site.
