# 0022: Liveness outside the database

Status: accepted (ARC-010).

## Context

Every heartbeat runs a transaction that locks the node row and rewrites it, with its facts,
and agents that report a component or inference observation rewrite those rows too. At
10,000 nodes and a 10 second interval that is about 1,000 write transactions a second that
say almost nothing new. Most of the content is the same as last time: only timestamps,
free disk, free GPU memory and metric counters move.

## Decision

**A gateway keeps liveness in memory.** `platform/liveness` holds, per node, when it was
last heard, its latest report, a digest of the state last written, and the connectivity
last recorded. Its operations do no IO.

**A heartbeat writes synchronously only when state changes.** The digest covers the whole
handshake with the volatile fields cleared: observation times, free disk, free GPU memory and
component metrics. A field added later counts as state unless someone decides otherwise. When
the digest matches and the last synchronous write is younger than
`nodes.state_refresh_seconds`, one read-only statement authenticates the node and compares its
status, scope, protocol and capabilities, and the sighting goes to the table. Anything else
takes the existing transaction, which holds every validation rule and every error. That
includes a table with no entry, so a restart costs one ordinary heartbeat per node. The refresh
also bounds how long a rule that depends on other rows, such as a removed deployment, goes
unchecked. Each write backdates its age by up to half the refresh, so that nodes written
together do not all come due together. The fast path needs the worker: a process without it
writes every heartbeat, because nothing would persist what the table holds. It is PostgreSQL
only; MySQL and Dameng keep the transaction until CMP-201 and CMP-202.

**Control writes last-seen and the latest reports in batches.** One worker drains the table
every `nodes.liveness_flush_seconds` into `unnest` statements of up to 1,000 rows, in one
transaction per batch, and once more on a graceful stop. The owning module writes its own rows:
`managed_components`, `inference_nodes` and `deployments`. Rows locked by another transaction
are skipped, because the next heartbeat marks them again; a failed batch goes back to the table.
Each statement compares times, so a report older than a synchronous write never overwrites it,
and the table drops such a report when the write happens. Freshness columns keep their meaning:
they advance only when an agent really reported, later by at most the interval, so readers do
not change. Node facts are not batched: free disk reaches the database with the next refresh.
The online window must absorb the interval, so configuration refuses a flush interval the
default pacing does not fit and pacing updates are checked against the window less the flush.

At a 10 second heartbeat the rows written per second stay the same, because a node reports once
between flushes. What goes away is a transaction, a commit and the row locks for every heartbeat,
which is what limits the database.

**Connectivity transitions are stored.** Connectivity stays derived from the last-seen time
wherever it decides anything, so a stopped worker cannot leave a node looking online. The
worker also records each change and its time on the node, because the last-seen time cannot
say when a node came back or how often it flaps. The API returns the time as
`connectivity_changed_at`. A node gets better on its real last-seen time. It gets worse only
on the later of that time and the process start, so a control restart does not read as a fleet
outage. The database refuses to make a node worse when it holds a newer last-seen time than the
gateway does, which covers a replica that is down.

**The replicated store is NATS JetStream KV.** Liveness is a set of per-node maximum
timestamps, so last-writer-wins replication is correct and needs no transaction. Gateways
publish coalesced sightings and watch the bucket, keeping the largest time they see. NATS is
already a dependency of the task bus, is one static binary for offline delivery, and replicates
with Raft at three replicas. The bucket lives in server memory: losing all of it loses nothing
durable. TLS and an NKey are always required. A store that is down never stops a gateway. With
one gateway the table alone is complete, because nothing else needs its view and the database
restores it after a restart, so the single-node install does not run a broker for this. The
`liveness_replica` section selects the store; REL-101 and ARC-102 deploy it.

## Consequences

- A heartbeat that changes nothing costs one read and no write, and the write load of the
  fleet becomes a few batch statements per interval, independent of the agent count.
- Last-seen and report columns lag by up to the flush interval. A crash loses at most one
  interval of sightings, which the next heartbeat replaces.
- `updated_at` of a node no longer moves with its last-seen time: it now means the node changed.
- A rule that depends on other rows can stay unchecked for up to the state refresh.
- A state change on the server side reaches a heartbeat through the read-only statement,
  which returns the status, scope and profile the fast path compares.
- Revocation keeps the meaning of ADR 0020: a call in flight when it commits ends as if it
  arrived just before it.
