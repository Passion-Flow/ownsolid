# Observability and alerting

Module reviewed: 2026-11-09. See [the module review](../../research/observability-module-review.md).

Metrics, logs, traces and alerts for the platform and the fleet, in their own stores,
fully offline.

- Owns: `services/control/internal/modules/monitoring`, `platform/monitoringmetrics`, deploy telemetry stack.
- Depends on: architecture-and-scale.

## P0 Foundation

- [x] OBS-001 Select the time-series and log stores by license, offline packaging and single-binary operation (P0) - Evidence: commit e11ba81 (ADR 0009)
- [x] OBS-002 Telemetry and logs separated from the control-plane database (P0) - Evidence: commits f329a1a, 32df90d, 33061e5 (ADR 0012)

## P1 Enterprise release

- [ ] OBS-101 Node, GPU, service and app metrics with per-agent series budgets (P1) - Catalog: F230
  - [Scoped node scrape profile](../../research/node-scrape-budgets.md#verified-scoped-scrape-path) qualifies TLS discovery labels, per-target series and sample limits, and recovery. Enrollment-driven inventory and GPU, service and app publishers remain open.
- [ ] OBS-102 Log collection with agent-side rate limits and retention (P1)
- [ ] OBS-103 OpenTelemetry tracing across gateway, workflows and jobs (P1) - Catalog: F235
- [ ] OBS-104 Alert rules with severity, grouping, silencing and escalation (P1)
  - [Scoped acknowledgement candidate](../../research/alert-occurrence-escalation.md#verified-loopback-path) qualifies durable confirmation and retry through real Alertmanager and PostgreSQL. Product migration, authorized Admin write, Console workflow and grouped escalation remain open.
- [ ] OBS-105 Offline notification channels: email, webhook, WeCom, DingTalk and Feishu over internal relays (P1) - Catalog: F002, F567
  - [Site-local Matrix relay](../../research/offline-alert-notifications.md#verified-matrix-path) qualifies one authenticated chat path.
  - [Site-local Feishu relay](../../research/offline-alert-notifications.md#verified-feishu-relay-contract) qualifies signed delivery to a controlled private receiver. Vendor-side integration, remaining channels, installer and Console work remain open.
  - [Site-local DingTalk relay](../../research/offline-alert-notifications.md#verified-dingtalk-relay-contract) qualifies signed, durable delivery through a controlled private endpoint. Vendor-side deployment remains open.
  - [Site-local WeCom relay](../../research/offline-alert-notifications.md#verified-wecom-relay-contract) qualifies durable bounded text and a 40-alert group through a controlled private endpoint. Vendor and product wiring remain open.
- [ ] OBS-106 Dashboards for platform health, fleet, GPU and model usage (P1)
- [ ] OBS-107 Export to customer Prometheus, Grafana and log systems (P1) - Catalog: F235
  - [Native scoped federation prototype](../../research/tenant-telemetry-export.md#native-filter-prototype) qualifies the store filter. Authenticated export identity and installed store isolation remain prerequisites for customer integration.
- [x] OBS-108 Load tested at about 17 million active series (P1) - Evidence: [Linux amd64 scale qualification](../../research/telemetry-scale.md#linux-amd64-qualification)
- [ ] OBS-109 Retention tiers and long-range rollups built on recording rules, because downsampling and multiple retentions are Enterprise-only in the selected store (P1)
  - [Scoped history read](../../research/telemetry-retention-tiers.md#verified-scoped-service-path) verifies raw/rollup selection, active Workspace authority, measured zero and no raw fallback. Installer, Admin API and Console wiring remain open.
- [ ] OBS-110 Series cardinality explorer with per-tenant and per-agent limits (P1)
  - [Scoped cardinality fixture](../../research/series-cardinality-scope.md#verified-query-boundary) qualifies native daily counts and ranks and identifies query-usage metadata that must be omitted. Authorized API, Console and ingest limit enforcement remain open.
- [ ] OBS-111 Log-based alert rules and SLO burn-rate alerts (P1)
  - [Node exporter journal alert](../../research/log-alert-rules.md#verified-linux-path) qualifies tenant-scoped native evaluation and offline notification. Continuous log collection, service SLI inputs, rule administration and packaged evaluator wiring remain open.
- [ ] OBS-112 Black-box probes of platform endpoints and model routes (P1)
  - [Private HTTPS platform probe](../../research/platform-endpoint-probes.md#verified-linux-path) qualifies a native health endpoint and remote-written results. Installed entrance and model-route probes remain open.
- [ ] OBS-113 Telemetry store backup and restore with the open-source tools, scheduled by ownsolid (P1)
  - [Offline multi-day recovery](../../research/telemetry-backup.md#verified-path) qualifies native metric backup and log-partition recovery across two UTC days and two accounts. Scheduled publication and recovery coordination remain open.

## P2 Fleet operations

- [ ] OBS-201 Cross-layer observability fields with privacy budgets (P2) - Catalog: F651
- [ ] OBS-202 SMS and voice alert gateways (P2)
- [ ] OBS-203 Capacity planning reports and forecasts (P2)
- [ ] OBS-204 Platform fault drills and a support status board (P2) - Catalog: F652
- [ ] OBS-205 Authenticated, encrypted traffic between telemetry store nodes in a cell, because mTLS between cluster components is Enterprise-only in the selected store (P2)
- [ ] OBS-206 Telemetry pipeline self-monitoring and independent notification-path heartbeat with missing-signal detection (P2)

## P3 Multi-site

- [ ] OBS-301 Global aggregation across sites (P3)

Catalog rows absorbed: F002, F230, F232, F235, F408, F508, F567, F651, F652.
