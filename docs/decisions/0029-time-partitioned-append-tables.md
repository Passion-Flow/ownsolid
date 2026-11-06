# 0029: Time-partitioned append tables

Status: accepted (ARC-012). Research: [time-partitioned tables](../research/time-partitioned-tables.md).

## Context

Append tables grow with the fleet, and removing old rows with `DELETE` costs scans and vacuum that
grow with it. PostgreSQL drops a partition in one step, but the serving role has no DDL rights, and
the schema check, privilege check and backups all compare exact lists of tables.

## Decision

**Append tables are partitioned by month.** Every partition holds one calendar month in UTC. Rows
leave when their whole month has passed the retention, so retention is a minimum and a row can stay
up to a month longer. Rows are never deleted one at a time on PostgreSQL. MySQL and Dameng keep their
tables and row deletion until CMP-201 and CMP-202.

**One function does the DDL.** `manage_time_partitions`, owned by the migration role, is
`SECURITY DEFINER` so that the serving role gains no DDL right. It pins its `search_path` and
refuses any table that is not a partitioned table of its owner in its own schema. Only append tables
are partitioned, so that is the list it serves. Dropping a month grants nothing new: the serving
role can already delete those rows.

**Partitions are created ahead, and attached.** The retention worker calls the function each pass,
in transactions of its own, and partitions reach three months ahead. Attaching does not block
writers. Dropping takes `ACCESS EXCLUSIVE` on the parent under a two second `lock_timeout`; a blocked
drop is reported and retried at the next pass.

**There is no default partition.** A write beyond the last partition fails. For audit that is
correct, since a privileged action that cannot be audited must not happen, and
`ownsolid_partition_reach_days` warns months before.

**Partitions are not part of the schema.** They appear at run time, so the catalog digest, the
runtime privilege check, the table count and backup fingerprints list parents only, and read
partition rows through them. A partition carries no grants of its own, and the existing check still
refuses any.

**Audit keeps no foreign keys.** ADR 0020 left this open. An audit row outlives what it names, each
key cost a statement per write, and the Workspace key made renames wait for agent traffic. The IDs
come from the server. `PRIMARY KEY (id, occurred_at)` replaces `PRIMARY KEY (id)`; nothing refers to
the table and IDs are random UUIDs.

**An upgrade copies no audit or event rows.** The old table becomes the first partition, from `MINVALUE` to the end
of the month of the upgrade, and leaves when that month expires. An upgrade adds a key and scans each
table once, under an exclusive lock, and rewrites the event tables to add their column, so its
duration grows with that history.

**Events partition by the creation time of their attempt.** `training_events` and
`model_preparation_events` must keep `(attempt_id, sequence)` unique, and a partitioned key must hold
the partition key. A value fixed per attempt adds nothing to the key's meaning, so `attempt_created_at`
keeps uniqueness exact, and an attempt's events stay in one partition. Writers copy it from the
attempt. `event_days` defaults to keeping them: dropping training records is the operator's choice.

**Finished tasks get an append table.** `node_tasks` is updated in place, referred to by six tables
and held to one running task per node, so it cannot be partitioned. A trigger writes
`node_task_history` when a task finishes, so no path can miss it. A finished task leaves `node_tasks`
after `task_days` only if it has a history record and nothing refers to it, and the task list reads
live tasks and the history. Each pass reads one batch and remembers where it stopped, so tasks that
must stay are not read over and over. Their upgrade copies finished tasks into the history once.

**Connections.** Agents have no database driver. A held claim holds no pooled connection, and more
claims than the pool has connections wait at once; a test keeps it that way. `pgxpool` is the pool.
PgBouncer is left to REL-101 and ARC-102, which will know the replica count.

**Left out.** `task_event_outbox`, `task_event_receipts` and `task_event_dead_letters` are updated in
place, and `control_outbox` holds minutes of small rows.

## Consequences

- Looking up an audit row by ID alone probes every partition, a cost bounded by the retention.
- The first partition keeps rows older than the retention until its last month expires, so storage
  falls only then.
- A drop can make writers of the parent wait up to two seconds, once a month.
- Attaching or dropping a partition with a foreign key also locks the referenced table briefly, so
  event tables wait for writers of attempts. A timeout is retried.
- Idempotency keys of tasks pruned from `node_tasks` stop matching after `task_days`.
- A task finished before an upgrade that nothing refers to leaves `node_tasks` like any other.
