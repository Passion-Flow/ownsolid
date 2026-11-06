# 0028: Task wakes through a transactional outbox

Status: accepted (ARC-011). Research: [transactional outbox](../research/transactional-outbox.md).

## Context

A claim held open by an agent is woken when a task for its node is written. The wake was a call
inside the writing transaction on the same process, so a claim held on another gateway slept until
its wait ended, and a woken claim waited 100 ms because the task had not committed yet. The roadmap
names LISTEN/NOTIFY as the mechanism to replace; the code never used it, and the research note
explains why we do not adopt it now.

Sweeping for expired tasks ran every 10 seconds, although a live node settles its own tasks when it
claims.

## Decision

**A writer appends to `control_outbox` in its own transaction.** An event exists if and only if the
task commits, and a rollback wakes nobody. The event names a topic and a key, which here is the node.

**Every gateway reads every event, in batches, on its own.** Each holds a position in memory and
polls every `nodes.outbox_poll_milliseconds`, up to 1,000 rows at a time, then wakes the claims it
holds. Only transactions older than every running one are read, by storing the transaction ID in
the row and comparing it with `pg_snapshot_xmin(pg_current_snapshot())`. No value assigned at
write time is enough: a transaction that started earlier can commit later, and a reader past it
would never see its event. The table uses no sequence, which the serving role is not granted, so a
row's position is its transaction ID and a random UUID; events of one transaction come in no
particular order.

**An event is a hint.** A claim looks at the task table before it waits and returns at the end of
its wait, so a late or lost event costs one claim wait. That is the low-frequency reconciliation of
delivery, so nothing replays: a restarted reader starts at the watermark, and an event older than
`nodes.outbox_retention_seconds` is deleted whether or not everyone read it.

**The sweep runs every 30 seconds by default.** It only has silent nodes left to settle. Their
expired tasks now fail up to 30 seconds after their deadline, rather than 10.

**A claim woken from the outbox looks again at once**, since delivery follows commit. MySQL and
Dameng keep the wake on one process and its settle delay until CMP-201 and CMP-202. A process that
is not running the reader also wakes locally, as a test or a command may be.

## Consequences

- Each gateway runs a constant number of reads per second, whatever the size of the fleet or the
  outbox. The reads and the sweep are measured in the agent cost test.
- A wake adds up to one poll interval to dispatch, about the settle delay it removes.
- A long write transaction holds the watermark, which delays every wake until it ends, and claims
  fall back to their wait meanwhile. `ownsolid_node_task_wake_lag_seconds` shows it. Transactions
  with no transaction ID, such as `pg_dump`, do not hold it.
- A logical restore can bring rows whose transaction IDs the new server has not reached. They sit
  above the watermark, are never read and age out.
- Reading by every gateway does not scale with the number of gateways as well as a broker would,
  but gateways number in the tens, and an event is 100 bytes. ARC-015 measures dispatch at load.
- The table is trimmed by delete. ARC-012 decides whether it joins the partitioned append tables.
