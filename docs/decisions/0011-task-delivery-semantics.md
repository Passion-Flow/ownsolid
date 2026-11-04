# 0011: Task delivery semantics

- Status: accepted
- Date: 2026-11-04

## Context

ARC-006 asks for per-node sequence numbers, idempotent tasks, acknowledgements, resume
from the last acknowledgement and a rate-limited full resync.

Until now a claim committed a lease and then sent it. When the reply was lost, for
example because the stream broke, the task stayed `running` with a token no agent held.
The node then idled until the 30-second lease expired, the next claim spent one of the
task's three attempts, and training and model preparation tasks, which have exactly one
attempt, failed outright. Nothing told control which tasks an agent had actually taken,
and nothing detected an agent and a control plane whose records had drifted apart, for
example after a database restore or a lost state directory.

Task creation was already idempotent through request keys, and results through the
completed lease and result hashes. A node has at most one pending or running task.

## Decision

**Per-node sequences.** Every task of a node gets the next number of that node's
sequence, starting at 1. A `BEFORE INSERT` trigger on `node_tasks` assigns it from
`managed_nodes.task_sequence`, so every insert path gets it, concurrent inserts for a node
serialize on the node row, and no caller can choose or change it. Leases carry the
sequence.

**Acknowledge before executing.** The agent acknowledges a lease (`ack` on the session
stream, or `POST .../tasks/{task_id}/acknowledgements` on HTTPS) before it does anything
with it, and executes only after control confirms. Control records the acknowledged
attempt on the task and the node's highest acknowledged sequence, and renews the lease.
An acknowledgement is idempotent; the agent sends it once more over HTTPS when the stream
breaks. The agent saves its cursor, the highest sequence it acknowledged, durably before
it sends the acknowledgement, so its cursor is never behind control's record.

**An unacknowledged lease was never delivered.** The agent claims only when it holds no
task, so a claim while a lease is unacknowledged proves the lease was lost on the way.
Before any claim path runs, control returns such a lease to `pending` without spending an
attempt, and the claim delivers it again with a new token, which fences the lost one. The
sweep does the same for an expired unacknowledged lease. A training or model preparation
lease may bind a new stop authority while its task was never acknowledged; once
acknowledged, the stop authority stays immutable as before. After
`maximum_task_deliveries` deliveries without an acknowledgement the task fails with
`TASK_ATTEMPTS_EXHAUSTED`, so an agent that cannot accept a task does not loop until the
deadline. A release or that failure gives the attempt back, so attempt 0 always means no
agent acknowledged the task. The training and model preparation reconcilers release an
expired unacknowledged lease before they judge a job, and share one decision,
`nodes.NeverDelivered`: attempt 0 with no events and no outcome proves the command never
reached an agent, even when a stop authority was bound to a lost lease, as long as the task
is pending or failed by the delivery bound. The job then ends without manual recovery and
its GPU is released. Any other finished task proves nothing and keeps its physical owner.
An acknowledged lease that expires, because the agent
died while executing, is reclaimed on the next attempt as before, and the executors'
journals resume it.

**Resume from the cursor, resync when it disagrees.** Every claim carries the agent's
cursor. Control asks for a full resync instead of answering with a lease when the cursor
is ahead of the node's assigned sequence (control restored to an older state), behind the
highest acknowledged sequence (the agent lost its state), or ahead of the unfinished task
(an older task still in flight). A resync names control's highest acknowledged sequence;
the agent recovers all local work from its journals, adopts that cursor, and the next
cycle reports a full heartbeat and claims from there. Control grants one node at most one
resync per `resync_interval_seconds` and the fleet at most `resync_per_second` through a
token bucket in each control process; a deferred resync answers `RATE_LIMITED` with a
retry hint, so a mass reconnect cannot turn into a resync storm. Grants are audited as
`node.resync_granted`, releases as `node.task_released`.

**Engines.** Sequences, acknowledgement records and resync exist on the PostgreSQL
lineage only (migration 000031). The MySQL and Dameng chains lack the columns until
CMP-201 and CMP-202: there leases carry sequence 0, an acknowledgement renews the lease
it names, and claims never release or resync.

**Upgrade order.** Control is upgraded before agents. An agent built before this change
does not acknowledge, so its leases are released and delivered again until the delivery
bound fails them, and it refuses the new lease field. Agents must be upgraded with
control; ARC-303 adds the version-skew policy.

## Consequences

- A lost claim reply costs one claim cycle instead of a lease expiry and an attempt, and no
  longer fails a training or model preparation task.
- Every claim runs one short preflight transaction before the claim paths. ARC-009 reviews
  its cost with the other agent calls.
- The fleet-wide resync rate is per control process; with more gateway replicas (ARC-102)
  the effective rate is the sum.
