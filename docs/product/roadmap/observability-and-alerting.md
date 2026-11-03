# Observability and alerting

Metrics, logs, traces and alerts for the platform and the fleet, in their own stores,
fully offline.

- Owns: `services/control/internal/modules/monitoring`, `platform/monitoringmetrics`, deploy telemetry stack.
- Depends on: architecture-and-scale.

## P0 Foundation

- [ ] OBS-001 Select the time-series and log stores by license, offline packaging and single-binary operation (P0)
- [ ] OBS-002 Telemetry and logs separated from the control-plane database (P0)

## P1 Enterprise release

- [ ] OBS-101 Node, GPU, service and app metrics with per-agent series budgets (P1) - Catalog: F230
- [ ] OBS-102 Log collection with agent-side rate limits and retention (P1)
- [ ] OBS-103 OpenTelemetry tracing across gateway, workflows and jobs (P1) - Catalog: F235
- [ ] OBS-104 Alert rules with severity, grouping, silencing and escalation (P1)
- [ ] OBS-105 Offline notification channels: email, webhook, WeCom, DingTalk and Feishu over internal relays (P1) - Catalog: F002, F567
- [ ] OBS-106 Dashboards for platform health, fleet, GPU and model usage (P1)
- [ ] OBS-107 Export to customer Prometheus, Grafana and log systems (P1) - Catalog: F235
- [ ] OBS-108 Load tested at about 17 million active series (P1)

## P2 Fleet operations

- [ ] OBS-201 Cross-layer observability fields with privacy budgets (P2) - Catalog: F651
- [ ] OBS-202 SMS and voice alert gateways (P2)
- [ ] OBS-203 Capacity planning reports and forecasts (P2)
- [ ] OBS-204 Platform fault drills and a support status board (P2) - Catalog: F652

## P3 Multi-site

- [ ] OBS-301 Global aggregation across sites (P3)

Catalog rows absorbed: F002, F230, F232, F235, F408, F508, F567, F651, F652.
