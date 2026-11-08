# Offline fleet alert routing (OBS-104)

## Sources and licenses

[VictoriaMetrics vmalert](https://docs.victoriametrics.com/victoriametrics/vmalert/)
evaluates PromQL/MetricsQL rules and writes pending alert state to VictoriaMetrics
for restart recovery. [Prometheus Alertmanager](https://prometheus.io/docs/alerting/latest/alertmanager/)
groups alerts, inhibits symptoms, stores silences and deduplicates deliveries.
[Grafana Alerting](https://grafana.com/docs/grafana/latest/alerting/set-up/configure-alertmanager/)
also supports external Alertmanager routing and silences. Reuse the upstream
evaluators and delivery pipeline instead of implementing alert lifecycles in SQL
or making each node send a notification.

[vmalert v1.155.0](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/LICENSE)
and [Alertmanager v0.34.2](https://github.com/prometheus/alertmanager/blob/v0.34.2/LICENSE)
are Apache-2.0. Commercial use, redistribution and offline use are permitted
subject to license and notice requirements. The acceptance driver uses the
BSD-3-Clause Go standard library and synthetic scrape outcomes. It uses no
models, third-party libraries or customer data.

## Decisions and failure boundaries

Evaluate only one scrape-health series per node for this rule family. A trusted
in-site scraper sets site, cell, rack and node labels and publishes `up=0` when a
scrape fails. A failed scrape establishes that the endpoint is unreachable. It
does not identify loss of power. Alert on individual nodes and aggregate a rack
where at least two observed targets all fail continuously. Preserve the
underlying alerts for diagnosis. Group by site, cell, rack and alert name. Mute
individual node alerts while the corresponding rack alert fires.
[Alertmanager treats absent equality labels as empty](https://prometheus.io/docs/alerting/latest/configuration/#inhibit_rule),
so both source and target matchers explicitly require each scope label.
Use group wait to allow related alerts to arrive. Send through an in-site URL
file with redirect following disabled. Keep the evaluator and notifier on a
private network accessible to operators only. Tenant-authored rules, relays
and silences require a scoped control-plane boundary before exposure.

A scrape stream that stops emitting is distinct from a node outage. OBS-206
covers detecting a broken scrape or delivery pipeline. Alertmanager can route
by severity, but its repeat timer does not implement acknowledgement-based
escalation. The ownsolid console (S-506), scoped audited rule and silence
management, escalation and offline bundle wiring are required for the full
roadmap tick. This operator configuration covers the rack policy without
changing the shared API, installer or UI.

## Acceptance

With VictoriaMetrics, vmalert, Alertmanager and a local webhook in a namespace
without external routes, publish 40 failed scrapes for one rack and a separate
failed node with the same rack name in another cell. The first notification for
the affected rack contains one critical rack alert and no node alerts. The
other cell's node alert is delivered separately. A scoped silence suppresses a
third cell without affecting either delivery. An alert with incomplete topology
cannot inhibit any node. Verify state and inhibition through the real notifier
API and ensure no notifications leave the namespace.

## Verified path

On Linux amd64, TestRackOutageDeliversOneOfflineNotification passed with
VictoriaMetrics and vmalert 1.155.0 and Alertmanager 0.34.2 in a loopback-only
network namespace. The webhook received one critical rack alert and one
separate node alert for the other cell. The notifier API showed all 40 affected
nodes inhibited, and one node in a third cell suppressed by an API-created
silence. The unscoped input produced no alert. All test processes stopped.
