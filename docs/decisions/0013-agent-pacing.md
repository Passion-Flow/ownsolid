# 0013: Agent pacing and one retry layer

Status: accepted (ARC-007).

## Context

An agent reported every 10 seconds, a constant in the protocol, and doubled its delay
after a failure, with up to 255 ms of jitter. Three things follow at fleet scale. An
operator who needs to take load off control has no way to slow agents down without
restarting them. Agents disconnected by the same control restart come back together,
because 255 ms spreads nothing. And a failed heartbeat cost more than one request: the
session reopened a broken stream once and tried HTTPS once before it reported a failure,
and the run loop retried on top of that, so a struggling control received the most
requests exactly when it could least afford them.

## Decision

**Control paces agents at runtime.** Every heartbeat reply carries `Pacing`: heartbeat
interval, jitter, and the base and maximum of the reconnect backoff. An agent adopts it
on the reply, so a change reaches the fleet within one interval with no restart. An agent
that has not heard from control uses `DefaultPacing`, with the reconnect bounds from its
own configuration. An agent keeps the last valid pacing when it receives an invalid one.
Task lease timing is not paced; it stays a protocol constant.

**Pacing is one row per cell,** `agent_pacing`, read and changed by platform
administrators through `GET` and `PUT /api/v1/.../admin/agent-pacing`. A change needs the
current version and a reason and is audited as `node.pacing_updated`. Heartbeats read a
per-process copy refreshed at most every five seconds by one caller, so the cost of
pacing does not grow with the fleet and another control process converges within that
time. When the row cannot be read, agents are told the last known pacing; a heartbeat
never fails because of pacing.

**The interval is bound by the online window.** A node counts as online for
`nodes.online_seconds` after its last heartbeat, and a node that looks offline loses
inference. So a change is refused with `AGENT_PACING_EXCEEDS_ONLINE_WINDOW` unless one
and a half of the longest interval fits inside that window, and a stored row that no
longer fits, because the window was lowered later, is replaced by the default when it is
served. Slowing agents further means raising `nodes.online_seconds` first.

**Full jitter.** After a failed cycle the delay is uniform between zero and
`base*2^failures`, capped at the maximum. An interval is uniform within `jitter_percent`
of the heartbeat interval. The claim is held for the shortest possible interval, so new
work still starts at once, and only the jitter remainder is slept. The longest claim wait
is therefore the longest interval, and a session stays open for three intervals of its
own pacing, counted from the last reply.

**One retry layer.** A heartbeat or claim makes exactly one request per attempt, and the
run loop is the only thing that repeats it:

- A stream that breaks is reported as a failure, not reopened on the spot. A stream left
  idle during a long task is replaced before use rather than tried and retried.
- The fallback to HTTPS moves between attempts: when the stream cannot be opened, the
  next attempt tries HTTPS, and only its success starts the fallback window.
- The acknowledgement keeps its one extra try over HTTPS, because the run loop never
  repeats it and a lost reply would strand an acknowledged task. That is still one layer.
- A model file range is retried inside the client, with full jitter; the task is not
  retried on top of it.

## Consequences

- During an outage each agent sends one request per backoff step, spread over the whole
  range, instead of up to three at the start of each step.
- An agent on a path without HTTP/2 reports one failed cycle each time the fallback
  window ends, and is back on HTTPS after its first backoff.
- Retry hints from control (`Retry-After`) are not used yet; ARC-008 adds them per
  operation.
- MySQL and Dameng have no `agent_pacing` table until CMP-201 and CMP-202: they serve the
  default pacing and refuse a change with `AGENT_PACING_UNSUPPORTED`.
- The setting has an API and no console page yet: FLT-113 adds the page.
