# 0047: Repeated heartbeats are answered from memory

Status: accepted (ARC-101). Research: [node view cache](../research/node-view-cache.md).
Builds on [ADR 0022](0022-liveness-outside-the-database.md) and [ADR 0028](0028-task-wakes-through-an-outbox.md).

## Context

ADR 0022 left one read-only statement on a repeated heartbeat: about 1,000 a second at 10,000 nodes,
nearly all returning the same row. That statement is also the heartbeat's authentication, so a cache of
it creates a window in which a revoked node is answered. Outbox events are hints and can be lost.

## Decision

**A gateway keeps a view of each node it hears from**, loaded on first read: status, credential digest,
protocol, profile and capabilities. Only a node that would pass is kept. Everything else, including a
wrong credential, goes to the database and the synchronous path as before.

**Triggers announce changes through the outbox**, in the writing transaction, so no writer can forget.
A change to a node drops its view and wakes its held claim. A change to a workspace or tenant drops all.

**Staleness has three bounds.** One outbox poll while the reader is healthy. `nodes.view_max_age_seconds`,
60 by default, less a random part of up to half, whatever happens to events. And the cache closes and
forgets everything when an outbox read fails, so reads go to the database until one succeeds.

**Only the heartbeat uses views.** Claims, results and transfers authenticate in their own transactions,
because they fence work.

## Consequences

- A revoked node may have a heartbeat answered for up to one poll, 200 ms by default, and up to the
  maximum age if events stop arriving while reads succeed, as with a very long transaction. It still
  cannot claim, report or transfer: those check the database. This is the accepted cost.
- Repeated heartbeats keep working while the tables they read are locked.
- MySQL and Dameng have no outbox reader, so their cache stays closed until CMP-201 and CMP-202.
- Schema 50 adds the triggers; a change to the view's columns must change the trigger with it.
