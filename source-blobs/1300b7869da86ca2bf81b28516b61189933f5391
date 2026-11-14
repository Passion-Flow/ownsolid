# 0040: Agent calls that change nothing do not wait for the disk

Status: accepted (ARC-015). Research: [fleet load test](../research/fleet-load-test.md). Builds on
[ADR 0020](0020-agent-call-cost-rule.md) and [ADR 0022](0022-liveness-outside-the-database.md).

## Context

The fleet load test reached a heartbeat p99 of a second or more on a host with nothing else
running, while the median stayed at 3 ms. PostgreSQL's slow statement log placed the cause: every
30 to 60 seconds, nine `commit` statements ended at the same instant, the longest after 2,176 ms.
One slow sync of the write-ahead log held every committing transaction and, with them, nine of the
process's ten pooled connections. The heartbeat that only reads had no connection to run on.

The disk is not unusually bad. On `ownsolid-cpu`, idle, a synchronous 8 KB write takes 0.16 ms at
the median and 0.36 ms at p99, with 16 stalls over 50 ms in ten minutes, the longest 463 ms. A
customer's disk will stall too, so a heartbeat must not depend on one not stalling.

The log names no caller, so which commits were waiting is inferred from the code and confirmed by
the result. An agent call locks its node row first (ADR 0020), and a row lock gives the transaction
an ID, so its commit waits for the disk although a claim that finds no work changed nothing. At 2,000
nodes that is about 400 such commits a second: a 50 ms stall gathers twenty of them, more than the
pool holds. With this change, moments with five or more slow commits fell from dozens a run to at
most ten.

## Decision

**An agent call that changed nothing commits with `synchronous_commit=off`**, set for that
transaction alone with `SET LOCAL`. The statements of every machine transaction run through a
watcher, `transaction.Watched`, which counts a change for any affected row, any failed statement and
any query whose text is not a plain read; a row lock is not a change. Only when it saw none does the
transaction stop waiting. A transaction that only held a lock loses nothing if a crash forgets it.

**Two writes the next heartbeat repeats also do not wait:** a heartbeat whose state is the one last
written, which only the refresh sent down the synchronous path, and a batch of sightings written by
the liveness worker. Each says only that a node was still there. A crash can move a node's last-seen
time back by under a second until its next heartbeat. The liveness table's digest only ever claims a
state that was written durably, so a crash cannot leave it trusting a state the database lost.

PostgreSQL states the risk of an asynchronous commit as "data loss, not data corruption", at most
three times `wal_writer_delay`, and that "no inconsistency can be introduced", because transactions
replay in commit order. ([Asynchronous Commit](https://www.postgresql.org/docs/18/wal-async-commit.html))

**Everything else keeps waiting for the disk:** a heartbeat that changes state, a claim that leases a
task or expires one, connectivity changes, enrollment, results and every call on the legacy
transaction, which writes outside the watcher.

## Rejected

- **Only the heartbeat writes.** Tried first: three runs still missed once, with nine claims holding
  the pool. It removed the wrong writers.
- **A separate connection pool for reads.** It would keep heartbeats moving through a stall while ten
  claims queue on the disk, and adds a setting to size. It stays available if a run shows the
  remaining synchronous writers can still fill the pool.
- **A claim without the node lock.** The lock fences a status change (ADR 0020); removing it trades a
  latency problem for a correctness one.
- **`synchronous_commit=off` for the whole database.** A task's lease or result could be lost after
  control told an agent it was recorded, the case the PostgreSQL documentation warns against.
- **The write-ahead log on its own disk.** Worth documenting for operators, but it cannot be required,
  and any disk stalls.

## Consequences

- At fleet scale most agent calls stop costing a disk sync, so a slow disk degrades task writes, not
  liveness.
- The watcher judges a query by its text. A read that calls a database function which writes would
  be misjudged. The schema's only functions that are not triggers, `local_cell_id` and
  `local_site_id`, read; a function that writes must be called through `Exec`.
- MySQL and Dameng are unchanged until CMP-201 and CMP-202.
