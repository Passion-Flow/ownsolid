# Scheduling and quota

Who gets which GPUs, when. Two levels: ownsolid owns admission, quota and topology;
Kueue and Volcano place work inside each cell behind an adapter boundary.

- Owns: `services/control/internal/modules` scheduling packages (new), cell adapters.
- Depends on: fleet-and-infrastructure, gpu-operations, tenancy-metering-commerce.

## P1 Enterprise release

- [ ] SCH-101 Unified job model for training, inference, notebooks and batch work (P1) - Catalog: F622
- [ ] SCH-102 Hierarchical quota tree: tenant, department, project, with guaranteed and burst quota (P1) - Catalog: F473, F629
- [ ] SCH-103 Queues with priority classes and fair share (P1) - Catalog: F629
- [ ] SCH-104 Gang scheduling: all-or-nothing admission for distributed jobs (P1) - Catalog: F627
- [ ] SCH-105 Preemption with grace periods, checkpoint hooks and requeue-loop breaker (P1) - Catalog: F589
- [ ] SCH-106 Topology model: NUMA, PCIe, NVLink, rail, leaf and spine; topology-aware placement (P1) - Catalog: F627
- [ ] SCH-107 Kueue and Volcano adapters installed per cell (P1)
- [ ] SCH-108 Placement explanation: why a job is waiting and what would let it run (P1) - Catalog: F632
- [ ] SCH-109 Quota borrowing and reclaim between teams (P1) - Catalog: F629
- [ ] SCH-110 Resource estimation and preflight before submission (P1) - Catalog: F107
- [ ] SCH-111 Load tested: 1,000-node cell with 10,000 queued jobs, admission latency recorded (P1)

## P2 Fleet operations

- [ ] SCH-201 Tidal scheduling: lend inference capacity to training at night and reclaim it (P2) - Catalog: F628
- [ ] SCH-202 Scheduling simulation and policy dry run with rollback (P2) - Catalog: F632
- [ ] SCH-203 Data-locality and cache-aware placement (P2) - Catalog: F627
- [ ] SCH-204 Spot-style preemptible capacity with pricing hooks (P2)
- [ ] SCH-205 Slurm cluster adoption: submit and observe through ownsolid (P2) - Catalog: F626
- [ ] SCH-206 Ascend-aware scheduling through Volcano (P2)
- [ ] SCH-207 Load tested: 10,000 nodes across cells with fair-share convergence measured (P2)

## P3 Multi-site

- [ ] SCH-301 Cross-cell and cross-site admission with global quota (P3) - Catalog: F631
- [ ] SCH-302 Site failover for queued work (P3)

## P4 Breadth

- [ ] SCH-401 Slurm on Kubernetes (P4)

Catalog rows absorbed: F107, F473, F589, F622, F627 to F633.
