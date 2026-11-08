# Platform reliability

Module reviewed: 2026-11-09.

ownsolid keeping itself running: high availability, backup and recovery, disaster
recovery and support tooling.

- Owns: `services/control/internal/operations`, `deploy/`.
- Depends on: architecture-and-scale, delivery-and-upgrades.

## P0 Foundation

- [x] REL-001 Control-plane restart and crash recovery without losing tasks (P0) - Evidence: commit f966d43 (ADR 0046); `TestControlCrash` passed twice in a row with 2,000 simulated agents on `ownsolid-cpu`
  - Scope of the evidence: one control process on one host, PostgreSQL only, simulated read-only nodes, model store in memory. Mail and workflow recovery tests ran; dataset, knowledge and task-delivery recovery tests exist but did not run. Training and preparation were not in the drill: ARC-013 covers them against a control outage.
- [x] REL-002 Database-aware backup and restore of the control plane (P0) - Catalog: F204 - Evidence: commit 5a62c37 (ADR 0014)

## P1 Enterprise release

- [ ] REL-101 High availability: multiple control-plane replicas, PostgreSQL primary and standby, gateway replicas (P1) - Catalog: F224, F529
  - The installer also deploys the NATS JetStream cluster that gateways share liveness through and writes the `liveness_replica` section ([ADR 0022](../../decisions/0022-liveness-outside-the-database.md)).
- [ ] REL-102 Published reliability targets for the reference topology (availability, RPO, RTO) (P1) - Catalog: F528
- [ ] REL-103 Coordinated backup of database, objects, models and keys with a consistency point (P1) - Catalog: F355, F507
- [ ] REL-104 Restore into an empty air-gapped environment, tested every release (P1) - Catalog: F507
- [ ] REL-105 Diagnostic bundle: redacted logs, configuration and state for support (P1) - Catalog: F647
- [ ] REL-106 Long-running tasks idempotent, retryable and resumable after any component restart (P1) - Catalog: F530
- [ ] REL-107 Self-monitoring of control-plane components with alerts (P1)
- [ ] REL-108 Scheduled backups with retention, run by ownsolid (P1)
- [ ] REL-109 Point-in-time recovery from a base backup and archived WAL, including incremental base backups (P1)
- [ ] REL-110 Encrypted backups with the key held outside the archive (P1)
  - The explicit single-node installer path passed the disconnected Linux amd64 drill. The repository-wide test gate is open. See the [status evidence](../status.md) and [ADR 0056](../../decisions/0056-encrypt-logical-backups-with-a-public-recipient.md).
- [ ] REL-111 Backup history, status and failure alerts in the console (P1)
- [ ] REL-112 `apply` after a `restore` over a running installation: the administrator intent is bound to the database's OIDs, which the restored database does not have, so every later `apply` stops with `BOOTSTRAP_INTENT_MISMATCH` (found on `ownsolid-cpu`, 2026-11-07) (P1)
- [ ] REL-113 An installation restored from a backup upgrades through every later migration: check constraints stay in a form pg_dump and pg_restore preserve (`IN` on varchar does not), and a test applies each new migration to a restored schema (P1)

- [ ] REL-114 Resume or roll back an interrupted database restore by identifying its original and incoming databases across the swap, before bringing the writer online (P1)

## P2 Fleet operations

- [ ] REL-201 Disaster recovery to a standby site (P2) - Catalog: F407
- [ ] REL-202 Database per cell failover drills (P2) - Catalog: F639
- [ ] REL-203 Zero-downtime upgrades of the control plane (P2)
- [ ] REL-204 Recovery points across stores with execution-right invalidation (P2) - Catalog: F639

## P3 Multi-site

- [ ] REL-301 Active-active global layer (P3)
- [ ] REL-302 Site loss drills (P3)

Catalog rows absorbed: F204, F224, F232, F355, F407, F507, F528 to F534, F639.
