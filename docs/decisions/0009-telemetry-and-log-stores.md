# 0009: VictoriaMetrics and VictoriaLogs as the telemetry and log stores

- Status: accepted
- Date: 2026-11-04

## Context

OBS-001 selects a dedicated time-series store and log store, so that telemetry never
lands in the control-plane database. The roadmap sets the criteria: license, offline
packaging, single-binary operation, and a load test at about 17 million series. Both
stores ship inside a commercially licensed offline bundle and run on single nodes first
and in cells later.

## Decision

Use the open-source editions, pinned by digest:

| Store | Image | Digest |
| --- | --- | --- |
| Metrics | `victoriametrics/victoria-metrics:v1.155.0` | `sha256:f60dc268b54ecf87f6e09d740c2b732838aa9193090cc741417c9f6386a130df` |
| Logs | `victoriametrics/victoria-logs:v1.53.0` | `sha256:251121fa882af99b95ba0c230a4a2f412ea602d2698c64a96c58dc9842bb755d` |

### Options

| Option | License (from the project repository) | Result |
| --- | --- | --- |
| VictoriaMetrics, VictoriaLogs | Apache-2.0, single-node and cluster | Selected |
| Grafana Mimir, Grafana Loki | AGPL-3.0-only, with Apache-2.0 exceptions for parts of Loki | Rejected on license: both would ship inside a commercial bundle |
| Prometheus | Apache-2.0 | Rejected: server nodes are autonomous and a single server does not scale out, so 17 million series would need sharding that ownsolid builds itself |
| ClickHouse | Apache-2.0 | Not chosen: a general database that would need its own schema and collection layer. Not measured |

### What was run

On `ownsolid-cpu`, 2026-11-04, inside the zero-egress host of
[ADR 0008](0008-zero-egress-harness.md), with the images loaded from an archive and each
store running read-only, as a non-root user, with all capabilities dropped and
`no-new-privileges`.

| Check | Result |
| --- | --- |
| Offline operation | No outbound attempt during 5 minutes idle, or under load. Probes from the host and from each store's network namespace were recorded, so the recorder was watching |
| Binary inspection | No update or telemetry flag in either `-help`. Embedded hosts are documentation links and cloud service discovery, which is used only when configured. This does not prove absence |
| Packaging | Metrics image 57 MB and logs image 45 MB. One process each, one data directory |
| Metrics, 2 million series | 6 million samples, no failed batch, 4 CPUs and 6 GB limit, about 720 MiB resident |
| Metrics, 17 million series | 85 million samples in 5 rounds, no failed batch, no out-of-memory kill, 6 CPUs and 16 GB limit, 3.9 GiB in use, 2.5 GB on disk |
| Metrics ingest rate at 17 million | 244,000 samples per second while creating series, then 1.8 to 2.1 million per second. A 15-second interval needs 1.13 million |
| Metrics queries at 17 million | One node, 1,000 series: 882 ms cold, 16 ms after restart. One metric across a cell, 1,600 series: 18.9 s cold |
| Metrics restart at 17 million | Ready in 4 seconds with data intact |
| Logs | 2 million lines at about 750,000 per second, no failed batch, 4 CPUs and 6 GB limit, all lines present after a restart |

### Limits of this evidence

The 17 million run is a selection check, not the load test of OBS-108, which stays open.
It lasted about two minutes, on synthetic and highly compressible data, with no queries
running during ingest, and the generator shared the same 8 CPUs. Memory and disk at
steady state, with merges and a full retention window, are unknown, and so are the
compression figures for real data. The generator was a throwaway; OBS-108 brings one
that stays in the repository.

## Consequences

- **Enterprise-only features are not available**, and ownsolid does not buy or ship
  them: downsampling, multiple retentions, backup automation, mTLS between cluster
  components, LTS releases and FIPS builds. The roadmap owns the gaps: OBS-109 for
  retention and rollups, OBS-113 for backup and OBS-205 for traffic between store nodes.
- Without LTS releases, every release must move to a current open-source version and
  take its fixes that way. DEL-201 image scanning covers these images too.
- FIPS builds would need a separate decision if a customer requires them.
- Broad cold queries are slow at this size. Dashboards and alerts over a whole cell use
  recording rules instead of raw series (OBS-106, OBS-109).
- The logs image has no shell, so a Compose health check cannot run inside it. OBS-002
  has to check readiness from outside the container.
