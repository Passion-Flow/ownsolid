# Whole-job quota admission and preflight

SCH-104, SCH-108 and SCH-110 share a decision boundary.
[Kueue all-or-nothing scheduling](https://kueue.sigs.k8s.io/docs/concepts/all_or_nothing/)
reserves all pod-set demand and can additionally confirm cell readiness.
[Volcano PodGroup](https://volcano.sh/docs/concepts/podgroup/) provides a minimum
member and resource threshold at placement. [Kueue troubleshooting](https://kueue.sigs.k8s.io/docs/tasks/troubleshooting/troubleshooting_jobs/)
shows quota and placement as separate conditions.

Preflight checks the normalized demand against all ancestor ceilings and a
cell's aggregate capacity, then considers current reservations. Explain a hard
limit separately from an exhausted allocation, with resource, requested units
and available units. A snapshot may not prove the physical gang fits across
nodes. Real placement must rely on the cell's all-or-nothing adapter. Any
reservation writer must reevaluate against current locks and store all resources
or none in one transaction. The scheduler never reports a preflight as an
admission and does not release quota merely because a cell call timed out.

Partial reservation can deadlock distributed jobs. Checking only the workspace
misses ancestor pressure. A second heuristic placement engine would disagree with
Kueue and Volcano over actual node availability. Use adapter conditions for
physical placement and scheduler policy reasons for quota.

Only the Go standard library is needed in this slice
([BSD-3-Clause](https://go.dev/LICENSE)). Apache-2.0
[Kueue](https://github.com/kubernetes-sigs/kueue/blob/main/LICENSE) and
[Volcano](https://github.com/volcano-sh/volcano/blob/master/LICENSE) are upstream
references here. No runtime download, model or dataset is required.

Exercise multi-group demand, a missing dimension, one insufficient group in a
gang, quota changes, a full cell, overcommitted live counters, scoped diagnostics
and unchanged inputs on failure. Full acceptance also needs transactional
reservation, contention tests, adapter placement and the preflight and waiting
states in the offline product entrance.

## Live submission estimate

SCH-110 reads current ownsolid inputs before the workload owner submits a
job. [Slurm's test-only submission](https://slurm.schedmd.com/sbatch.html#OPT_test-only)
validates a request against the current queue without creating a job.
[Kubernetes dry-run](https://kubernetes.io/docs/reference/using-api/api-concepts/#dry-run)
uses the normal authorization and validation boundary and avoids writes.
[Kueue admission](https://kueue.sigs.k8s.io/docs/concepts/admission/) keeps
quota reservation separate from the checks needed for admission.

Require an already authorized workspace and a read-only PostgreSQL
REPEATABLE READ transaction. [PostgreSQL snapshot isolation](https://www.postgresql.org/docs/18/transaction-iso.html#XACT-REPEATABLE-READ)
keeps policy, current holds and borrowing evidence consistent across reads.
[The transaction access mode](https://www.postgresql.org/docs/18/sql-set-transaction.html)
also prevents mutations to durable tables. The estimate reads the existing
policy revision and workspace usage, normalizes full job demand, uses the
same capacity comparison as the reservation writer and evaluates the supplied
cell snapshot. Other workspace holds already fit their ceilings and the
policy bounds the sum of child ceilings. A workspace fit therefore preserves
ancestor hard limits for this snapshot without reading other tenant usage.
An enabled department also evaluates the same loan decision as a new
attempt, including any pending workspace claim. Return safe reasons, full
job demand and the quota revision for the owner. A subsequent reservation
reads fresh data and has its own transactional admission result.

A committed-state estimate can change between preview and submit. Running
the writer and rolling it back would take admission locks and consume
reservation identities and would couple the preview to write permissions.
Re-reading policy and holds under READ COMMITTED could mix revisions.
Passing an empty or caller-guessed hold vector would overlook real work.
Keep the cell capacity an explicit, trusted owner input. Aggregate capacity
estimates leave replica placement to the verified cell adapter.

The existing BSD-3-Clause [Go](https://go.dev/LICENSE), MIT [pgx](https://github.com/jackc/pgx/blob/master/LICENSE)
and [PostgreSQL License](https://www.postgresql.org/about/licence/) permit
commercial, redistributed and offline use with their notices. Slurm is a
[GPL design reference](https://github.com/SchedMD/slurm/blob/master/COPYING).
This path adds no dependency, model or data set.

Exercise rejection of a write-capable or changing snapshot, stale versus
fresh snapshots across committed quota edits and releases, identical
inaccessible and missing scope, multi-group demand, current borrowing and
pending claims, cell shortfalls and a later exact-fit reservation. Verify
that preview never creates or consumes a hold or claim. Full acceptance
needs the owner's authorized submission endpoint, verified cell observation
and an offline user flow.

### Authorized scope and cell capacity handoff

Training's [submission authority](../../services/control/internal/modules/training/creator_repository.go)
resolves the installed tenant and Workspace from current actor/session state.
The owner checks the Workspace's active status and submit permission. The
Workspace's [fenced read](../../services/control/internal/modules/workspaces/scope.go)
reuses the same current actor authority and takes a shared Workspace lock.
Keep that authority outside the scheduling package. PostgreSQL disallows the
owner's row locks inside a read-only transaction. An owner can retain the
existing authority fences on one connection while it opens the bounded
read-only quota snapshot on another connection in the same repository.
Acquire authority first and release both transactions before exposing a result.
The workload owner's endpoint and transaction composition remain owner work.

The [training target](../../services/control/internal/modules/training/history.go)
reads one authorized node's GPU observations and probe status. The
[private-cluster discovery](../../services/control/internal/modules/privateclusters/discovery.go)
returns observed Node CPU and memory allocatable values. Kubernetes defines
[Node Allocatable](https://kubernetes.io/docs/tasks/administer-cluster/reserve-compute-resources/#node-allocatable)
as the capacity made available to Pods. That declaration omits current
reservations. Kueue's [ClusterQueue](https://kueue.sigs.k8s.io/v0.20/docs/concepts/cluster_queue/)
tracks quota for specific flavors and queues. Neither its counters nor the
[SCH-108 Workload observation](scheduling-wait-reasons.md#installed-workload-handoff)
measure total and occupied capacity for the selected cell.

The scheduler consumer takes one trusted sample containing the installation's
site and cell IDs, sample time, an exclusive expiration from the owner's
freshness policy, and total and reserved quantities in scheduling units.
It checks those coordinates against the local database cell and compares
the times with the PostgreSQL clock when evaluating the quota snapshot.
Every requested dimension must appear in both vectors. Explicit measured
zero is valid; an omitted quantity is unavailable. Stale, future, foreign,
malformed or unavailable input yields one retryable capacity error and
no estimate. A resource shortfall only exposes the requested units and
a fixed reason. Workspace availability comes only from its own quota
ceiling and held usage. The database remains read-only throughout the
scheduling calculation.

The [cell cluster owner](../../services/control/internal/modules/cellclusters/README.md)
provides installed cluster/member identity, readiness and CA evidence.
The selected cell owner must collect complete, current total and occupied
capacity for the same cell and observation cut, including non-ownsolid
occupancy where it affects scheduling. Reuse the SCH-108 transport's
configured private origin, verified CA, scoped credential and bound cell
identity when the source is an installed cluster. A Workload UID proves
which object returned a blocker. It supplies no cell capacity. A timeout,
missing coverage or unreachable source must keep the sample unavailable.
Probe time cannot refresh an older underlying measurement. Keep admission
and topology placement with their existing owners. This contract uses
the Go, pgx and PostgreSQL licenses already recorded and adds no dependency,
model or dataset.

Verify a real actor's Workspace resolution before reading the protected
quota snapshot on disconnected PostgreSQL. Use fixture samples to check
whole multi-group fit, occupied and zero capacity, expired and future
samples, the wrong site or cell and missing dimensions. Check that no
preview creates a hold or claim. Installed-cell acceptance requires the
owner's complete source and one authorized offline submission flow.

### Production capacity source boundary

The managed-cell adapter in `959f818e` authenticates one exact Kueue
Workload or Volcano Job. Its current `Observation` exports
`ResourceVersion`, `State` and `Blocker`. It does not export the
registered site/cell or cluster identity, a measurement time, an expiry,
or complete total and occupied resource vectors. The object generation
only fences its own conditions. An object resource version does not
establish a common measurement across node and occupancy reads.

Kueue's [pinned PodSetAssignment](https://github.com/kubernetes-sigs/kueue/blob/v0.20.0/apis/kueue/v1beta2/workload_types.go)
describes the resources requested by this Workload's Pods at admission.
Volcano's [JobStatus](https://github.com/volcano-sh/apis/blob/master/pkg/apis/batch/v1alpha1/job.go)
records this Job's lifecycle and pod counts. Neither supplies the cell's
complete capacity sample. Extending the state projection with assumed
zeros or using the response arrival time would lose occupancy or make an
old measurement appear current.

ARC's cell-cluster owner supplies the registered site, cell, cluster,
CA and member identities. The installed controller/cell inventory owner
must bind complete resource totals and occupancy to that identity and
one observation cut. It must include measurement time, coverage and the
freshness policy's exclusive expiry. The workload owner supplies the
selected cell and current submission authority. With those inputs,
reuse the authenticated transport and feed `CellCapacitySample` to
`EstimateSubmissionWithin`. Qualify real cell, stale, unavailable and
cross-cell reads through the authorized submission flow before treating
SCH-110 as complete. The present boundary remains open and creates no
capacity producer.

## Atomic gang reservation and release

The version 1 intent already describes named replica groups with counts and
per-replica resources. The reservation stores the normalized intent and all
resource dimensions on one exact attempt row. [PostgreSQL transactions](https://www.postgresql.org/docs/18/tutorial-transactions.html)
give this write the same commit outcome as the workload owner's physical
holds, dispatch and audit writes. Retain the row, quota checks and exact
replay key. The model expresses multi-replica demand independently of where
the cell places the replicas.

[Kueue's readiness check](https://kueue.sigs.k8s.io/docs/tasks/manage/setup_wait_for_pods_ready/)
can evict an admitted workload when all Pods fail to become ready. Its
[dynamic reclaim](https://kueue.sigs.k8s.io/docs/concepts/workload/#dynamic-reclaim)
is a separate partial quota policy. [Volcano gang scheduling](https://volcano.sh/docs/v1.16.0/scheduler/plugins/gang/)
uses a configured minimum threshold. For an ownsolid whole-job hold, keep
all capacity charged until the owner has positive release evidence for the
entire set of reserved replicas. Scheduler phase and timeouts do not prove
physical removal. Kubernetes documents that [force-deleting a Pod](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/#pod-termination-forced)
can remove its API object before the node stops it.

Give the owner a group release operation that validates the evidence set
against the exact stored attempt and its normalized replica groups. Each
entry names the attempt, group and zero-based replica index and represents a
verified stop or verified non-delivery. The owner must validate the physical
node or cell identity and the delivery history before passing an entry. A
slot that had multiple physical incarnations requires proof that all have
ended and that dispatch is fenced. The scheduler checks complete coverage
and rejects repeats, unknown slots, uncertain outcomes and evidence from
another attempt. It then writes the existing release timestamp in the same
owner transaction. The single-replica release path keeps the existing owner
proof contract. Previously released rows remain replayable.

Separate rows per replica would duplicate the exact attempt and complicate
rollback and accounting. Accepting a caller count would let repeated proof
for one replica free the entire group. Inferring physical state from a
Volcano phase, Kueue eviction or a missing object would free live capacity.
The owner remains the source of physical evidence. The candidate schema
needs no change.

Use the existing [Go](https://go.dev/LICENSE) BSD-3-Clause standard library,
[pgx](https://github.com/jackc/pgx/blob/master/LICENSE) MIT and
[PostgreSQL](https://www.postgresql.org/about/licence/) under its PostgreSQL
License. They permit commercial use, offline operation and redistribution
with notices. Kueue, Volcano and Kubernetes are Apache-2.0 references. No
dependency, model or dataset is added.

On isolated PostgreSQL, verify refusal of a gang when one dimension cannot
fit, rollback of all owner-side holds on reservation or commit failure,
replay of one exact gang after a lost commit acknowledgement, refusal of
changed shapes with identical totals, and complete evidence before any
capacity becomes free. Cover cross-tenant proof, missing and repeated
replicas, owner rollback, stale attempts and exact release replay. The
production migration, actual physical receipts and installed scheduler
acceptance remain separate owner work.
