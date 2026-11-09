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
