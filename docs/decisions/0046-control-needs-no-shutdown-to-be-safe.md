# 0046: Control needs no shutdown to be safe

Status: accepted (REL-001). Research: [control crash recovery](../research/control-crash-recovery.md).

## Context

ARC-015 proved a clean restart under load. Nothing proved a kill. Reading every worker showed that
each one keeps its work in the database, but a model import killed while unpacking ended `failed` with
`MODEL_STAGE_INVALID`, because the next pass met the dead attempt's files. A clean stop or a timeout
during unpacking left the same condition, so this was not only a crash problem.

## Decision

**Startup is the recovery path.** No worker may depend on shutdown code for correctness. Graceful
shutdown only shortens the wait. Any new long-running work must recover from a kill at any instruction,
and its recovery must be tested with a real kill.

**A lock that ends with the process proves the last owner is gone.** Where a worker holds a session lock,
as model imports do, recovery acts at once and cannot overlap a live attempt. Time-based leases stay where
they exist already, because they need no pinned session.

**A half-finished attempt is removed, not resumed.** It also spends an attempt, so a bundle that keeps
stopping control ends as `MODEL_RECOVERY_REQUIRED` rather than looping. An attempt that wrote nothing, or
finished all its files, keeps its number. Content that extraction never writes is left for an operator.

**The drill kills the process from outside the container**, because Docker's documentation does not say
how `docker kill` interacts with `unless-stopped`.

## Consequences

- A killed import finishes unattended, at the cost of one of its three attempts and of unpacking again.
- `TestControlCrash` needs the approved bundle and about 8 GB of memory for the install, so it runs on
  the test server rather than in `make test`.
- Several older recovery tests need inputs `make test` does not have. The research note lists which ran.
