# Platform reliability

ownsolid keeping itself running: high availability, backup and recovery, disaster
recovery and support tooling.

- Owns: `services/control/internal/operations`, `deploy/`.
- Depends on: architecture-and-scale, delivery-and-upgrades.

## P0 Foundation

- [ ] REL-001 Control-plane restart and crash recovery without losing tasks (P0)
- [ ] REL-002 Database-aware backup and restore of the control plane (P0) - Catalog: F204

## P1 Enterprise release

- [ ] REL-101 High availability: multiple control-plane replicas, PostgreSQL primary and standby, gateway replicas (P1) - Catalog: F224, F529
- [ ] REL-102 Published reliability targets for the reference topology (availability, RPO, RTO) (P1) - Catalog: F528
- [ ] REL-103 Coordinated backup of database, objects, models and keys with a consistency point (P1) - Catalog: F355, F507
- [ ] REL-104 Restore into an empty air-gapped environment, tested every release (P1) - Catalog: F507
- [ ] REL-105 Diagnostic bundle: redacted logs, configuration and state for support (P1) - Catalog: F647
- [ ] REL-106 Long-running tasks idempotent, retryable and resumable after any component restart (P1) - Catalog: F530
- [ ] REL-107 Self-monitoring of control-plane components with alerts (P1)

## P2 Fleet operations

- [ ] REL-201 Disaster recovery to a standby site (P2) - Catalog: F407
- [ ] REL-202 Database per cell failover drills (P2) - Catalog: F639
- [ ] REL-203 Zero-downtime upgrades of the control plane (P2)
- [ ] REL-204 Recovery points across stores with execution-right invalidation (P2) - Catalog: F639

## P3 Multi-site

- [ ] REL-301 Active-active global layer (P3)
- [ ] REL-302 Site loss drills (P3)

Catalog rows absorbed: F204, F224, F232, F355, F407, F507, F528 to F534, F639.
