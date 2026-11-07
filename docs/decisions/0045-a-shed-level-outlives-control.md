# 0045: A shed level set on the host outlives control

Status: accepted (ARC-016). Research: [break-glass operator path](../research/operator-break-glass.md).
Amends [ADR 0043](0043-shedding-agents-and-the-operator-reserve.md).

## Context

ADR 0043 assumed control keeps answering on its socket with the database stopped. It does not. Control
holds an installation lease on one pinned database session, and a lost session stops the process on
purpose: it is what keeps two writers apart, and nothing may repair or adopt it. With PostgreSQL stopped,
control exits within the lease interval and cannot start again until the database returns. The fleet
then arrives at a control that has only just started, at the moment an operator most wants it held back.

## Decision

**The lease stays.** Keeping control alive without its lease to serve a socket would weaken the fence
for a case a file can serve.

**A host level is held in a file beside the socket**, `socket/agent-shed.json`, mode 0600. Control writes
it when `SetLocalShed` takes a level and removes it once the level is stored and audited.
`ownsolid-install agents shed` writes it when nothing answers on the socket, and validates the request
itself, since control cannot.

**Control reads the file before it serves agents**, and every pacing refresh after that. The level acts
in memory, is stored and audited as any host level, and wins over the stored one until then. A damaged
file is logged and ignored: it never stops control starting, and the stored level stays in force.

## Consequences

- Nothing set on the host is forgotten by a restart, which replaces the consequence ADR 0043 recorded.
- While control is down the level has not acted, because there is nothing to act: agents reach no control
  at all. `ownsolid-install` says so with `AGENT_SHED_HELD`.
- The file is not in a backup. It is outage state, and a restored install has its stored level.
