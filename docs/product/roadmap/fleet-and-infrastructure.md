# Fleet and infrastructure

Everything about the physical fleet: inventory, enrollment, provisioning, lifecycle,
firmware, facility and spares.

- Owns: `services/control/internal/modules/nodes`, `components`, `services/agent`.
- Depends on: architecture-and-scale (agent protocol, cells).

Reviewed 2026-11-06 against Kubernetes, Nomad and GitHub runner documentation ([research](../../research/nodes-page.md)). The review added FLT-114 and FLT-115.

## P1 Enterprise release

- [ ] FLT-101 Inventory model: site, cell, room, row, rack, node, GPU, NIC, disk, with serials and positions (P1) - Catalog: F183
- [ ] FLT-102 One-command node enrollment with an install token; bulk enrollment by CSV (P1)
- [ ] FLT-103 Hardware and software discovery: CPU, memory, GPUs, NICs, disks, OS, kernel, driver, CUDA (P1) - Catalog: F183
- [ ] FLT-104 Node lifecycle states: enrolling, ready, cordoned, draining, maintenance, quarantined, retired (P1)
- [ ] FLT-105 Cordon and drain with workload-aware eviction and progress (P1) - Catalog: F568
- [ ] FLT-106 Maintenance windows with impact preview of affected tenants and jobs (P1) - Catalog: F568
- [ ] FLT-107 Driver, CUDA and container-toolkit baselines per node pool, with staged rollout (P1)
- [ ] FLT-108 Node pools and labels by hardware, purpose and tenant (P1) - Catalog: F634
- [ ] FLT-109 Managed host components (node exporter and others) with install, configure, upgrade, rollback (P1) - Catalog: F004, F005
- [ ] FLT-110 Agent self-upgrade in rings with automatic rollback (P1)
- [ ] FLT-111 Kubernetes cell lifecycle: create, add nodes, upgrade, certificate rotation (P1) - Catalog: F400, F624
- [ ] FLT-112 Fleet batch actions with staged rollout and stop on failure (P1)
- [x] FLT-113 Console page for agent pacing: current values, change with a reason, the online-window refusal explained (P1) - Evidence: 5dd2247; real Chromium on an offline install on `ownsolid-cpu` changed the pacing and a real agent adopted it and the earlier value, three runs in a row, 2026-11-06
- [x] FLT-114 Nodes page: list with server-side filters and counts, details drawer, rename, disable and remove, add a node with a one-time join token and see it join (P1) - Evidence: 602acdf, b66dfce; real Chromium on an offline install on `ownsolid-cpu` added a node by running the page's commands on a disposable host, saw the real agent join and ran a task on it, four runs in a row, 2026-11-06
- [ ] FLT-115 Node events: joined, connectivity changes, status changes and who made them, per node in the details drawer (P1)

## P2 Fleet operations

- [ ] FLT-201 Bare-metal provisioning: PXE and iPXE boot, OS image install, per provisioning domain of up to 1,000 nodes (P2) - Catalog: F637
- [ ] FLT-202 BMC management over Redfish and IPMI: power, boot order, sensors, serial console (P2) - Catalog: F637
- [ ] FLT-203 Firmware inventory and staged firmware rings for BIOS, BMC, GPU and NIC (P2)
- [ ] FLT-204 Burn-in and acceptance tests for new nodes before they take work (P2)
- [ ] FLT-205 Spare pool, RMA and repair ticket tracking with vendor case fields (P2)
- [ ] FLT-206 Facility connectors: power, cooling and environment readings over Modbus, SNMP and MQTT (P2)
- [ ] FLT-207 Power-aware admission and rack power budgets (P2)
- [ ] FLT-208 Data-center asset management and work tickets for field engineers (P2)
- [ ] FLT-209 Existing Slurm and Docker Swarm clusters registered and observed (P2) - Catalog: F626
- [ ] FLT-210 Load tested with 10,000 simulated nodes enrolling, reporting and draining (P2)
- [ ] FLT-211 Cloud GPU node pools for connected deployments: scale pools from queue demand through cloud APIs, use spot or preemptible capacity with checkpoint and requeue on interruption, record cost per job; the air-gapped default stays unchanged (P2)

## P3 Multi-site

- [ ] FLT-301 Site management with site autonomy and global inventory (P3) - Catalog: F395
- [ ] FLT-302 Cross-site workload placement with data-boundary rules (P3) - Catalog: F631

Catalog rows absorbed: F004, F005, F183, F190, F395, F400, F568, F622 to F626, F630, F634, F637.
Cloud and private-cloud providers are in the compatibility matrix.
