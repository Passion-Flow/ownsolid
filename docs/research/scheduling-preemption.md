# Preemption, checkpoint grace and bounded requeue

SCH-105 lets an authorized higher-priority workload reclaim execution capacity
without silently losing a running workload's progress or repeatedly restarting it.
This topic builds on exact reservations, gang release and durable reclaim plans.
It does not replace the chosen Kueue/Volcano cell boundary.

## Primary references

[Kueue preemption](https://kueue.sigs.k8s.io/docs/concepts/preemption/) separates
eviction policy from workload execution and records eviction/preemption
conditions. These conditions alone do not prove physical GPU release.
[Kueue readiness requeue](https://kueue.sigs.k8s.io/docs/tasks/manage/setup_wait_for_pods_ready/#requeuing-strategy)
has an explicit retry ceiling and capped exponential delay. Omitting its retry
ceiling can requeue a readiness timeout indefinitely. Readiness requeue is not a
universal counter for every business preemption.

[Volcano Job](https://volcano.sh/docs/concepts/volcanojob/) provides lifecycle
policies and maxRetry. Its
[Job specification](https://github.com/volcano-sh/apis/blob/master/pkg/apis/batch/v1alpha1/job.go)
makes the retry bound a job field. Recreating a controller object must not reset
ownsolid's separate business lineage.

[Slurm preemption](https://slurm.schedmd.com/preempt.html) distinguishes cancel,
requeue and suspend. GraceTime gives selected modes a stop grace interval;
suspension has different resource implications. A generic preempted flag cannot
be interpreted as freed resources or a completed checkpoint.

[Kubernetes lifecycle hooks](https://kubernetes.io/docs/concepts/containers/container-lifecycle-hooks/)
share a termination grace budget with normal shutdown and may be delivered more
than once. A hook invocation is not an exactly-once durable checkpoint receipt.

These current upstream references establish semantics, not a controller version
upgrade. Installed adapter acceptance must use the signed, pinned offline assets
and their exact API version. No latest-tag image or runtime download is selected.

## Chosen owner boundary

Persist a scoped preemption operation and immutable affected-attempt identity in
the workload owner's transaction, with its audit and stop/checkpoint outbox.
Deliver outside the transaction using one stable operation identity. Replay a
duplicate delivery or uncertain acknowledgement against that identity. The
workload owner verifies that checkpointing and restart are supported for the
particular runtime; ownsolid does not infer support from a signal handler.

Keep the logical hold until positive stop or non-delivery proof exists for every
reserved slot. Expiry of a grace deadline permits the configured stop escalation;
it never releases quota by itself. The owner must distinguish successful
checkpoint persistence from merely starting or finishing a process hook.
Unsupported or failed checkpointing has an explicit policy outcome and actionable
state; no success stub or unverified resume artifact is accepted.

Persist a business-preemption count and capped retry policy in the existing job
lineage. Do not count ordinary transport retries as preemptions, or reset the
counter on process restart, new attempt number or controller replacement.
Current priority authority and quota admission apply again before requeue.
Cancellation must suppress any pending requeue and retain exact replay evidence.

Reject an in-memory retry counter, arbitrary client-supplied hook commands/URLs,
freeing on controller eviction alone, and treating suspension as release.
A second private job store would duplicate the workload owner's authority and
terminal transitions. Shared production interfaces are not changed to invent
the missing lineage.

## Dependencies and offline use

This design adds no dependency, model or data set. Kueue and Volcano remain
references under their
[Apache-2.0](https://github.com/kubernetes-sigs/kueue/blob/main/LICENSE)
[licenses](https://github.com/volcano-sh/apis/blob/master/LICENSE); any later
offline distribution must include the exact assets' license and notice inventory.
Slurm and Kubernetes documentation are references; no source is copied.
Existing Go, pgx and PostgreSQL licenses are recorded in
[the reservation research](scheduling-training-reservations.md). Runtime checkpoint formats and dependencies
must be checked for the selected actual backend before claiming support.

## Acceptance and present boundary

Test owner rollback, duplicate and concurrent delivery, crash before/after
acknowledgement, unknown commit, stale attempt and controller UID, partial gang
stop, checkpoint failure/timeout, cancellation during checkpoint, revoked
authority, cross-tenant requests, restart and controller replacement at the retry
ceiling. A clock change must not reset the persisted count or create a release.
Verify clear wait/error states and a bounded automatic retry path.

The completed product flow lets an authorized operator request preemption,
inspect checkpoint/grace/stop/requeue results, retry a recoverable failure and
observe the next admission through the UI with audit and both languages, offline.
Use the real runtime and installed cell once, then repeat with network
disconnected. Unit state transitions alone cannot close SCH-105.

The current scheduling reclaim rows identify victims and positive releases, but
do not contain the business-preemption lineage, checkpoint authority, runtime
delivery or UI. The workload owner supplies those durable inputs. Until then,
retain SCH-105 open and do not add a shadow runtime/controller or claim that the
queued-admission component implements preemption.
