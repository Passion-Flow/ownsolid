# Transactional outbox for control-plane events

Research behind ARC-011.

## The problem in this code base

The roadmap names LISTEN/NOTIFY, but the code does not use it. What exists is `wakeHub` in
`nodes/claim_wait.go`, which wakes a claim held open on the same process. It has two defects a
second gateway (ARC-102) would expose:

- A task written through one gateway does not wake a claim held on another. The agent finds the
  task only after its claim wait ends, which is up to one heartbeat interval.
- The wake fires inside the writing transaction, before commit, so a woken claim sleeps 100 ms
  (`claimSettle`) and can still miss the row.

The module also sweeps for expired tasks every 10 seconds, although a live node reaps its own
tasks when it claims, so only silent nodes need the sweep.

## How others solve it

**Transactional outbox.** The service stores the message in the same transaction as the business
change, and a separate process delivers it, so a message is sent if and only if the transaction
commits. The relay may deliver more than once, so consumers must be idempotent, and the stated
drawback is that a developer can forget to publish
([microservices.io](https://microservices.io/patterns/data/transactional-outbox.html)).

**LISTEN/NOTIFY is the alternative this item rejects.** PostgreSQL delivers notifications in commit
order and fails commits when its queue fills
([NOTIFY](https://www.postgresql.org/docs/current/sql-notify.html)). Commits that notify have been
reported to serialize on a global lock under load
([Recall.ai](https://recall.ai/blog/postgres-listen-notify-does-not-scale)). Whether that holds is
disputed: Recall.ai's post was later updated to credit a core fix, and DBOS reports that the patch
it examined does not remove the lock and that batching raised throughput about twentyfold
([DBOS](https://dbos.dev/blog/postgres-listen-notify-scalability)). We did not measure this. The
decision does not rest on it: NOTIFY also needs a dedicated listening connection per gateway,
which the connection-pooling goal of ARC-012 works against, and a notification sent while a
listener is reconnecting needs a table to recover from anyway.

**Reading the outbox in order.** A sequence value is taken before commit and lost on rollback, so
positions have gaps and can become visible out of order; a reader that remembers the highest
position it saw can skip a row that commits late. The documented fix stores the transaction ID in
an `xid8` column and reads only rows below the oldest transaction still running,
`pg_snapshot_xmin(pg_current_snapshot())`, ordered by transaction ID and position
([Dudycz](https://event-driven.io/en/ordering_in_postgres_outbox/);
[functions](https://www.postgresql.org/docs/current/functions-info.html)). Its stated downsides:
reads are delayed while a transaction runs long, and order follows when a transaction started, not
when it wrote.

## Chosen approach

- One table, `control_outbox`, with `seq`, `xid8`, topic and key. It is append only and trimmed by
  age; ARC-012 decides whether to partition it.
- A writer appends in its own transaction. PostgreSQL only; MySQL and Dameng keep the process-local
  wake until CMP-201 and CMP-202.
- Every gateway reads on its own with an in-memory cursor `(xid, seq)`, up to 1,000 rows at a time,
  and starts from the current watermark. Nothing is replayed after a restart, because wakes are
  hints: a claim always looks at the task table first.
- Delivery is at least once and consumers are idempotent. A wake with nothing to claim costs one
  look for work.
- Reads poll every 200 ms by default. That cost is constant per gateway and does not grow with the
  fleet.
- The sweep runs every 30 seconds by default instead of 10, and the claim wait remains the backstop
  for a wake that is late or lost.

## Alternatives rejected

| Alternative | Why not |
| --- | --- |
| LISTEN/NOTIFY | See above |
| Remembering the highest `seq` | Skips a row that commits late, which is the bug the watermark prevents |
| Holding a cursor at a gap until a timeout | More code for the same guarantee, and it needs a timeout that can still lose a row |
| One dispatcher deleting rows with `SKIP LOCKED` | Suits one consumer per message. Every gateway must see every wake, because only the holder of the claim can act |
| Publishing through NATS | The single-node install runs no broker, and the wake must work there |
| Logical decoding | Needs a replication slot and a privileged role; a stalled slot retains WAL. Too much for a hint |

No new dependency, so no licence changes.

## Failure modes and tests

| Failure | Expected | Test covers |
| --- | --- | --- |
| Transaction rolls back | No event | Rollback test against PostgreSQL |
| A later transaction commits first | Earlier row is still delivered, after it commits | Two real transactions committed out of order |
| Long write transaction | Delivery waits for it, claims fall back to their wait, and a metric shows the lag | Held transaction test |
| Database unavailable | Reader keeps its cursor and resumes | Failed read test |
| Batch larger than the limit | Read in several passes, none lost | Limit test |
| Task written through another gateway | The claim held elsewhere wakes promptly | Two services on one database |
| Restored database with higher stored xids | Rows stay above the watermark, are not read and age out | Stale xid test |
| Restart | Reader starts at the watermark without replay | Start position test |

## What the finished item must do

1. A task committed through one control process wakes a claim held on another within about a second,
   without waiting for the claim to end.
2. A task that rolls back wakes nobody.
3. No woken claim needs a settle delay on PostgreSQL, because delivery follows commit.
4. The polling cost per gateway does not grow with the fleet or the outbox's history.
5. A claim still finds its task when delivery is late or lost.
6. The sweep runs at low frequency, and dead nodes' expired tasks are still settled.

## Open question

Dispatch latency at load depends on how long write transactions run, because they hold the
watermark. ARC-015 measures it and ARC-106 publishes it.
