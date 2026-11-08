# Unified scheduling job model

## Problem and upstream practice

SCH-101 gives training, inference, notebooks and batch one resource request and
lifecycle vocabulary. The job records intent. An admission reservation and a
runtime observation have separate owners and may arrive after submission.

- [Kueue Workload](https://kueue.sigs.k8s.io/docs/concepts/workload/) models
  homogeneous pod sets as named groups, each with a replica count and per-pod
  resource requests. Kueue sums the group demands to reserve quota.
- [Volcano Job](https://volcano.sh/docs/concepts/volcanojob/) models tasks with
  replicas and distinct roles. Its job lifecycle and placement stay inside the cell.
- [Kubernetes workload controllers](https://kubernetes.io/docs/concepts/workloads/controllers/)
  use different runtime controllers for finite jobs and long-lived services.
  [Slurm heterogeneous jobs](https://slurm.schedmd.com/heterogeneous_jobs.html)
  show why distributed work must retain separate component requests.

## Approach

Use one versioned, adapter-neutral internal intent with a workload kind, an
existing opaque source ID and named replica groups. The source owner still
validates executable payloads, including models, images, data and credentials.
Only the authenticated control plane may attach tenant and workspace identity.
An internal caller takes the transaction-bound scope from its existing authority
and supplies it separately from the intent. No public endpoint is introduced in
this slice.

Requests use exact integers for millicores, memory bytes and whole accelerators.
Accelerators use qualified resource names so later devices can coexist without
charging distinct classes against the same key. Identical group sets compare
equal regardless of caller order. Validate before multiplication and addition so
a malformed request cannot wrap into a smaller admission charge.

Persist immutable intent in PostgreSQL, uniquely keyed by cell, tenant,
workspace, workload kind and source ID. Insert and replay in the caller's
transaction, with a unique index as the concurrency arbiter. A changed payload
under the same key conflicts. Use compare-and-set transitions and return the
current state on an exact replay. Keep the state machine independent of wall
clock. A cell adapter must prove completion or removal before a caller advances
a job to a terminal state or releases quota.

A Kubernetes object as the primary record would make the control layer depend on
one cell API even when reading queued work. A single flat GPU count loses
driver/worker shape and accelerator identity. Separate job models per runtime
would duplicate quota and lifecycle decisions. Keep the intent independent of
all three choices.

## Licensing and offline operation

Go's [BSD-3-Clause licence](https://go.dev/LICENSE), existing pgx's
[MIT licence](https://github.com/jackc/pgx/blob/master/LICENSE), the existing UUID
library's [BSD-3-Clause licence](https://github.com/google/uuid/blob/master/LICENSE)
and the [PostgreSQL licence](https://www.postgresql.org/about/licence/) allow
commercial use, redistribution with the required notices and offline operation.
[Kueue](https://github.com/kubernetes-sigs/kueue/blob/main/LICENSE) and
[Volcano](https://github.com/volcano-sh/volcano/blob/master/LICENSE) are Apache-2.0
upstreams used as design references. This slice adds no runtime dependency, model,
dataset or external connection.

## Failure tests and acceptance

Test all four kinds, multi-role demand, duplicate names, integer overflow,
cross-workspace access, idempotent concurrent creation, changed-payload replay,
illegal transitions, transaction rollback and rereading through a fresh
connection. The full roadmap item also needs the existing workload owners to
submit through this contract, durable scheduler and adapter wiring and access from
the product entrances. Those routes and permissions must be exercised in an
offline installation before the item ticks.
