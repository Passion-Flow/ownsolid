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
before it checks for bootstrap, and partitions reach three months ahead. Attaching does not block
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

**An upgrade copies nothing.** The old table becomes the first partition, from `MINVALUE` to the end
of the month of the upgrade, and leaves when that month expires. An upgrade adds a key and scans the
table once, under an exclusive lock, so its duration grows with the audit history.

## Consequences

- Looking up an audit row by ID alone probes every partition, a cost bounded by the retention.
- The first partition keeps rows older than the retention until its last month expires, so storage
  falls only then.
- A drop can make writers of the parent wait up to two seconds, once a month.
