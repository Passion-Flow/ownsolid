# Time-partitioned append tables and database connections

Research behind ARC-012 and [ADR 0029](../decisions/0029-time-partitioned-append-tables.md).

## The problem

`audit_events` grows with every privileged action and every agent write, and retention removes it
100 rows at a time. Training and model preparation events grow with every running job and are never
removed. `node_tasks` keeps every finished task beside the few live ones. A bulk `DELETE` of old rows
costs a scan, dead tuples and vacuum, all of which grow with the fleet.

## How others solve it

**PostgreSQL declarative partitioning** is the upstream answer. Dropping or detaching a partition "is
far faster than a bulk operation" and "entirely avoid[s] the VACUUM overhead caused by a bulk
`DELETE`" ([partitioning](https://www.postgresql.org/docs/18/ddl-partitioning.html)). The limits that
shape this design, from the same page and
[ALTER TABLE](https://www.postgresql.org/docs/18/sql-altertable.html) and
[CREATE TABLE](https://www.postgresql.org/docs/18/sql-createtable.html):

- A unique or primary key must include every partition key column. There is no global `UNIQUE (id)`.
- `CREATE TABLE ... PARTITION OF` and `DROP TABLE` of a partition take `ACCESS EXCLUSIVE` on the parent.
  `ATTACH PARTITION` needs only `SHARE UPDATE EXCLUSIVE`, and a valid matching `CHECK` skips its scan.
- `DETACH ... CONCURRENTLY` cannot run in a transaction block and is not allowed with a default
  partition.
- A default partition must be scanned or excluded by a `CHECK` whenever a partition is attached.
- `BEFORE ROW` triggers on `INSERT` cannot change the destination partition.
- The planner handles up to a few thousand partitions fairly well, and pruning uses only the
  partition key, not indexes. A query without the key probes every partition.

**pg_partman** automates this on native partitioning under the
[PostgreSQL License](https://github.com/pgpartman/pg_partman/blob/development/LICENSE.txt). Its
background worker needs `shared_preload_libraries`, and it would be another extension to build,
package and upgrade for each supported PostgreSQL. Maintenance is a small part of it that we need.

**TimescaleDB** hypertables do the same with more features, but much of it is under the
[Timescale License](https://github.com/timescale/timescaledb/blob/main/LICENSE). The licence forbids
products that are "primarily database storage or operations products" from relying on some rights and
adds conditions on customers modifying the schema, and the
[legal page](https://www.tigerdata.com/legal/licenses) needs a lawyer's reading before use in a product
sold to others. Rejected on licence risk and on packaging.

## Chosen approach

Native partitioning by month, maintained by the product, with no new dependency.

**Who runs the DDL.** The serving role has no DDL rights and must not gain any
([AGENTS.md section 9](../../AGENTS.md)). A `SECURITY DEFINER` function owned by the migration role
creates partitions ahead of time and drops expired ones, for a fixed list of tables. It takes a table
name from that list and numbers, never a free identifier, and qualifies every object. Giving the
serving role this function grants nothing it could not already do: it can delete those rows today.

**Create by attach, drop on a short lock.** A new partition is created as a plain table and attached,
which does not block writers. Dropping takes `ACCESS EXCLUSIVE` on the parent, under a short
`lock_timeout`; a timeout is retried at the next pass. It happens once a month per table.
`DETACH CONCURRENTLY` is not available inside a function.

**No default partition.** It would block concurrent detach, force scans at every attach and hide a
maintenance failure. Partitions are created three months ahead, every retention pass and at start,
and a metric reports how far ahead they reach. A write with no partition fails, which for audit is
the correct behaviour: a privileged action that cannot be audited must not happen.

**Keys.** `audit_events` becomes `PRIMARY KEY (id, occurred_at)`; IDs are random UUIDs from the server,
and nothing references the table. Event tables must keep `(attempt_id, sequence)` unique, so they
partition by the attempt's creation time, copied into each row: that value is fixed per attempt, so
adding it to the key does not weaken uniqueness.

**Retention gets coarser.** A row now lives until its whole month has passed the cutoff, so up to a
month past the configured age. Retention is a minimum.

**Catalog and counters.** Partitions appear at run time, so the schema catalog, the runtime privilege
check, table counts and backup manifests must ignore them, and test fixtures must drop them.

## Task history

`node_tasks` cannot be partitioned: it is updated in place, it is referenced by foreign keys from
about eight tables, and its partial unique index for one running task per node has no partition key.
It is not an append table. Finished tasks instead get an append table, `node_task_history`, written by
a trigger on the transition to a final state so every path is covered, and rows nothing references
leave `node_tasks` after a retention period.

## Tables left out

`task_event_outbox`, `task_event_receipts` and `task_event_dead_letters` are updated in place and
guarded by state triggers, so they are not append tables. They serve the optional broker. `control_outbox`
holds minutes of small rows and stays on trimming.

## Connections

Agents never connect to the database: the agent has no driver, and calls reach it through control.
A long-lived stream or a held claim must hold no pooled connection while it waits, which is the
property worth testing. `pgxpool` already bounds the connections of a process
([pgxpool](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool)), with `MaxConnLifetime`, jitter and
idle limits.

PgBouncer in transaction mode would multiplex several control processes, but it breaks `SET`,
`LISTEN`, session advisory locks and SQL `PREPARE`
([features](https://www.pgbouncer.org/features.html)), and dataset processing takes session advisory
locks. Its licence was not established from a primary source here. Not needed for one process;
REL-101 and ARC-102 decide it with real replica counts.

## Failure modes and tests

| Failure | Expected | Test |
| --- | --- | --- |
| Write beyond the last partition | Fails clearly; the metric shows the reach | Real insert past the range |
| Maintenance not run for months | Next pass fills every gap | Function with a far later clock |
| Drop blocked by a long reader | Lock timeout, reported, retried, writers resume | Held lock test |
| Upgrade with existing rows | All rows readable, nothing copied | Lineage upgrade test |
| Backup and restore | Partitions and rows survive, catalog passes | Real backup and restore |
| Serving role calls the function with another table | Refused | Permission test |
| Caller changes `search_path` | Objects stay qualified | Hostile `search_path` test |
| Held claim or stream | No pooled connection held | Pool statistics during a held claim |

## What the finished item must do

1. Audit, training and preparation events and finished tasks live in monthly partitions.
2. Expired months are removed by dropping a partition, never by deleting rows.
3. The serving role gains no DDL right.
4. An upgrade keeps existing rows.
5. Backup, restore and the schema check work with partitions created at run time.
6. A held claim or open stream holds no database connection.
