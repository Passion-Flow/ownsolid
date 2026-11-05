# 0017: Agent call admission, priorities and retry hints

Status: accepted (ARC-008).

## Context

Every agent call paid two database transactions before any work: a per-address and a
per-node rate bucket, each a locked row in `rate_limit_buckets`. At the planned 10,000
nodes that is about 2,000 extra transactions a second on heartbeats alone, and the cost
was paid again when control was already overloaded, so shedding load cost as much as
serving it. One per-node bucket covered heartbeats, claims, results and renewals
together: a node busy with renewals could lose its heartbeats. Agent calls also shared
the HTTP request slots with the console, so a burst of model downloads could refuse an
operator's login, and a flood of console requests could refuse heartbeats. A refusal
told the agent nothing about when to return, and an agent that failed together with
thousands of others backed off on its own schedule only (ADR 0013).

## Decision

**Every agent call is one named call with a priority.** `api/agent/v1` lists the calls
(`enroll`, `heartbeat`, `claim`, `acknowledge`, `renew`, `result`, `stop`, `events`,
`download`, `upload`) and classifies every agent HTTPS path and session exchange as one
of them. Each call has a priority, highest first:

| Priority | Calls | Why |
| --- | --- | --- |
| quarantine | result, stop | They end or contain work and free leases. |
| heartbeat | heartbeat, claim, acknowledge, renew | They keep nodes online and dispatch moving. |
| telemetry | events | Progress of running work. |
| bulk | enroll, download, upload | Work that can wait. |

The node HTTP contracts name each operation's call in `x-agent-call`, and a test keeps
them and the protocol table in step. ARC-009 adds each call's declared server cost to
the same table.

**Admission is in memory and comes first.** `internal/platform/agentadmission` admits
every agent call, over HTTPS and on the session stream, before any database work:

1. A token bucket on the client address, before the credential is checked.
2. A token bucket per node and call, keyed by node, call and a hash of the presented
   credential, so a caller without the node's credential cannot spend the node's
   allowance. Enrollment names no node and keeps its database limit on join attempts,
   shared by every control process.
3. A serving seat. The process has `seats` seats for agent calls; each priority has a
   cap, a queue and a longest wait. A freed seat goes to the oldest waiter of the
   highest priority under its cap. Telemetry and bulk together may never hold every
   seat, so heartbeats and quarantine reports always find one.

A held claim takes a seat only while control looks for work, not while it waits, so
thousands of idle agents waiting for tasks hold no seats. The bucket table is bounded;
full buckets carry no state and are dropped when room is needed, at most once a second.

**Agent calls do not use the HTTP request slots.** Agent seats bound them instead, so
the console and the model gateway keep `http.max_active_requests` to themselves. This is
the first part of the independent operator path that ARC-016 completes.

**Refusals carry a retry hint.** A call over its limit is refused with 429
`RATE_LIMITED` and `Retry-After` set to when its next token arrives; a call that found no
seat is refused with 503 `AGENT_OVERLOADED` and the priority's `retry_seconds`. On the
stream the hint travels as `SessionProblem.retry_after_seconds`. Hints are bounded by
`MaxRetryAfterSeconds`, the longest reconnect backoff.

**Agents wait at least the hint.** The run loop waits the longer of its full-jitter
backoff and the hint plus up to half the hint again, so agents refused together do not
return together. A model range refused with a hint is retried after the hint. A lease
renewal refused with a hint is tried again at the next renewal tick while the lease
still holds; the task ends only if the lease expires first. The three copies of the
renewal loop (component and inference, training, model preparation) became one.

**Observability.** The controller pushes seats in use, queued calls, admitted calls and
refusals by priority and reason to the metrics store, and logs refusals at most once per
priority and reason every ten seconds, with the count suppressed in between.

## Consequences

- An agent call costs no database write before it is served, and a shed call costs no
  database work at all.
- Limits are per control process. With several gateway replicas (ARC-102) an agent that
  spreads its calls gets each replica's limit; the cell's own capacity is what admission
  protects, so that is the intended scope.
- The configuration keys `rates.agent_ip_*`, `rates.agent_transfer_ip_*`,
  `rates.agent_node_*` and `rates.agent_transfer_node_*` are gone; a configuration that
  still sets them fails to load. `agent_admission` replaces them.
- An agent built before this change rejects a session problem that carries
  `retry_after_seconds` as an invalid reply and backs off. Control is upgraded before
  agents (ADR 0007), and ARC-303 adds the version-skew policy.
- The console page for these settings and a live view of the counters are FLT-113 and
  the observability module's work; the counters are in the metrics store now.
