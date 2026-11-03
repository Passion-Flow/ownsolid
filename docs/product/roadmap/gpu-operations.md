# GPU operations

Health, faults, sharing and remediation for accelerators. At 10,000 nodes, 20 to 70
node-impacting failures happen every day, so detection and remediation must be automatic.

- Owns: `services/agent` device collectors, `services/control` health and remediation.
- Depends on: fleet-and-infrastructure, observability-and-alerting.

## P1 Enterprise release

- [ ] GPU-101 NVIDIA health through DCGM: utilization, memory, temperature, power, clocks, ECC, NVLink (P1)
- [ ] GPU-102 XID and other driver fault catalog with severity and recommended action (P1)
- [ ] GPU-103 Automatic quarantine of failing GPUs and nodes, with a circuit breaker that stops mass quarantine (P1) - Catalog: F636
- [ ] GPU-104 Bad-node history and chronic-node ("lemon") detection (P1)
- [ ] GPU-105 Device allocation through Kubernetes DRA and the NVIDIA device plugin (P1) - Catalog: F624
- [ ] GPU-106 MIG partitioning and time-slicing with isolation profiles per pool (P1) - Catalog: F635
- [ ] GPU-107 Exclusive GPU leases with fencing so two owners never share a device (P1) - Catalog: F630
- [ ] GPU-108 Per-GPU utilization and effective-use reports (P1) - Catalog: F633
- [ ] GPU-109 Verified on real NVIDIA hardware (record model and count) (P1)

## P2 Fleet operations

- [ ] GPU-201 Closed-loop remediation runbooks: reset, drain, reboot, reimage, RMA, each with breakers (P2)
- [ ] GPU-202 Fail-slow and straggler detection for GPUs and nodes in training jobs (P2)
- [ ] GPU-203 Active health checks between jobs (short burn tests) (P2)
- [ ] GPU-204 Ascend NPU support: npu-smi health, Ascend device plugin, fault catalog (P2) - Catalog: F185, F520
- [ ] GPU-205 Ascend allocation and partitioning profiles (P2) - Catalog: F635
- [ ] GPU-206 Capacity degradation tracking and spare planning (P2) - Catalog: F636
- [ ] GPU-207 Verified on real Ascend hardware (record model and count) (P2)
- [ ] GPU-208 Heterogeneous pools with capability tiers (P2) - Catalog: F634

## P4 Breadth

- [ ] GPU-401 Other accelerators from the compatibility matrix: AMD ROCm, Hygon DCU, Cambricon, Kunlunxin, Moore Threads, MetaX, Intel (P4) - Catalog: F186, F187, F191 to F194, F521
- [ ] GPU-402 vGPU and virtual-card isolation profiles (P4) - Catalog: F635

Catalog rows absorbed: F183 to F196, F520 to F523, F630, F633 to F636.
