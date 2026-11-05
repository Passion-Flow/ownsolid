# 0022: Liveness outside the database

Status: accepted (ARC-010). Implementation is in progress; the roadmap box says when it is
complete.

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
handshake with the volatile fields cleared, so a field added later counts as state unless
someone decides otherwise. When the digest matches and the last write is recent, one
read-only statement authenticates the node and checks its scope, and the sighting goes to
the table. Anything else takes the existing transaction, which holds every validation rule.
That includes a table with no entry, so a restart costs one ordinary heartbeat per node.
The fast path is PostgreSQL only; MySQL and Dameng keep the transaction until CMP-201 and
CMP-202.

**Control writes last-seen and the latest reports in batches.** One worker drains the table
on a configured interval into bounded `unnest` statements. Rows locked by another
transaction are skipped, because the next heartbeat marks them again; a failed batch is
marked again for the next pass. Freshness columns keep their meaning: they advance only when
an agent really reported, later by at most the interval. Readers do not change. The online
window must therefore absorb the interval, and configuration and pacing checks say so.

**Connectivity transitions are stored.** Connectivity stays derived from the last-seen time
wherever it decides anything, so a stopped worker cannot leave a node looking online. The
worker also records each change and its time on the node, because the last-seen time cannot
say when a node came back or how often it flaps. A node gets better on its real last-seen
time. It gets worse only on the later of that time and the process start, so a control
restart does not read as a fleet outage.

**The replicated store is NATS JetStream KV.** Liveness is a set of per-node maximum
timestamps, so last-writer-wins replication is correct and needs no transaction. Gateways
publish coalesced sightings and watch the bucket, merging what others heard. NATS is already
a dependency of the task bus, is one static binary for offline delivery, and replicates with
Raft at three replicas. With one gateway the table alone is complete, because nothing else
needs its view and the database restores it after a restart, so the single-node install does
not run a broker for this. Multi-replica topologies select the store in typed configuration;
REL-101 and ARC-102 deploy it.

## Consequences

- A heartbeat that changes nothing costs one read and no write, and the write load of the
  fleet becomes a few batch statements per interval, independent of the agent count.
- Last-seen and report columns lag by up to the flush interval. A crash loses at most one
  interval of sightings, which the next heartbeat replaces.
- A state change on the server side reaches a heartbeat through the read-only statement,
  which returns the status, scope and profile the fast path compares.
- Revocation keeps the meaning of ADR 0020: a call in flight when it commits ends as if it
  arrived just before it.
