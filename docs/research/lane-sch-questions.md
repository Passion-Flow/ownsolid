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
- SCH-103 ranks eligible workspace queues for one tenant using the existing
  policy and active reservations. The dispatch owner still supplies authorized
  membership, priority and durable enqueue order, opens a fresh snapshot after
  dispatch and rechecks admission in the write transaction. Training
  currently selects one due job by `next_dispatch_at,created_at,id`; its
  display list supplies neither the eligible batch nor resolved priority
  classes. The existing persisted reservation read now refuses foreign
  cell coordinates within the selected workspaces. See the
  [durable queue boundary](scheduling-queue-policy.md#durable-queue-and-persisted-reservation-boundary).
- SCH-104 reserves whole demand in PostgreSQL and requires a verified
  stop or non-delivery for every exact replica slot before release.
  The [gang handoff](scheduling-admission.md#atomic-gang-reservation-and-release)
  requires the workload owner to preserve each attempt-to-slot binding, fence
  dispatch, verify positive physical proof, and reconcile group readiness
  with an installed Kueue or Volcano controller.
- SCH-110 has a read-only submission estimate. It accepts only complete,
  current samples for the database's local site and cell. The Workspace
  authority and snapshot have been exercised together using fixture samples.
  The workload owner must compose the authenticated endpoint and the cell
  owner must supply verified total and occupied capacity from the same
  observation. The managed-cell TLS adapter supplies only exact-object
  state, version and blocker. ARC must bind the registered cluster identity,
  and the cell inventory/controller owner must supply measurement time,
  expiry and complete total/occupied resource vectors from one observation.
  See the [production source boundary](scheduling-admission.md#production-capacity-source-boundary).
- SCH-108 projects actionable reasons from queue, preflight, reservation and
  reclaim decisions. The cell consumer reads the current Workload over TLS
  and checks its exact UID before projecting a Kueue blocker. The workload
  owner must persist the attempt-to-controller binding. The authenticated
  API and browser owners must resolve it under current authority. Live
  validation requires signed offline
  controller assets and a ready isolated cell. See the
  [installed workload handoff](scheduling-wait-reasons.md#installed-workload-handoff).
- SCH-105 requires a durable preemption attempt, explicit checkpoint support
  from the workload owner, a bounded retry policy and cell confirmation before
  resource release. Kueue [preemption](https://kueue.sigs.k8s.io/docs/concepts/preemption/)
  and Slurm [preemption](https://slurm.schedmd.com/preempt.html) expose different
  stop and requeue semantics. The owner must supply a durable, scoped
  preemption lineage to distinguish requeues from ordinary attempt retries,
  plus a checkpoint and exact positive stop result before capacity is freed.
  The [preemption research](scheduling-preemption.md) records current upstream
  retry/grace semantics, the rejected shadow runtime and exact acceptance cases.
- SCH-106 requires trustworthy device and network topology observations. The
  current agent facts contract lacks the full PCIe, NUMA, NVLink and fabric
  inventory. [NVIDIA DCGM topology](https://docs.nvidia.com/datacenter/dcgm/latest/learn/core-services/topology-and-links.html)
  and the [Kubernetes Topology Manager](https://kubernetes.io/docs/tasks/administer-cluster/topology-manager/)
  distinguish local hardware affinity from scheduler labels. Avoid inventing
  topology from node names or assuming every link is healthy.
  The [topology research](scheduling-topology.md) separates physical graphs,
  derived scheduler domain trees and current link observations, and records the
  exact missing producer contract and hard/preferred acceptance conditions.
- SCH-107 has observation readers. The signed offline install assets and
  RKE2 deployment, workload creation, RBAC provisioning and runtime reconciliation
  remain part of the item. The [offline installation boundary](scheduling-cell-adapters.md#offline-controller-installation-and-upgrade-boundary)
  records release-artifact rendering, node image delivery, scoped webhooks and
  non-destructive upgrade acceptance; source-chart defaults are not offline payloads.
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
Wall-clock differences must not be used to derive duration. The gateway
intake verifies the canonical receipt against the exact scoped reported
row in the owner's transaction. The distribution owner must call it after
canonical persistence or replay verification, before acknowledging Report.
The isolated serving-role test verifies the current business-table grant
and the narrower ledger privileges. The database owner must number and
admit the candidate, reconcile install and upgrade grants with runtime
readiness and accept the fresh and restored catalogs. The shared Control
role remains trusted for source state. Requests without a report and
historical reconciliation remain coverage boundaries. See
[serving-role grants and rollback prerequisites](usage-ledger.md#serving-principal-and-ledger-grants).

## Tenancy and commerce

TEN-101 through TEN-105 need the workspace and identity owners to define
organization scope and enforce it across stores. The existing installed-tenant
workspace API remains the authority. A private parallel hierarchy would let
membership and resource access diverge.

TEN-107 rates the isolated ledger by occurrence against installation-owned
versions. The metering read now reuses current Workspace and session
authorization. The integration owner must bind the public dispatcher to that
read, retain historical rates and reconcile production ledger and producer
coverage before exposing showback. See [showback](metering-showback.md).
TEN-108 previews alert-only and deny-at-known-limit policies on the
authorized showback snapshot. Production activation requires an effective
workspace policy, durable notification identity, complete producer coverage
and an owner transaction for current and pending usage. See
[budget status](metering-budget-status.md#authenticated-admission-preview).

TEN-109 requires the commerce owner's verifiable grant authority, the
database owner's durable credit records and the workload owner's exact
charge identity and pending usage policy. The current catalog supplies none
of those credit write boundaries. See [prepaid balance authority](metering-prepaid-credits.md)
for the exact inputs and replay and concurrency acceptance conditions.
[FinOps allocation](https://www.finops.org/framework/capabilities/allocation/)
and [OpenMeter entitlement grants](https://openmeter.io/docs/billing/entitlements/grant)
provide the reporting and balance references. Offline top-up is a tenant
accounting action. The whole-platform license verifier has separate authority.

## Public workspace quota status

The owned reader and HTTP component reuse the current Workspace authority
and the selected repository in one transaction. They return actual logical
allocation and held usage without a job-readiness, physical-capacity or
topology claim. IAM owns route composition, DEV the final OpenAPI/SDK/Help,
and Web the UI. The [scope contract](scheduling-wait-reasons.md#authorized-workspace-quota-status)
defines the payload, failure set and remaining production DDL/role boundary.
This component does not depend on fair-queue or selected-executor candidates.
