# Network and storage

Module reviewed: 2026-11-09. See [the module review](../../research/network-storage-module-review.md).

Fabric and storage operations for GPU clusters, and the storage contracts ownsolid uses.

- Owns: `services/control/internal/modules/artifactstores`, `imageregistry`, agent collectors.
- Depends on: fleet-and-infrastructure.

## P1 Enterprise release

- [ ] NET-101 Object storage contract over S3 for models, datasets, checkpoints and backups (P1) - Catalog: F199, F210
  - [Object backup fence](../../research/object-backup-fence.md) covers the artifactstores instance. A [verified staged archive](../../research/object-backup-fence.md#verified-staged-archive) copies exact S3 bytes under that fence. Provider exclusion, publication and coordinated recovery remain open.
- [ ] NET-102 OCI registry contract and the in-site registry (P1) - Catalog: F200, F212
  - [Site release pin admission](../../research/site-registry-pin-admission.md) verifies read-only import with the current release profile, recovery and Workspace scope. Control startup wiring and installed-release acceptance remain open.
- [ ] NET-103 Network interface, link and RDMA status per node (P1)
- [ ] NET-104 Shared file system mounts for jobs (NFS first) (P1) - Catalog: F329
- [ ] NET-105 Storage credential mapping per tenant (P1)
- [ ] NET-106 Default-deny egress for workloads on Kubernetes cells: network policy for every workload namespace, the same denial DEL-003 gives Docker nodes, proven with the zero-egress harness (P1)

## P2 Fleet operations

- [ ] NET-201 InfiniBand fabric monitoring through UFM, link flap detection (P2)
- [ ] NET-202 RoCE monitoring and congestion signals (P2)
- [ ] NET-203 Rail and leaf-spine topology discovery feeding the scheduler (P2)
- [ ] NET-204 Parallel file system integration: Lustre, GPFS, WEKA, 3FS (P2)
- [ ] NET-205 Storage metadata load checks before large job launches (P2)
- [ ] NET-206 Peer-to-peer image and model distribution (P2) - Catalog: F642
- [ ] NET-207 Model and dataset distribution capacity controls (P2) - Catalog: F642
- [ ] NET-208 Scheduled integrity scans for retained artifacts against their recorded digests, with scoped recovery from a verified copy (P2)

## P4 Breadth

- [ ] NET-401 Object, file and block storage products from the compatibility matrix (P4) - Catalog: F311 to F330

Catalog rows absorbed: F199, F200, F210, F212, F329, F330, F342, F642.
