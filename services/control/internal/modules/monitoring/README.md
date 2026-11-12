# Scoped Admin monitoring snapshots

A caller cancelled immediately after a known SQL commit receives a zero snapshot
and a cancellation error. The diagnostic audit remains committed exactly once;
unknown COMMIT still takes precedence and is never replayed. Current selected-SQL
controls cover role/user/Workspace/tenant revocation and real authenticated TLS
HTTP/2 diagnostics independently from the raw metrics SSE/process controls.

This module composes bounded Admin observations from existing owners. It adds no telemetry listener, independent worker, metrics database or second connection pool. [service.go](service.go) requires matching identity, Workspace, license, component/node and distribution owners plus the configured HTTP recorder.

The Control root wraps its already selected primary and optional machine HTTP
chains with the recorder. Their installation-lease admission checks remain in
the chain when monitoring is enabled; a lost lease returns service-unavailable
before entering business code and the recorder observes that refusal. The
composition regression is `TestMonitoringPreservesInstallationLeaseAdmission`
in `cmd/server`.

Get checks Admin audience/platform authority and current installed tenant/Workspace in the selected transaction. The returned snapshot includes license state, process/HTTP and runtime observations, gateway window and paged node observations. Diagnostic-read audit and trusted license-clock updates share the commit; a lost commit acknowledgement is not a successful read. Missing measurements remain distinguishable from a measured zero.

Monitoring requires the locked Workspace to be active, even when the ordinary
Admin directory permits inspecting a disabled Workspace. Disabled monitoring
reads return `ACCESS_DENIED` and a zero snapshot before license-clock/metrics
collection or diagnostic audit. Re-enabling that scope permits a later explicit
read using the same current authorized Admin session.

[config.go](config.go) owns snapshot concurrency, deadlines, limits and the matching recorder policy. The control root loads MonitoringConfigFile. This module uses existing persistent authorities rather than adding a migration. Cursor format, bounded admission and sanitized error codes must stay consistent with the HTTP contract.

Synchronize [monitoring configuration](../../../../../config/examples/monitoring/README.md), [M30](../../../../../docs/milestones/m30-reliability), platform/monitoringmetrics, component/distribution observation sources, HTTP/frontend contracts and [reliability harnesses](../../../../../tools/quality/reliability). Changes to a metric's source or null semantics require consumer and capacity-report updates; a dashboard must not invent an observed value.

Focused native check: `go -C services/control test ./internal/modules/monitoring`. Native SQL, real full-server observations and capacity workloads are separate acceptance profiles. Capacity reports describe their actual process, request and dataset sizes; they do not establish unmeasured production scale.

## In-site rack reachability policy

The [vmalert rules](alerting/fleet-rules.yaml) and
[Alertmanager example](alerting/alertmanager.example.yaml) work with
VictoriaMetrics 1.155.0, vmalert 1.155.0 and Alertmanager 0.34.2.
Provision the pinned binaries on the operator network. This is a manually
operated policy, pending the ownsolid rule and silence management flow.

1. Configure an in-site Prometheus-compatible scraper to attach trusted
   site_id, cell_id, rack_id and node_id labels to exactly one
   job="ownsolid-node" target per node. Keep target labels authoritative.
   Send the scraper's up series to the local VictoriaMetrics instance.
2. Write the approved intranet webhook URL to the protected file named by
   url_file. Give only the Alertmanager service account read access. Use
   HTTPS with the local CA for a relay reached over the network. Keep
   Alertmanager's listener private, turn off clustering for a single
   instance, and mount a persistent directory at its --storage.path.
3. Point vmalert's -rule at fleet-rules.yaml, -datasource.url,
   -remoteWrite.url and -remoteRead.url at the local metrics store, and
   -notifier.url at the private Alertmanager. Keep the metrics, evaluator
   and notifier on the site's private network. Limit receiver egress to
   the approved local relay.

At least two observed targets must continuously fail before a rack alert can
fire. Alertmanager inhibits matching node alerts within the same site, cell
and rack. Nodes without a rack assignment use individual notification
groups. A vanished scraper needs its own health alert.

To run the native integration check, verify the three Linux amd64 upstream
release archives against their published SHA-256 digests. Export
OWNSOLID_TEST_VICTORIA_METRICS, OWNSOLID_TEST_VMALERT and
OWNSOLID_TEST_ALERTMANAGER as absolute paths to the extracted binaries.
In the alerting directory run the following on a Linux development host:

    GO111MODULE=off go test -c -o /tmp/ownsolid-alerting.test routing_integration_test.go
    sudo --preserve-env=OWNSOLID_TEST_VICTORIA_METRICS,OWNSOLID_TEST_VMALERT,OWNSOLID_TEST_ALERTMANAGER \
      unshare --net sh -ec 'ip link set lo up; /tmp/ownsolid-alerting.test -test.v -test.timeout 5m'

The test binds ephemeral loopback ports and stops the three processes after
the check. It verifies the webhook batch and queries the notifier's active,
inhibited and silenced states.

### Occurrence acknowledgement candidate

The candidate uses the existing authenticated webhook parser and a dedicated
real Alertmanager observation and escalation route. It checks a single alert
occurrence per group and uses a PostgreSQL row and fenced attempt history.
The fixture verifies the first authorized responder wins, fresh occurrences
remain independent, and silent or resolved alerts do not acquire new
delivery claims. The candidate is compiled only with the
`alerting_candidate` build tag. The product listener retains its existing
delivery path.

Set `OWNSOLID_TEST_ALERTMANAGER` to the verified Alertmanager 0.34.2
binary and `OWNSOLID_TEST_POSTGRES_IMAGE` to the verified, preloaded
PostgreSQL 18.6 image ID. Run on Linux with permission to enter the
disposable container's network namespace.

    bash services/control/internal/modules/monitoring/alerting/testdata/check-local-escalation.sh

The runner creates the DDL only in its disposable database. See the
[candidate boundary and evidence](../../../../../docs/research/alert-occurrence-escalation.md).

### Local SMTP delivery

The [SMTP example](alerting/alertmanager-smtp.example.yaml) uses implicit
TLS and a site CA file. Set the relay address and sender and recipient
mailboxes to values approved for the customer intranet. For a relay that
requires authentication, set auth_username and auth_password_file. Add
the email_configs entry to the webhook receiver if both delivery paths
should share the rack grouping and inhibition policy.

From the repository root, point OWNSOLID_TEST_ALERTMANAGER at the verified
Alertmanager 0.34.2 binary and run the TLS SMTP check as root on Linux:

    unshare --net sh -ec 'ip link set lo up; bash services/control/internal/modules/monitoring/alerting/testdata/check-local-smtp.sh'

It reuses the repository's disposable SMTP sink and refuses to start if
the namespace has a non-loopback interface. It starts and stops both
processes and verifies the alert subject and recipient in the received mail.

### Site-local Matrix delivery

Use the [relay settings](alerting/matrix-relay.example.json) and
[Alertmanager receiver example](alerting/alertmanager-matrix.example.yaml)
on a dedicated operator network. Provision a local homeserver with
federation disabled and a private, non-federating, unencrypted room
and bot device for each tenant. Give each bot membership only in its own
room and use a distinct access token. Restrict room membership and history
to the authorized audience. Keep external push gateways and egress disabled.

Configure explicit private IP addresses and ports for the relay listener
and homeserver HTTPS origin. Their certificates need the corresponding
IP SANs. Configure both CA paths and install the same per-tenant webhook
token in protected files readable by the respective Alertmanager and relay
accounts. Give the relay account separate, protected Matrix token files.
Each receiver URL file points to the relay's
`https://<private-address>:<port>/v1/alertmanager/<tenant-id>`.
The receiver name, tenant group label and all member tenant labels must
match. Restrict Alertmanager API access to trusted operators and rules.
Keep tenant labels in every group, silence and inhibition boundary.
The sample uses separate node groups for nodes without a rack.

Build from the repository root and start with an owned, protected JSON
settings file:

    go -C services/control build -o /absolute/path/to/matrix-relay ./internal/modules/monitoring/alerting/cmd/matrix-relay
    /absolute/path/to/matrix-relay -config /etc/ownsolid/relay/matrix-relay.json

Keep the delivery timeout below the Alertmanager webhook timeout.
A retryable Matrix failure returns 503 so Alertmanager can repeat the
same notification. A permanent upstream refusal returns 422. The relay
accepts at most 256 complete alerts and 256 KiB per webhook, and renders
one plain-text room event with at most ten alert details. Identical
firing reminders collapse under the same Matrix transaction. Resolutions
and new starts have their own identities. Preserve bot devices and
homeserver transaction history for retries across process restarts.
Credential or routing changes require a relay restart. The relay logs
tenant, outcome, transaction digest and processing time for local
collection.

For a native Linux test, set `OWNSOLID_TEST_ALERTMANAGER` and
`OWNSOLID_TEST_TUWUNEL` to checksum-verified Alertmanager 0.34.2 and
Tuwunel 1.9.4 binaries. Run the following from the repository root
as a user allowed to create an isolated network namespace:

    bash services/control/internal/modules/monitoring/alerting/testdata/check-local-matrix.sh

The runner starts three disposable processes and stops them after
checking real room history and Alertmanager silence state. The [research
note](../../../../../docs/research/offline-alert-notifications.md)
records installation and Console handoff requirements.

### Site-local Feishu delivery

Use the [Feishu relay settings](alerting/feishu-relay.example.json) with the
[Alertmanager receiver example](alerting/alertmanager-matrix.example.yaml).
Rename each example receiver and its route references from `-matrix` to
`-feishu`. Point each Alertmanager URL file to the relay's private
`/v1/alertmanager/<tenant-id>` endpoint. Keep the inbound bearer token,
outbound hook identifier and signing secret in separate protected files
for each tenant. Configure the private HTTPS origin and CA for your
receiver. Its certificate must cover the configured private IP address.

    go -C services/control build -o /absolute/path/to/feishu-relay ./internal/modules/monitoring/alerting/cmd/feishu-relay
    /absolute/path/to/feishu-relay -config /etc/ownsolid/relay/feishu-relay.json

The receiver must implement the site contract on
`/open-apis/bot/v2/hook/<hook>`. For `OPTIONS`, return HTTP 204 with
`X-Ownsolid-Idempotency: durable-v1`. For a signed `POST`, persist the
quoted `Idempotency-Key` and the message atomically before returning HTTP
200, JSON `{"code":0}` and the same key in the response header. An equal
key with equal semantic content returns the same receipt after a retry,
including a restart. Authenticate each retry with its fresh timestamp and
signature. Conflicting content under the same key is a permanent error.
Keep keys for at least the site's replay horizon. A receiver that also
forwards to a downstream system must reconcile an ambiguous downstream
response before repeating delivery there.

For the isolated Linux test, set `OWNSOLID_TEST_ALERTMANAGER` to the
checksum-verified Alertmanager 0.34.2 binary and run with permission to
create a network namespace.

    bash services/control/internal/modules/monitoring/alerting/testdata/check-local-feishu.sh

The test qualifies the [controlled HTTPS receiver contract](../../../../../docs/research/offline-alert-notifications.md#verified-feishu-relay-contract).

### Site-local DingTalk delivery

Use the [DingTalk settings](alerting/dingtalk-relay.example.json) with the
[Alertmanager receiver example](alerting/alertmanager-matrix.example.yaml).
Rename the receiver names and references from `-matrix` to `-dingtalk`.
Keep the Alertmanager bearer, DingTalk-compatible access token and signing
secret in separate protected files per tenant. Configure the exact private
HTTPS IP and a site CA whose server certificate covers that address.

    go -C services/control build -o /absolute/path/to/dingtalk-relay ./internal/modules/monitoring/alerting/cmd/dingtalk-relay
    /absolute/path/to/dingtalk-relay -config /etc/ownsolid/relay/dingtalk-relay.json

The customer receiver serves `/robot/send`. For an unsigned `OPTIONS` on
that path without a query string, return HTTP 204 and
`X-Ownsolid-Idempotency: durable-v1`. For `POST`, validate the access token,
millisecond timestamp, HMAC-SHA256 signature and tenant binding. The
message is JSON with `msgtype: "text"` and `text.content`. The site profile
limits the JSON body to 16 KiB. Persist the
semantic message and quoted `Idempotency-Key` atomically. Return HTTP
200 with `{"errcode":0}` and exactly one matching receipt header.
On a valid replay, acknowledge the same semantic message even though
the signature or timestamp changed. A conflicting replay is a permanent
error. Keep the keys for the site's replay horizon and reconcile any
uncertain downstream operation before repeating it.

Disable query capture in listener and proxy access logs, error logs and
traces. The `POST` query carries the token and signature. Keep the site
receiver restricted to the operator network. The local relay reuses
Alertmanager's routing, silences and retry cadence.

Set `OWNSOLID_TEST_ALERTMANAGER` to the checksum-verified Alertmanager
0.34.2 binary and run with Linux network namespace permission.

    bash services/control/internal/modules/monitoring/alerting/testdata/check-local-dingtalk.sh

See the [wire contract and native evidence](../../../../../docs/research/offline-alert-notifications.md#site-local-dingtalk-signed-webhook-compatibility).

### Site-local WeCom delivery

Use the [WeCom settings](alerting/wecom-relay.example.json) and
[Alertmanager receiver example](alerting/alertmanager-matrix.example.yaml).
Rename the receiver names and references from `-matrix` to `-wecom`.
Put each tenant's site push key and Alertmanager bearer in separate
protected files. Configure the exact private HTTPS IP and its CA.

    go -C services/control build -o /absolute/path/to/wecom-relay ./internal/modules/monitoring/alerting/cmd/wecom-relay
    /absolute/path/to/wecom-relay -config /etc/ownsolid/relay/wecom-relay.json

The customer receiver serves `/cgi-bin/webhook/send`. Return HTTP 204
with `X-Ownsolid-Idempotency: durable-v1` on an unsigned `OPTIONS` with no
query. Authenticate the `POST` with the `key` query value and persist
the tenant-scoped message and quoted `Idempotency-Key` atomically.
Acknowledge successful storage and equal replays with HTTP 200,
`{"errcode":0}` and exactly one matching receipt header. Retain keys
for the site's replay horizon. Suppress query capture in listener and
proxy logs and traces because the query contains the push key.

Each notification contains one text message. The relay preserves its
group summary and shortens oversized details to meet the WeCom
2,048-byte UTF-8 text limit. Delivery follows the Alertmanager route,
silences and retry cadence. The receiver owns any downstream quota
policy and reconciliation.

Set `OWNSOLID_TEST_ALERTMANAGER` to the checksum-verified Alertmanager
0.34.2 binary and run with Linux network namespace permission.

    bash services/control/internal/modules/monitoring/alerting/testdata/check-local-wecom.sh

See the [wire contract and native evidence](../../../../../docs/research/offline-alert-notifications.md#site-local-wecom-message-push).

### Scoped node scrape budgets

The [vmagent example](alerting/node-scrape.example.yaml) reads nodes.json
and node-ca.pem from its configuration directory. The operator-owned
inventory must supply one HTTPS address per enrolled node with the trusted
tenant_id, workspace_id, site_id, cell_id and node_id labels. Use distinct
node IDs and restrict the exporter and remote-write routes to the private
operator network. Protect the CA and inventory from exporter writes. The
profile admits the initial CPU and available-memory families. Apply
inventory-owned rack assignment when enabling rack-level alerting.

The example sets a byte cap, a per-scrape sample cap and a per-target
distinct-series budget. Expose scrape_series_limit_samples_dropped in
operator monitoring. Tune the caps for the installed exporter and measure
ingress before scaling. See the
[scope, operational limits and evidence](../../../../../docs/research/node-scrape-budgets.md).

To reproduce on Linux amd64, set OWNSOLID_TEST_VMAGENT and
OWNSOLID_TEST_VICTORIA_METRICS to the verified 1.155.0 binary paths, then
run from the repository root with namespace access:

    work=$(mktemp -d)
    go -C services/control test -tags alerting_integration -c \
      -o "$work/alerting.test" ./internal/modules/monitoring/alerting
    cd services/control/internal/modules/monitoring/alerting
    unshare --net --pid --fork --kill-child sh -ec \
      'ip link set lo up; exec "$1" -test.v -test.run "^TestScopedNodeScrapeBudgets$" -test.timeout 90s' \
      sh "$work/alerting.test"
    rm "$work/alerting.test"
    rmdir "$work"

#### Enrolled node inventory

The internal RefreshNodeScrapeInventory entry point takes a current
platform Admin session, the Workspace UUID and operator-owned file
references from trusted composition. The protected assignment file
contains only approved endpoints and the Workspace pin.

    {
      "format_version": 1,
      "workspace_id": "a943b5a7-c4cf-4e3b-a12a-4b933fa51ef1",
      "allowed_networks": ["10.42.0.0/16"],
      "endpoints": {
        "a1790234-5080-451f-997f-f4f4af42bb80": "10.42.1.15:9443"
      }
    }

The module checks enrollment in the selected PostgreSQL repository,
commits the existing diagnostic audit, and atomically publishes
nodes-<workspace UUID>.json. Configure that exact file in the collector's
file_sd_configs. Place it in a private directory dedicated to this
installation. Run the publisher with ownership of the mode 0700
directory and give the collector read access through the installed
private mount and UID policy. Provision the private CA and an IP SAN
on the exporter endpoint. Remove the endpoint from the approved
assignment or refresh after disabling the node to withdraw it. A
current platform Admin can refresh a disabled Workspace to withdraw
its entire list.

Use RunNodeScrapeLease to require current publication before collector
startup and to stop the Workspace collector on denied refresh or expiry.
The deployment owner supplies a protected policy reference, refreshed
authorization and independent process containment if the supervisor
exits unexpectedly. The node endpoint owner supplies the TLS relay or
exporter. See [the collector lease boundary](../../../../../docs/research/node-scrape-budgets.md#collector-lease-and-authority-loss).
Set OWNSOLID_TEST_VMAGENT and
OWNSOLID_TEST_VICTORIA_METRICS to the verified 1.155.0 executables and
OWNSOLID_TEST_POSTGRES_IMAGE to the verified PostgreSQL 18.6 image ID
with pgvector 0.8.7. Run from the repository root with namespace access.

    bash services/control/internal/modules/monitoring/testdata/check-fleet-history.sh scrape-inventory

### Long retention for fleet health aggregates

Run the [fleet recording rules](alerting/fleet-rollup-rules.yaml) in a separate
vmalert process. Set its datasource URL to the raw VictoriaMetrics instance
and its remote-write URL to a second instance with an independent longer
retention and storage directory. Keep the ingestion, evaluation and query
interfaces private. Only the trusted scraper may write the node health
series, and only the evaluator may write the rollup series. Discovery must
assign and overwrite tenant_id, workspace_id, site_id, cell_id and node_id,
with exactly one trusted ownsolid-node target per enrolled node. Keep the
existing site/cell operational series out of workspace views.

The shared startup already loads the protected monitoring JSON. The installer
owner can provision the two stores and add this optional object to that file:

    "fleet_history": {
      "raw_origin": "https://raw-metrics.example.invalid",
      "long_origin": "https://long-metrics.example.invalid",
      "raw_horizon_seconds": 172800,
      "max_range_seconds": 7776000,
      "settling_seconds": 60
    }

Choose a raw horizon within actual raw retention and a settling delay covering
scrape, evaluation and remote-write lag. Use private HTTPS origins trusted by
Control's system CA store. Plain HTTP is restricted to loopback IP literals.
The same origin cannot represent both tiers. Omitting the long origin allows
raw-only reads within the raw horizon. Omitting the policy disables history.

The Admin history read uses
GET /api/v1/sites/{site_id}/cells/{cell_id}/admin/monitoring/fleet-history.
Use the installed site and cell IDs and exactly one workspace_id, from and to
query parameter. Times must be whole-second UTC values in
YYYY-MM-DDTHH:MM:SSZ form, with from before to. The installed database clock
determines the tier and rejects future or overlong ranges. The caller cannot
choose a tenant, native query or tier.

The original dispatcher resolves the cell, validates the Admin session and
Origin, and supplies the request ID and deadline. The service checks current
Workspace authority before store I/O and again before committing the read
audit. Success uses the shared data envelope; the complete response, including
its trailing newline, is at most 1 MiB. The snapshot keeps its existing path.
The raw cadence is 15 seconds and the rollup cadence one minute. Longer
requests widen the evaluation step to at most 1,440 points per cell. Missing
points are unobserved. Reachable zero with a positive observed count is a
measured observation.

A 400 MONITORING_HISTORY_RANGE_INVALID requires correcting the time window.
A 422 MONITORING_HISTORY_RESPONSE_LIMIT requires a smaller query. A 503
MONITORING_HISTORY_NOT_CONFIGURED or MONITORING_LONG_TIER_NOT_CONFIGURED
requires the operator to provision the corresponding protected settings.
MONITORING_RAW_TIER_UNAVAILABLE and MONITORING_LONG_TIER_UNAVAILABLE return
503 for failed store reads; restore private connectivity, certificate trust
or store health and retry the same request. A failed long tier never falls
back to raw. Authorization failures return no data, and failed reads do not
commit a successful history audit.

The [retention research](../../../../../docs/research/telemetry-retention-tiers.md#admin-route-integration)
separates module entry qualification from installer and Console acceptance.

For the Linux amd64 acceptance run, export OWNSOLID_TEST_POSTGRES_IMAGE to
the preloaded, verified PostgreSQL 18.6 and pgvector 0.8.7 image ID. Set
OWNSOLID_TEST_VICTORIA_METRICS and OWNSOLID_TEST_VMALERT to the verified
v1.155.0 binary paths. Run from the repository root with namespace access:

    bash services/control/internal/modules/monitoring/testdata/check-fleet-history.sh

For the small registered-route profile, use the normal CSRF/login/session,
verified TLS, formal response schemas and actual stores without repeating
the large-fleet workload:

    bash services/control/internal/modules/monitoring/testdata/check-fleet-history.sh history-http

To qualify the protected configuration and native TLS with separately
trusted raw and long origins, run the same fixture with the TLS selector.

    bash services/control/internal/modules/monitoring/testdata/check-fleet-history.sh history-tls

Provision the applicable private CAs in Control's system trust store.
Pass the raw CA to vmalert's `-datasource.tlsCAFile` and the long CA to
its `-remoteWrite.tlsCAFile`. Match the certificates to the configured
origins and restrict raw and rollup access by caller and route.
Stage old and next CA trust before changing issuers and qualify
Control and vmalert separately. The fixture reconnects at each read
and exercises same-origin certificate rejection and recovery.
See [the deployment evidence](../../../../../docs/research/telemetry-retention-tiers.md#verified-private-tls-handoff).

The runner uses loopback only and stops all of its processes and container.
See the [scope, test and scale evidence](../../../../../docs/research/telemetry-retention-tiers.md#scoped-history-read).

### Node exporter journal alert

Run the [journal rule](alerting/node-journal-rules.yaml) in a dedicated
vmalert process. Point `-datasource.url` at the private VictoriaLogs
endpoint and `-notifier.url` at the private Alertmanager. Keep the
evaluator, data store and Alertmanager API on the operator network. Keep
VictoriaLogs write access restricted to the trusted Control ingestion path.

    /absolute/path/to/vmalert \
      -httpListenAddr=127.0.0.1:8880 \
      -rule=/etc/ownsolid/alerting/node-journal-rules.yaml \
      -datasource.url=http://10.42.1.23:9428 \
      -notifier.url=http://10.42.1.24:9093

Route `OwnsolidNodeExporterJournalErrors` through the tenant receiver with
`group_by: [tenant_id, workspace_id, alertname]`. A workspace warning fires
when the latest snapshots from at least three distinct nodes include
an error-or-higher entry. The rule looks back five minutes for snapshots,
then accepts entries up to five minutes old at collection. A newer clean
snapshot, a removal marker, or expiry can clear the warning. The signal
reflects collected snapshots from the component logs action. A workspace
with no recent snapshots has no signal. View the affected workspace and
node logs through the authorized component view.

For the isolated Linux run, set `OWNSOLID_TEST_VICTORIA_LOGS`,
`OWNSOLID_TEST_VMALERT`, and `OWNSOLID_TEST_ALERTMANAGER` to the
checksum-verified VictoriaLogs 1.53.0, vmalert 1.155.0 and Alertmanager
0.34.2 binaries. Run with permission to create a network namespace.

    bash services/control/internal/modules/monitoring/alerting/testdata/check-local-journal-alerts.sh

See the [Linux validation and scale measurements](../../../../../docs/research/log-alert-rules.md#verified-linux-path).

### Private platform readiness probes

Put the [probe modules](alerting/platform-probe-modules.example.yaml)
and [vmagent scrape settings](alerting/platform-probe-scrape.example.yaml)
in your protected monitoring configuration directory. Configure the
site CA and substitute private readiness URLs. Keep the site and endpoint
labels stable and unique. The example uses IPv4, an exact HTTP 200
response and verified TLS. It uses no application credentials.

    /absolute/path/to/blackbox_exporter \
      --web.listen-address=127.0.0.1:9115 \
      --config.file=/etc/ownsolid/monitoring/platform-probe-modules.yaml

    /absolute/path/to/vmagent \
      -httpListenAddr=127.0.0.1:8429 \
      -promscrape.config=/etc/ownsolid/monitoring/platform-probe-scrape.yaml \
      -remoteWrite.url=http://10.42.1.25:8428/api/v1/write \
      -remoteWrite.tmpDataPath=/var/lib/ownsolid/platform-probes \
      -remoteWrite.maxDiskUsagePerURL=500MB

Permit access to the probe listener only from the trusted local
scraper and enforce an outbound allowlist for the exporter. Limit the
allowlist to the configured private target addresses and ports. The
example caps the per-destination disk queue at one 500 MB chunk.
vmagent drops the oldest queued samples after the queue reaches its cap.
A failed target can have `probe_success=0` while `up=1` means the
exporter responded to the scrape. Use `probe_http_status_code` to
distinguish application responses from a failed connection or TLS
handshake. Query the `ownsolid-platform-readiness` job in the private
VictoriaMetrics instance.

Set `OWNSOLID_TEST_BLACKBOX_EXPORTER`, `OWNSOLID_TEST_VMAGENT`,
`OWNSOLID_TEST_VICTORIA_METRICS` and `OWNSOLID_TEST_VICTORIA_LOGS`
to the checksum-verified Blackbox Exporter 0.29.1, vmagent and
VictoriaMetrics 1.155.0, and VictoriaLogs 1.53.0 binaries. Run the Linux
test with permission to create a network namespace.

    bash services/control/internal/modules/monitoring/alerting/testdata/check-local-platform-probes.sh

See the [pinned exporter and native validation](../../../../../docs/research/platform-endpoint-probes.md#verified-linux-path).

### Offline snapshot restore check

The [snapshot restore check](testdata/check-telemetry-restore.sh) starts
disposable stores, makes live snapshots and restores their data into fresh
directories. It uses VictoriaMetrics 1.155.0, the matching vmbackup and
vmrestore binaries, VictoriaLogs 1.53.0 and local rsync. Verify the upstream
release archives and extract those four binaries into one directory.
From the repository root on Linux, run as a user allowed to create a
network namespace:

    unshare --net sh -ec 'ip link set lo up; bash "$1" "$2"' sh \
      services/control/internal/modules/monitoring/testdata/check-telemetry-restore.sh \
      /absolute/path/to/verified/upstream-binaries

The fixture stops each store before starting the restored instance. It
reads back a metric and two UTC log partitions, with a separate LogsQL
query for each of two native accounts. Installation scheduling and
cross-store recovery coordination remain separate from this check.
