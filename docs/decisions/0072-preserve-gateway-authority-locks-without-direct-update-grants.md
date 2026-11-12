# 0072: Preserve gateway authority locks without direct update grants

Date: 2026-11-13

Status: accepted for ARC-111 prototyping; production activation remains open.

## Context

The dedicated Agent gateway currently uses control's database principal. ARC-111
requires a separate principal with its own connection budget and only its active
session/worker permissions. The existing node authority path takes shared row
locks on installation_state and tenants before locking a node. PostgreSQL requires
UPDATE on at least one column even for FOR SHARE. SELECT-only table grants cannot
run that path; granting an arbitrary UPDATE column can also permit real mutation.

## Decision

Prototype a narrowly scoped security-definer authority-lock function before
activating the gateway principal. Bind trusted table references when defining its
SQL body, fix its search path, revoke PUBLIC execution in the same transaction,
and grant execution only to the intended principal. Preserve the original scope,
status and transaction-lock lifetime. Do not grant direct mutation on installation
or tenant authority solely to obtain a shared lock, and do not remove the lock.

The native PostgreSQL prototype proves this mechanism on synthetic tables,
including foreign scope, execution denial, temporary-name shadowing and competing
writers. It does not activate a product function or replace full permission-closure
review. The production catalog, protected operator preparation, normal and
post-restart readiness, every delegated profile and installed delivery still need
independent acceptance. Existing control principal grants and old gateway/agent
behavior remain unchanged during their rolling upgrade.

## Consequences

This introduces privileged SQL only after its closed execution surface and exact
catalog admission are verified. A connection-limit-only clone of control's grants
would not meet ARC-111. A writable dummy column or removal of the authority fence
would weaken the current boundary. A new database service is unnecessary.

The [research and native prototype evidence](../research/agent-gateway-database-role.md)
record the upstream PostgreSQL contracts, licenses, observed SQLSTATEs and remaining
scope. No production migration number or new dependency is introduced by this ADR.
