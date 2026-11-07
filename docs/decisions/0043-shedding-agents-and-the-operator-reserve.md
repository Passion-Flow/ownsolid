# 0043: An operator can shed agents, and keeps a reserve when they overload control

Status: accepted (ARC-016). Research: [break-glass operator path](../research/operator-break-glass.md).
Builds on [ADR 0018](0018-agent-call-admission.md) and [ADR 0032](0032-static-stability.md).

## Context

ADR 0018 took agent calls off the console's request slots. Agents still share the database pool: 64
agent seats face 10 pooled connections, so a busy fleet can leave a sign-in waiting for a connection.
An operator also has no way to tell a fleet to back off, short of stopping control.

## Decision

**Agents leave a reserve in the pool.** `database.operator_reserved_connections`, default 2: the seats
in use never exceed the pool less the reserve. Telemetry and bulk keep their promise inside the smaller
number: together they never hold every seat, so a heartbeat always finds one.

**A shed level refuses agent traffic, lowest priority first.** `none`, `bulk`, `telemetry` (bulk and
telemetry) and `heartbeats` (all but quarantine reports). A shed call is refused before any limit is
spent or any database work is done, with `AGENT_OVERLOADED` and its priority's retry hint, which agents
honor. Running work continues through the strongest level, because that is what static stability
promises; nodes drop offline in control and return when it is lifted.

**The level acts in memory first and is stored second.** It is stored beside the cell's pacing, so it
survives a restart and reaches every gateway within the pacing refresh, through a worker that does not
depend on agent traffic, which the strongest level stops.

**Administrators use `GET` and `PUT /api/v1/admin/agent-shed`**, with a version, a reason and an audit
record in the same transaction, as for pacing.

**On the host, `SetLocalShed` needs no database.** It takes effect at once, says whether it was stored,
and the worker keeps trying until it is, audited with the `operator` audience and no user. Until then a
stored level does not overwrite it.

## Consequences

- At most eight agent calls use the database at once by default, against 64 before. A fleet that needs
  more raises the pool.
- Control stops when it loses its database, so what a host level does across that is in [ADR 0044](0044-a-shed-level-outlives-control.md).
- MySQL and Dameng cannot store a level until CMP-201 and CMP-202; an administrator is told so.
