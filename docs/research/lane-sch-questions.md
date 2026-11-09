# Scheduling and metering integration boundaries

## Admission, PostgreSQL and the product entrances

SCH-101 through SCH-110 need authoritative writes. Training, inference,
workspaces and the Console have separate owners. The least invasive path is
for those owners to call scheduling after rechecking authority in their own
transaction. The scheduler supplies policy and adapter observations. It must
not take over their runtime lifecycle, infer membership directly from another
module's tables or create a second authorization path.

PostgreSQL schema additions also change the production catalog fingerprint in
`services/control/internal/platform/database/catalog.go`. The shared-file
allowlist names migrations, schema version and table count, plus tests that
enumerate schema objects. It does not name the production fingerprint. Adding a
migration without updating it causes startup and restore to reject the schema.
Keep the production migration lineage unchanged while testing proposed
transaction behavior on a separate schema. The integration requires the
database owner to land or authorize the exact fresh and restored fingerprints.

## Remaining scheduling items

- SCH-101 has a PostgreSQL reservation and release seam. The
  [training and database handoff](scheduling-training-reservations.md) names the
  existing owner transactions and records fresh and restored catalog evidence.
- SCH-104, SCH-108, SCH-110 have snapshot checks. The repository can reserve
  whole demand, and the runtime reconciliation and product entrance are still needed.
- SCH-105 requires a durable preemption attempt, explicit checkpoint support
  from the workload owner, a bounded retry policy and cell confirmation before
  resource release. Kueue [preemption](https://kueue.sigs.k8s.io/docs/concepts/preemption/)
  and Slurm [preemption](https://slurm.schedmd.com/preempt.html) expose different
  stop and requeue semantics. Do not treat a Kubernetes stop request or an
  elapsed grace period as proof that GPUs are free.
- SCH-106 requires trustworthy device and network topology observations. The
  current agent facts contract lacks the full PCIe, NUMA, NVLink and fabric
  inventory. [NVIDIA DCGM topology](https://docs.nvidia.com/datacenter/dcgm/latest/learn/core-services/topology-and-links.html)
  and the [Kubernetes Topology Manager](https://kubernetes.io/docs/tasks/administer-cluster/topology-manager/)
  distinguish local hardware affinity from scheduler labels. Avoid inventing
  topology from node names or assuming every link is healthy.
- SCH-107 has observation readers. The signed offline install assets and
  RKE2 deployment, workload creation, RBAC provisioning and runtime reconciliation
  remain part of the item.
- SCH-109 has candidate PostgreSQL loan attribution and ordered reclaim
  within enabled departments. [Borrowing and reclaim](scheduling-quota-borrowing.md)
  records the owner transaction, follow-on schema and positive release
  boundary. Production schema and runtime stop delivery remain open.
- SCH-111 requires a cell running the real admission pipeline. A package
  benchmark on synthetic queue entries does not establish that admission
  latency or fleet scale.

## Metering producers

TEN-106 ingestion is downstream of trusted scheduling, gateway and storage
observations. A durable source must supply a stable identity and measured delta
and atomically record the delta or retain an outbox entry until acknowledged.
Wall-clock differences must not be used to derive duration. Installing a ledger
without these producer hooks would silently omit usage.

## Tenancy and commerce

TEN-101 through TEN-105 need the workspace and identity owners to define
organization scope and enforce it across stores. The existing installed-tenant
workspace API remains the authority. A private parallel hierarchy would let
membership and resource access diverge.

TEN-107 through TEN-109 need the real usage producers, effective-dated rate
policy and an authorized transaction to enforce budget or credit admission.
[FinOps allocation](https://www.finops.org/framework/capabilities/allocation/)
and [OpenMeter entitlement grants](https://openmeter.io/docs/billing/entitlements/grant)
provide the reporting and balance references. Offline top-up is a tenant
accounting action. The whole-platform license verifier has separate authority.
