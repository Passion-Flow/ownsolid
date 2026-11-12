# 0074: Bound tenant admission locking

Status: Accepted
Date: 2026-11-13

## Context

SCH-103 needs a current fair queue decision and its logical reservation in one
workload-owner transaction. The retained candidate locks every workspace in the
supplied tenant domain. The configured Workspace maximum permits 10,000 entries;
one advisory lock and round trip per entry would tie admission cost and the
PostgreSQL shared lock pool to that policy maximum.

## Decision

Use one transaction-level tenant read/write advisory fence. Ordinary reservation,
release and reclaim mutations take the shared side. ReserveQueuedWithin takes
the exclusive side before its current owner/usage read, then reuses ReserveWithin
to admit the selected head. The lock order is policy, tenant, requesting workspace,
cohort. Existing policy and workspace fences remain. A cohort never locks another
workspace. Queue admission must be the first scheduling write in the transaction.

The owner supplies a complete, bounded, server-authorized domain and eligible
batch. It retains actor/workspace/node/job/physical-resource fences, task/dequeue/
audit writes, current authority and commit ownership. The callback does not take
other jobs' row locks or perform writes/network I/O. Snapshot ranking remains a
separate hint; final admission uses current READ COMMITTED statements.

## Consequences

Scheduling admission uses a constant number of advisory locks for a tenant,
rather than one per configured workspace. Ordinary writers in distinct workspaces
continue concurrently, while a queued decision excludes their mutations until
commit. Unrelated tenants have separate keys; hash collisions only serialize.
A caller must not upgrade a previously acquired shared scheduling fence as a
later operation. It rolls back on cancellation, deadlock, malformed scope or owner
failure and retries with the documented ordering.

This is an internal scheduling protocol, not a replacement for Kueue or Volcano.
It introduces no persistent table or new dependency. Before production activation,
all Control writers must use the new protocol; mixed-version direct writers are
not an atomic queue cutover. ARC/IAM retain grants/readiness, and the workload owner
retains real dispatch/UI acceptance. The previous broad lane candidate is preserved.

## Evidence required

Qualify real PostgreSQL shared/exclusive and cross-tenant behavior, bounded
10,000-workspace lock count, queue and owner rollback/replay, cancellation, lost
commit acknowledgement, borrowing/reclaim/positive release and existing read APIs.
The source documents transaction advisory locks and their lifetime in
[PostgreSQL 18](https://www.postgresql.org/docs/18/explicit-locking.html#ADVISORY-LOCKS)
and the shared/exclusive functions in its
[administration reference](https://www.postgresql.org/docs/18/functions-admin.html#FUNCTIONS-ADVISORY-LOCKS).
Implementation and acceptance remain separate from this decision.
