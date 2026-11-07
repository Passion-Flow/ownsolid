# Node view cache

Research behind ARC-101: a versioned in-memory snapshot with watches for per-node reads.

## The read to remove

ADR 0022 took the writes out of a repeated heartbeat and left one read-only statement:
`heartbeatUnchanged` joins `managed_nodes`, `workspaces`, `tenants` and `installation_state`
by node ID and credential hash. At 10,000 nodes and a 10 s interval that is about 1,000
statements a second that nearly always return the same row. The same statement is the
heartbeat's authentication, so caching it creates a window in which a revoked node is still
answered. That window is the design problem.

Claims, results and transfers authenticate inside their own transactions and stay as they
are: they take locks and write, so a cached check would save little and weaken a fence.
ARC-109 cuts their repeated checks.

## How others solve it

| Source | What it does | What we take from it |
| --- | --- | --- |
| [Kubernetes API concepts](https://kubernetes.io/docs/reference/using-api/api-concepts/) | A list gives a `resourceVersion` and a watch continues from it. A client must tolerate 410 Gone, and the page's guidance is to list again and restart the watch | When the stream of changes may have a gap, drop the cache and read again. Never patch over a gap |
| [KEP 2340, consistent reads from cache](https://github.com/kubernetes/enhancements/blob/master/keps/sig-api-machinery/2340-Consistent-reads-from-cache/README.md) | A read from the watch cache waits until the cache is at least as new as the store, and falls back to the store if it does not catch up. Consistent GETs stay on etcd | A cache that may be behind falls back to the database. The fallback is the existing path, not a new one |
| [etcd API guarantees](https://etcd.io/docs/v3.5/learning/api_guarantees/) | Watch events are ordered and never drop a subsequence within the history window; a watch resumes from its last revision only while that revision is in the window | Our outbox has a window too (`outbox_retention_seconds`), and a reader behind it has a gap |
| [xDS protocol](https://www.envoyproxy.io/docs/envoy/latest/api-docs/xds_protocol) | A version belongs to the resources rather than the stream, and a reconnecting client names what it holds | The version is what the cache read, not a property of a connection |
| [go-control-plane](https://github.com/envoyproxy/go-control-plane) (Apache-2.0) | A snapshot cache keeps a consistent view per node; the consumer populates and invalidates it | The same shape. The library serves Envoy resources, so it is a reference and not a dependency |

## Chosen approach

**A view per node, loaded on first read.** It holds what the statement returns plus when it
was read. A wrong credential, an inactive node or anything else unusual is never cached and
takes the synchronous path, which holds every rule and error.

**Triggers announce changes.** `managed_nodes`, `workspaces` and `tenants` append to the
outbox (ADR 0028) in the writing transaction when a column the view holds changes. A
trigger rather than Go code, because several modules and the operator tools write these
rows, and a new writer must not be able to forget. Heartbeat writes repeat those columns,
so they add nothing to the outbox. The comparison sits in the function body, because
`CatalogSQL` reads a trigger's condition with `pg_get_expr`, which refuses a condition that
names both `OLD` and `NEW`. The functions run as the writer, which is the serving role, granted
the outbox for task wakes, or the table's owner.

**A change to one node drops that view and wakes its claim.** A held claim then looks at
the table at once, which is the watch: a disabled node hears it without waiting for the
claim to end. A change to a workspace or tenant drops every view, since it is rare and
views reload as heartbeats arrive.

**Three bounds on staleness**, because ADR 0028 makes an event a hint:

1. Normally a view is dropped within one outbox poll of the commit, 200 ms by default.
2. A view older than `nodes.view_max_age_seconds`, less up to half of it at random, is read
   again. This bounds what a lost event can cost, and the jitter keeps a fleet that
   started together from expiring together.
3. If a read of the outbox fails, every view is dropped and the cache serves nothing until
   a read succeeds, so reads go to the database as they do today. The same holds before the
   reader has started, and on MySQL and Dameng, which have no outbox reader.

**A load that overlaps a change is not kept.** The cache counts invalidations. A load notes
the count before reading and stores its result only if the count is unchanged. An event is
delivered after its commit, so a load that started after the event saw the committed row, and
one that started before it is discarded.

**Time comes from the database.** The statement also returned `clock_timestamp()`, which
last-seen times are compared against. A cached heartbeat uses the local clock plus the offset
measured at the last load, so the times stay on one clock.

## Alternatives rejected

- **Time-only expiry.** Simple, but revocation would wait for the whole age. With a short age
  it saves little.
- **Appending events from Go at each write site.** A writer that forgets would leave a stale
  view until its age ran out, with nothing to catch it.
- **LISTEN/NOTIFY.** ADR 0028 removed the need and it does not survive a pooled connection.
- **Preloading every node at startup.** A cost that grows with the fleet at the worst moment.
  Loading on first read spreads it across one heartbeat interval.
- **Caching claims and transfers.** See above: those paths fence work.
- **go-control-plane as a dependency.** Its types are Envoy's; ours are one small struct.

## Dependencies and licences

No new dependency. go-control-plane is Apache-2.0 and was read as a reference only.

## Failure modes and tests

| Failure | Test |
| --- | --- |
| A node is disabled, removed or its credential rotated | Integration: the next heartbeat after one poll takes the synchronous path and is refused |
| A workspace or tenant is disabled | Integration: every view drops |
| A heartbeat write fills the outbox | Integration: an update of `last_heartbeat_at`, or one that repeats a value, appends nothing |
| A change is rolled back | Integration: no event |
| The serving role writes a node | `TestSingleNodeInstall`: a change through the API appends its events under the real role |
| A load overlaps an invalidation | Unit: the result is not kept |
| The outbox read fails | Unit and integration: the cache drops all and serves nothing until a read succeeds |
| An event is lost | Unit: a view past its age is read again |
| The wrong credential with a warm view | Unit: not answered from the view; constant-time compare |
| A heartbeat that carries a change | Existing tests: the digest differs, so the view is not consulted |
| Fleet-wide expiry | Unit: expiry is spread across half the age |
| Regression at load | `TestFleetLoad` passes its thresholds; statements per second are compared |

## What the finished item must do

- A repeated heartbeat with a warm view runs no SQL.
- A revoked node is refused within one outbox poll while the reader is healthy, and within
  `view_max_age_seconds` in every other case.
- A held claim ends when its node's view changes.
- A failed outbox read falls back to the database with no error to any agent.
- The cache never holds more views than `nodes.maximum_live_nodes`.
- Hits, misses, invalidations and fallbacks are metrics.
