# Observability module review

The 2026-11-09 review retains the open-source VictoriaMetrics and VictoriaLogs
decision in [ADR 0009](../decisions/0009-telemetry-and-log-stores.md).
[VictoriaMetrics ingestion and cardinality controls](https://docs.victoriametrics.com/victoriametrics/vmagent/)
support bounded labels, sample and series limits, and a bounded forwarding
buffer. A global approximate series limit complements per-source admission.
It cannot provide a tenant or agent quota by itself.

[vmalert](https://docs.victoriametrics.com/victoriametrics/vmalert/) evaluates
metrics and [LogsQL](https://docs.victoriametrics.com/victorialogs/vmalert/)
rules, writes recording-rule samples to VictoriaMetrics and can restore alert
state from it. [Alertmanager](https://prometheus.io/docs/alerting/latest/alertmanager/)
groups, deduplicates, inhibits and silences alerts. A cell and rack are appropriate
grouping keys for node failures when attached by a trusted topology source.
Group wait lets related alerts arrive before delivery. Rack-level alerts can
inhibit individual node alerts only within the same authorized scope. Keep
routing and group timing distinct from acknowledgement-based escalation.

The ingest through restore chain is covered by OBS-101 to OBS-113. One
missing capability is detecting failure in the telemetry or notification
path itself. A dashboard or alert evaluated by the same failed path cannot
report its own outage. OBS-206 covers tracking ingest drops, stale sources,
evaluation failures and failed deliveries, with a periodic heartbeat checked
by an independent in-site receiver that reports missed deliveries. Grafana
documents an analogous [alert delivery heartbeat](https://grafana.com/docs/grafana/latest/alerting/configure-notifications/manage-contact-points/integrations/configure-irm/).
The heartbeat can run against an offline receiver and adds no cloud dependency.
