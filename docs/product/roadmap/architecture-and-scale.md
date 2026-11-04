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
- [ ] ARC-007 Server-paced heartbeat interval, jitter and reconnect delay, changeable at runtime; full-jitter backoff; retry at one layer only (P0)
- [ ] ARC-008 Per-operation limits and priority classes (quarantine above heartbeat above telemetry above bulk) with retry hints (P0)
- [ ] ARC-009 Cost rule: every agent call declares its server cost; calls whose cost grows with fleet size fail review (P0)
- [ ] ARC-010 Liveness kept in gateway memory plus a small replicated store; PostgreSQL stores only state transitions and batched last-seen (P0)
- [ ] ARC-011 Transactional outbox with batched delivery replaces LISTEN/NOTIFY; low-frequency reconciliation (P0)
- [ ] ARC-012 Time-partitioned append tables for events, task history and audit, with partition drop; pooled connections; agents never hold database connections (P0)
- [ ] ARC-013 Static stability: agents keep running work when disconnected and never kill on disconnect (P0)
- [ ] ARC-014 Fake-agent simulator speaking the real protocol, thousands of agents per process, scripted heartbeats, task acks, reconnect and fault storms (P0)
- [ ] ARC-015 Load tested with 2,000 simulated nodes at twice the planned load; full reconnect within 5 minutes; control-plane restart leaves work running (P0)
- [ ] ARC-016 Break-glass admin path independent of agent traffic, plus an admission switch that sheds agent load (P0)

## P1 Enterprise release

- [ ] ARC-101 Versioned in-memory snapshot cache with watches for per-node reads (P1)
- [ ] ARC-102 Gateway tier scales horizontally; agents connect to one or two gateways (P1)
- [ ] ARC-103 mTLS agent identity, certificate rotation, enrollment throttled per rack (P1) - Catalog: F575
- [ ] ARC-104 One Kubernetes cluster per cell installed and managed by ownsolid, with the agent on every node (P1)
- [ ] ARC-105 Load tested with 1,000 simulated nodes per cell at twice the load, including a Kubernetes cell simulated with KWOK (P1)
- [ ] ARC-106 Published fleet KPIs: dispatch p99, reconnect time, database writes per second at measured scale (P1)
- [ ] ARC-107 Gateway connection lifecycle: maximum connection age with jitter, GOAWAY drain, and rebalancing after a gateway roll so long-lived streams do not pile onto old gateways (P1)

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
