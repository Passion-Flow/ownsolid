# Retain assistant reservations until usage is known

Status: accepted for the staged PostgreSQL execution-budget component.

## Context

AGT-112 needs user and installation budgets that survive replicas and
process restart. The assistant currently has deterministic commands and an
installation execution policy, but no authoritative user allowance, model
execution route or registered budget schema. Historical workspace cost
reports cannot atomically admit concurrent assistant work.

## Decision

Reserve one execution and its finite worst-case token charge on both scopes
in one borrowed PostgreSQL transaction. Recheck the current session and
workspace before consulting a trusted policy source. Bind the reservation
UUID to the exact execution digest, maximum charge and authenticated scope.
Lock the reservation, installation counter and user counter in that order.
Policy revisions reuse the same window counters.

Only a newly committed reservation permits one execution. A held replay is
pending and a settled replay is complete; neither grants another execution.
An unknown commit returns no reservation. Reconcile the original UUID
instead of retrying under a new identity.

Keep a hold through timeout, cancellation, restart and window expiry.
Settlement requires the original current authority, bounded actual usage
and a stable digest of the trusted gateway receipt. Refund only unused
tokens, once. Keep the admitted execution charged. Do not infer a refund
from a missing reply, failed connection or model-generated usage.

## Consequences

A failed or uncertain execution can retain conservative capacity. Operator
reconciliation needs its own authorized, audited contract; it cannot be an
automatic time-based refund. An installation counter serializes writes to
its current window, which is necessary for the first exact shared ceiling.

The component introduces no alternative policy store or default allowance.
Its DDL remains an isolated fixture input until the persistence owner adds
a forward migration and restore catalogue. The gateway adapter must prove
its maximum charge before admission. Human confirmation, effect permissions
and success auditing remain the action owner's responsibilities. Loop
limits use persistent per-run claims, an immutable initial ceiling and a
monotonic admitted-step count. A pending or completed replay cannot dispatch
again. Stop and policy loss suppress an in-flight result; no database
transaction spans the command execution. Zero-token deterministic completion
uses a local status digest, which provides no model-usage evidence.

Production binding, telemetry, controls and browser flows remain
open in AGT-112. See the
[research and verification contract](../research/product-assistant-execution-budgets.md).
