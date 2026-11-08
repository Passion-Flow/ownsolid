# Architecture and scale

Module reviewed: 2026-11-04.

The invariants that let one control plane run 10,000+ GPU servers, and the simulator
that proves them. Getting these right early is cheap; retrofitting them across installed
agents is not.

- Owns: `services/control/internal/platform`, `services/agent/internal/client`, `api/agent`,
  `tools/dev/simulator` (new), `go.work`.
- Depends on: nothing. Every other module depends on this one.
- Planning loads at 10,000 nodes: about 1,000 liveness updates per second at a 10 s
  interval, 20 to 70 node failures per day, about 17 million active metric series.

## P0 Foundation

- [x] ARC-001 Repository layout, language and tooling checks enforced in CI (P0) - Evidence: commit 96f3e9f
- [x] ARC-002 Backend moved into the ownsolid layout with behavior unchanged (P0) - Evidence: commit df6fbd5
- [x] ARC-003 Merge the dozen deployment profiles into one topology model with sizes: single node, HA, cell, multi-cell, multi-site; remove profile branches from code, configuration and UI (P0) - Evidence: commits 17e8506, e38f5cf (ADR 0003, 0005)
  - Scope of the evidence: only `single-node` is accepted today. `api/release` keeps one release profile, still named `mvp-postgresql-cpu-v1`; DEL-101 renames it.
- [x] ARC-004 Cell ID and site ID in every resource key, table and API path, even with one cell (P0) - Evidence: commit 4d36bfa (ADR 0006)
  - Scope of the evidence: PostgreSQL only; MySQL and Dameng chains lack the columns until CMP-201 and CMP-202. Site and cell are fixed to `default` until ARC-202.
- [x] ARC-005 Agent protocol v1: agent-initiated long-lived gRPC stream over HTTP/2 through a stateless gateway tier, with long-poll fallback (P0) - Evidence: commit 0a8e805 (ADR 0007)
  - Scope of the evidence: the stream carries heartbeats and claims; results, renewals and transfers stay on HTTPS until ARC-006. The gateway runs in the control process (separate replicas are ARC-102), and the task wake is per process until ARC-011.
- [x] ARC-006 Task semantics: per-node sequence numbers, idempotent tasks, acknowledgements, resume from last ack, rate-limited full resync (P0) - Evidence: commit 9eaa366 (ADR 0011)
  - Scope of the evidence: PostgreSQL only; MySQL and Dameng leases carry sequence 0 until CMP-201 and CMP-202. Model preparation scenarios ran on synthetic command envelopes; those claiming through the full preparation service need a retained CPU training output and have not run. Resync limits are per control process until separate gateway replicas (ARC-102).
- [x] ARC-007 Server-paced heartbeat interval, jitter and reconnect delay, changeable at runtime; full-jitter backoff; retry at one layer only (P0) - Evidence: commit eaa67f6 (ADR 0013)
  - Scope of the evidence: PostgreSQL only; MySQL and Dameng serve the default pacing until CMP-201 and CMP-202. Changed through the API; the console page is FLT-113. `Retry-After` hints are ARC-008.
- [x] ARC-008 Per-operation limits and priority classes (quarantine above heartbeat above telemetry above bulk) with retry hints (P0) - Evidence: commit 6c04ac4 (ADR 0018)
  - Scope of the evidence: limits and seats apply per control process until separate gateway replicas (ARC-102). Settings go through `control_settings`; the console page is FLT-113. Shedding ran with one agent; fleet-scale load is ARC-015.
- [x] ARC-009 Cost rule: every agent call declares its server cost; calls whose cost grows with fleet size fail review (P0) - Evidence: commit 9d25841 (ADR 0020)
  - Scope of the evidence: PostgreSQL only; MySQL and Dameng keep their locks and `count(*)` until CMP-201 and CMP-202. Measured on 50 and 3,000 nodes through the node and training paths, which cover all ten calls; component, inference, preparation and registry operations are ARC-108. Load at fleet scale is ARC-015.
- [x] ARC-010 Liveness kept in gateway memory plus a small replicated store; PostgreSQL stores only state transitions and batched last-seen (P0) - Evidence: commit 2ee0fe1 (ADR 0022)
  - Scope of the evidence: PostgreSQL only; MySQL and Dameng keep the synchronous heartbeat until CMP-201 and CMP-202. The replicated store (NATS JetStream KV) ran on three real servers with one stopped; the installer deploys it with REL-101, and with one gateway none is needed. Ran with one real agent on a real install; fleet load is ARC-015.
- [x] ARC-011 Transactional outbox with batched delivery replaces LISTEN/NOTIFY; low-frequency reconciliation (P0) - Evidence: commit 1f9fb41 (ADR 0028)
  - Scope of the evidence: PostgreSQL only; MySQL and Dameng wake on one process until CMP-201 and CMP-202. The code had no LISTEN/NOTIFY: the outbox replaced an in-process wake. Two services on one database stood in for two gateways, since separate replicas are ARC-102. Dispatch at load is ARC-015.
- [x] ARC-012 Time-partitioned append tables for events, task history and audit, with partition drop; pooled connections; agents never hold database connections (P0) - Evidence: commit d216352 (ADR 0029)
  - Scope of the evidence: PostgreSQL only; MySQL and Dameng keep their tables until CMP-201 and CMP-202. `task_event_*` and `control_outbox` stay out on purpose: the first are updated in place, the second holds minutes of rows. `node_tasks` is pruned, not partitioned, and writes history through a trigger. Pooling is `pgxpool` per process; PgBouncer is left to REL-101 and ARC-102. A held claim is tested to hold no connection; a held stream shares its path but has no test of its own. Volume at scale is ARC-015.
- [x] ARC-013 Static stability: agents keep running work when disconnected and never kill on disconnect (P0) - Evidence: commit e22d96f (ADR 0032)
  - Scope of the evidence: training and model preparation on Docker nodes. A real container on `ownsolid-cpu` ran through an outage longer than the lease and an agent restart, with a counting process in place of the training image; no GPU run. Control's rules are shared SQL on all three databases and were tested on PostgreSQL. Inference containers keep serving, but an interrupted deploy task is not adopted after a restart. The full single-node install was not rerun; a control restart under fleet load is ARC-015.
- [x] ARC-014 Fake-agent simulator speaking the real protocol, thousands of agents per process, scripted heartbeats, task acks, reconnect and fault storms (P0) - Evidence: commit 48db9d9 (ADR 0033)
  - Scope of the evidence: 2,000 agents from one process against an offline single-node install on `ownsolid-cpu`, for four minutes at the default interval; this is a fault run, not the load test, which is ARC-015. Simulated nodes are read-only and run `collect_facts.v1`, so renewals, transfers and training events are not simulated. The `https_only` fault ran on unit tests against a fake control. All agents share one client address, so `agent_admission.address` and three other limits were raised for the run.
- [x] ARC-015 Load tested with 2,000 simulated nodes at twice the planned load; full reconnect within 5 minutes; control-plane restart leaves work running (P0) - Evidence: commits 3a15704, 5b8ddca, 2f7613b (ADR 0038, ADR 0040); `TestFleetLoad`, three runs in a row on `ownsolid-cpu`, 2026-11-07
  - Scope of the evidence: one control process and one PostgreSQL on one host, default resources with the raised limits named in the research note, simulated read-only nodes, runs of 25 minutes. Another project's tests were stopped within 15 seconds each time they started, outside the steady and restart stretches. ARC-105, ARC-203 and ARC-304 repeat it at their own sizes.
- [x] ARC-016 Break-glass admin path independent of agent traffic, plus an admission switch that sheds agent load (P0) - Evidence: commits bd27727, 2294c33 (ADR 0043, 0045); `TestBreakGlass` passed twice in a row with 2,000 simulated agents on `ownsolid-cpu`
  - Scope of the evidence: one control process on one host, simulated nodes, PostgreSQL only. With every agent seat waiting on a lock, sign-in 79 to 88 ms; the host path ran with the connection limit full and with PostgreSQL stopped. `TestFleetLoad` passed again with the six default seats.

## P1 Enterprise release

- [x] ARC-101 Versioned in-memory snapshot cache with watches for per-node reads (P1) - Evidence: commit 21460f6 (ADR 0047); `TestSingleNodeInstall` and `TestFleetLoad` on `ownsolid-cpu`, 2026-11-08
  - Scope of the evidence: PostgreSQL only; MySQL and Dameng keep reading the database until CMP-201 and CMP-202. Repeated heartbeats only: claims, results and transfers authenticate in their own transactions, and ARC-109 cuts their cost. One gateway, one load run, hit rate at load not recorded. The maximum-age bound rests on unit tests.
- [x] ARC-102 Gateway tier scales horizontally; agents connect to one or two gateways (P1) - Evidence: commit 541d10e (ADR 0048); `TestAgentGateways` passed twice in a row on `ownsolid-cpu`, 2026-11-08
  - Scope of the evidence: PostgreSQL only. Two gateway containers on the control host with 500 simulated agents, offline; gateways on hosts of their own, placed by the installer, are REL-101. Gateways share control's serving role (ARC-111) and keep the agents they gained when another returns (ARC-107). Load across gateways is ARC-105.
- [ ] ARC-103 mTLS agent identity, certificate rotation, enrollment throttled per rack (P1) - Catalog: F575
- [ ] ARC-104 One Kubernetes cluster per cell installed and managed by ownsolid, with the agent on every node (P1)
- [ ] ARC-105 Load tested with 1,000 simulated nodes per cell at twice the load, including a Kubernetes cell simulated with KWOK (P1)
- [ ] ARC-106 Published fleet KPIs: dispatch p99, reconnect time, database writes per second at measured scale (P1)
- [ ] ARC-107 Gateway connection lifecycle: maximum connection age with jitter, GOAWAY drain, and rebalancing after a gateway roll so long-lived streams do not pile onto old gateways (P1)
- [ ] ARC-108 Measure the component, inference, model preparation and image registry operations in the agent cost test, so every operation of every call has an exchange (P1)
- [ ] ARC-109 Cut the declared cost of claims, event batches and upload chunks, which repeat the same authority checks: one check per call, one per batch (P1)
- [ ] ARC-110 A listener per entrance: the admin console, the portal, the model gateway and the agent endpoint can each bind their own address and port, so a site can firewall and scale them apart; four hostnames on one TLS port stays the default ([ADR 0027](../../decisions/0027-entrance-addresses.md)) (P1)
- [ ] ARC-111 Agent gateways connect with a database role of their own, with its own connection limit and only the grants the agent path needs, instead of sharing control's serving role ([ADR 0048](../../decisions/0048-agent-gateways-beside-control.md)) (P1)

## P2 Fleet operations

- [ ] ARC-201 One PostgreSQL database per cell plus a thin global database (P2)
- [ ] ARC-202 Multiple cells per site with cell placement and capacity (P2)
- [ ] ARC-203 Load tested with 10,000 simulated nodes across at least two cells (P2)
- [ ] ARC-204 Failure drills: gateway roll, database failover, mass GPU error storm triggering the remediation breaker (P2)
- [ ] ARC-205 Unified middleware contract for state stores and messaging behind adapters (P2) - Catalog: F350, F351, F352, F353, F354, F355
- [ ] ARC-206 Cell router: agents and API clients reach their cell from an in-memory mapping that keeps working when the global layer is down (P2)
- [ ] ARC-207 Move a node between cells without stopping its running work (P2)

## P3 Multi-site

- [ ] ARC-301 Global layer for routing, quota, identity, catalog and aggregates (P3)
- [ ] ARC-302 Site autonomy: a WAN cut leaves every site running and recoverable (P3)
- [ ] ARC-303 Ring releases across cells with version-skew policy (P3)
- [ ] ARC-304 Load tested with 20,000 simulated nodes across simulated sites (P3)

Catalog rows absorbed: F350 to F358, F396 to F411 (contract parts), F622, F631.
