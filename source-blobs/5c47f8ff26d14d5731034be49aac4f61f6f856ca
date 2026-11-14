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
  - The shipped local Compose profile passed the disconnected database, model and key drill. External object fencing and restore remain open. See the [acceptance and provider handoff](../../research/coordinated-local-backup.md#acceptance-and-provider-handoff).
- [ ] REL-104 Restore into an empty air-gapped environment, tested every release (P1) - Catalog: F507
  - The temporary-signed Linux amd64 Compose drill passed for legacy and encrypted backups, rollback, both entrances and restored model files. The every-release production gate remains open. See the [acceptance evidence](../../research/signed-release-recovery.md#linux-amd64-acceptance).
- [ ] REL-105 Diagnostic bundle: redacted logs, configuration and state for support (P1) - Catalog: F647
  - The [disconnected managed API check](../../research/support-diagnostics.md#disconnected-managed-api-check) verified the native installer against both scoped HTTPS response contracts and preserved single-node diagnostics. A live managed cluster and installed chart remain to be qualified.
  - The local archive reports the protected pending restore phase with Docker unavailable. The [disconnected Linux check](../../research/support-diagnostics.md#disconnected-diagnostic-check) verified that identifiers stay out of the archive.
- [ ] REL-106 Long-running tasks idempotent, retryable and resumable after any component restart (P1) - Catalog: F530
- [ ] REL-107 Self-monitoring of control-plane components with alerts (P1)
- [ ] REL-108 Scheduled backups with retention, run by ownsolid (P1)
- [ ] REL-109 Point-in-time recovery from a base backup and archived WAL, including incremental base backups (P1)
- [x] REL-110 Encrypted backups with the key held outside the archive (P1) - Evidence: fe49ce45 and 3b47c1de. Disconnected Linux amd64 verified explicit single-node encrypted backup, direct restore and restore into a clean site, with the age identity held separately.
  - The [verification evidence](../../research/encrypted-backups.md#release-qualification) covers the repository gates and the encrypted archive refusal checks.
- [ ] REL-111 Backup history, status and failure alerts in the console (P1)
  - The scheduled installer now persists a redacted, installation-bound history and can report it offline. Disconnected Linux process tests covered failure, interruption, retry and archive expiry. The authenticated API, Console and alerts remain open. See [status evidence](../status.md) and [ADR 0063](../../decisions/0063-keep-local-scheduled-backup-history.md).
- [ ] REL-112 `apply` after a `restore` over a running installation: the administrator intent is bound to the database's OIDs, which the restored database does not have, so every later `apply` stops with `BOOTSTRAP_INTENT_MISMATCH` (found on `ownsolid-cpu`, 2026-11-07) (P1)
  - The [disconnected reapply drill](../../research/restore-apply.md#disconnected-reapply-acceptance) rejected a stale database-level adoption marker and verified two Apply retries against the adopted OID with both entrances ready.
- [ ] REL-113 An installation restored from a backup upgrades through every later migration: check constraints stay in a form pg_dump and pg_restore preserve (`IN` on varchar does not), and a test applies each new migration to a restored schema (P1)
  - The [disconnected acceptance fixture](../../research/restored-schema-upgrade.md#linux-amd64-acceptance) reproduces the refusal using the historical control binaries and the installer's logical backup and load functions. Production catalog admission and the restored-upgrade release gate remain open.

- [ ] REL-114 Resume or roll back an interrupted database restore by identifying its original and incoming databases across the swap, before bringing the writer online (P1)
  - The [disconnected load drill](../../research/interrupted-database-restore.md#disconnected-load-recovery) recovered from SIGKILL after real pg_restore. Both entrances remained ready on the original database while the unverified scratch OID was retained for inspection.
  - The [disconnected allocation drill](../../research/interrupted-database-restore.md#disconnected-allocation-recovery) covered SIGKILL before and after real CREATE, an occupied slot with no bound OID, and concurrent recovery. The live database and both entrances stayed available.

## P2 Fleet operations

- [ ] REL-201 Disaster recovery to a standby site (P2) - Catalog: F407
- [ ] REL-202 Database per cell failover drills (P2) - Catalog: F639
- [ ] REL-203 Zero-downtime upgrades of the control plane (P2)
- [ ] REL-204 Recovery points across stores with execution-right invalidation (P2) - Catalog: F639

## P3 Multi-site

- [ ] REL-301 Active-active global layer (P3)
- [ ] REL-302 Site loss drills (P3)

Catalog rows absorbed: F204, F224, F232, F355, F407, F507, F528 to F534, F639.
