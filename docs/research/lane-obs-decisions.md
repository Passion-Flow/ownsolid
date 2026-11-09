# Observability and storage lane boundaries

The lane qualifies the native telemetry and storage paths without editing the
shared API, agent, gateway, offline installer or web applications. Production
enablement needs those owners wherever shown below. The roadmap boxes retain
their original acceptance criteria.

| Item | Boundary |
| --- | --- |
| OBS-101 | Node and GPU publishers require an authenticated agent ingest path and producer-side series budgets. Service and app publishers must join the same scope contract. |
| OBS-102 | VictoriaLogs is already separate from PostgreSQL. A complete agent-side rate limit and log delivery path requires agent and offline configuration changes. |
| OBS-103 | Trace propagation needs coordinated context handling in the gateway, workflow and job runtimes before exporting spans to an in-site collector. |
| OBS-104, OBS-105 | The operator examples prove native offline rack grouping, scope isolation, silence, webhook and TLS SMTP. Rule administration, acknowledgement-based escalation, offline installer wiring and three vendor relay paths remain open. |
| OBS-106, OBS-110 | Dashboards and cardinality exploration require authorized query APIs and the web monitoring surface. The inherited snapshot endpoint does not expose those queries. |
| OBS-107 | Customer export needs an installation-owned destination policy and scope isolation for Prometheus/Grafana and log systems. Exposing the underlying stores would bypass the control-plane scope boundary. |
| OBS-109 | vmalert can route fleet health rollups to a separate long-retention instance. The installer must configure the second store and the console must query the appropriate tier. |
| OBS-111 | The node exporter journal rule runs on existing snapshots. Continuous collection and a deployed evaluator need installation wiring. Burn-rate alerts need authoritative eligible and failed request counts at the same service, tenant and workspace boundary. Rule administration still needs the Console. |
| OBS-112 | Model-route probes require scoped local test credentials, route inventory and a managed worker lifecycle. |
| OBS-113 | Native snapshot and fresh-directory recovery passed. Scheduling, backup publication and recovery integration are owned by the installation and reliability paths. |
| NET-101 | The existing model and dataset S3 ingress needs the private SeaweedFS and PostgreSQL profile. Checkpoint and backup use of the contract requires their own consumers. |
| NET-102 | Real Distribution transport passed with path-relative upload locations. The full product item needs an installed in-site registry and the user and agent consumption paths. |
| NET-103, NET-104 | Network and RDMA status and safe NFS mounts require agent collectors and workload execution integration. |
| NET-105 | Per-tenant backend identity, rotation and refusal require installation configuration and tenancy authority alongside the object-store adapter. |
| NET-106 | Each Kubernetes namespace needs the default-deny policy from the cell workload owner and an isolated zero-egress run. |
