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

### Long retention for fleet health aggregates

Run the [fleet recording rules](alerting/fleet-rollup-rules.yaml) in a separate
vmalert process. Point -datasource.url at the short-retention raw store and
-remoteWrite.url at a second VictoriaMetrics instance with a longer
-retentionPeriod. Give the two stores disjoint data directories. Use the raw
store for node drilldowns and the long store for the
ownsolid:cell_nodes_observed:count and ownsolid:cell_nodes_reachable:count
series. A zero reachable count is meaningful only when the observed count
exists for the same cell.

The native test above also verifies that vmalert writes these aggregates
only to the long store. The operator sets the retention periods on each
VictoriaMetrics instance.

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

The fixture stops each store before starting the restored instance and
queries both restored values. Installation scheduling and recovery remain
separate from this check.
