# 0020: Agent call cost rule and locks that do not span the fleet

Status: accepted (ARC-009).

## Context

Control must serve about 1,000 agent calls a second at 10,000 nodes. A call whose cost
grows with the fleet works in every test and fails in production, so the cost has to be
stated and checked for every call, not found in a load test.

Measuring the calls found two kinds of growth:

- **Work.** Enrollment ran `count(*)` over `managed_nodes`, reading every node to compare
  the fleet with its limit.
- **Waiting.** Every agent call locked the Workspace row `FOR UPDATE`, so all nodes of a
  Workspace ran one at a time; a call's wait grew with the nodes beside it. Training and
  preparation calls also locked the person who started the job, image pulls locked that
  person, their session and the license row, and enrollment locked the installation row
  that every agent call reads.

The obvious repair for the waiting, a shared lock, does not work. On PostgreSQL 18.6, 32
clients taking `FOR SHARE` in a loop held off a single `FOR NO KEY UPDATE` for 14 seconds,
until the load ended, and a 3 second `lock_timeout` never fired: each new sharer restarts
the wait. Agent traffic never pauses, so operators could not have changed a Workspace.

## Decision

**Every call declares its cost.** `api/agent/v1` gives each call a `Cost`: the most SQL
statements it runs, counting triggers and foreign key checks, and what it may grow with:
nothing, the items of a batch, or bytes. The fleet is deliberately not a value. Raising a
declaration is a change to that table, visible in review.

**A test enforces it.** `TestAgentCallCostDoesNotGrowWithTheFleet` drives one agent
through every call over real HTTPS against PostgreSQL, on a fleet of 50 nodes and on one of
3,000, each with task and audit history. It records what the database did with
`auto_explain`, and fails when an exchange:

1. runs more statements than its call declares, or more on the large fleet;
2. scans more than twice the rows plus 16 on the large fleet. A cost linear in the fleet is
   60 times larger there. Blocks are reported but not judged: a connection with a cold
   catalog cache reads a hundred more, which would make the test flaky;
3. reads a table with no usable index. Sequential scans are priced out, so one that still
   runs means no index serves the read, even on a table the test left small;
4. wants a row every node shares. Each exchange runs while another transaction holds
   `FOR SHARE` on the installation, tenant, Workspace, user and membership rows, so an
   exclusive lock on any of them times out and fails the exchange.

`make test-agent-cost` runs it, and CI runs it on every change.

**An agent call locks only what belongs to its node.** On PostgreSQL it takes its node row,
then reads the Workspace status without a lock. The node lock is the fence:

- A status change locks the Workspace's nodes before it changes the row. It waits for calls
  in flight, and a later call sees the new status. Visiting every node is fine for a rare
  operator action.
- Agent calls no longer lock the user, membership or session rows of a person's grant. A
  call in flight when a revocation commits ends as if it arrived just before it; the next
  call fails.
- The license admission check only reads, so it no longer locks the singleton row.

**Workspace writers use `FOR NO KEY UPDATE`.** Every audit event takes a key share on the
Workspace row through its foreign key, and `FOR UPDATE` conflicts with key shares. A writer
would starve behind them as above, and could deadlock with a call that holds its node. A
test keeps `FOR UPDATE` on that row out of PostgreSQL statements.

**Enrollment reads a counter.** A trigger on `managed_nodes` keeps `tenant_node_counts`
for every write path. Enrollment locks that row, which serializes it against the limit
without touching what running agents share. It takes the installation row `FOR SHARE`.

## Consequences

- Agent calls of one Workspace now run in parallel, bounded by seats (ADR 0018) instead of
  by a row lock.
- Renaming a Workspace changes a unique column, which is a key update, so a rename does
  wait for key shares and can be slow under load. Status changes do not. ARC-012 decides
  whether audit keeps that foreign key.
- Enrollment still holds a `FOR SHARE` on the Workspace row. Enrollment is rate limited
  and bulk priority, so it is not the steady stream that starves a writer.
- MySQL and Dameng keep their locks and `count(*)` until CMP-201 and CMP-202.
- The test covers the exchanges of the node and training paths. Component, inference,
  model preparation and registry operations are audited by reading, not measured; ARC-108
  adds them. Their locks follow the same rule, and registry pulls were changed by this
  decision without an exchange of their own.
- Constant costs can still be high: an event batch runs 75 statements and an upload chunk
  76, mostly repeated authority checks. They do not grow, and ARC-109 cuts them.
