# Authenticate a replacement before Agent connection age

Status: accepted for ARC-107 implementation; native installed acceptance remains open.

## Context

Automatic connection age and GOAWAY already retire a dedicated gateway's physical
transport. An application stream still needs to reconnect between exchanges. The
installed 500-agent drill finished its tasks and restored its assignment, but its
strict terminal snapshot observed 497/500 connections during another age cycle.
Keep that failure and its original assertions.

## Decision

After a successful authenticated first heartbeat, attach optional response
metadata describing the owned socket's remaining drain and hard-close durations.
Derive them from the listener's existing monotonic deadlines. Leave all v1 JSON
frames, task sequences, checkpoints, credentials and server age enforcement intact.
Old agents ignore the hints. New agents retain legacy behavior for old servers,
shared listeners, invalid hints or missing verified transport binding.

Between exchanges, establish a replacement connection with the same verified
origin, node certificate and first heartbeat. Adopt it only after that heartbeat
succeeds. A claim or task result is never replayed by this mechanism. A failed
candidate leaves the original path in place and remains a separately recorded
probe; the existing cycle retains retry ownership. Bound the probe by the normal
request and idle budgets and half the remaining positive drain window.

Bind the committed stream's real peer addresses to gRPC's transport lifecycle.
After adoption, leave the unused old channel quiescent until its physical end or
existing hard-close deadline. A separate bounded worker closes it outside gRPC's
connection callback; explicit shutdown joins that worker. Keep at most one
retiring channel and defer preference probes while it is still live. When both
checks are eligible, prefer a recovered higher-ranked gateway before refreshing
the same gateway, so repeated aging cannot starve failback. Use the same remaining
window budget and natural retirement for a preference move with a valid age hint.

Put this outgoing transport owner in `services/agent/sessiontransport`, within the
existing Agent module. The simulator imports the same implementation. This one
service and its development simulator do not need a new shared module, and the
versioned contract stays independent of gRPC. Keep format and scheduling rules in
the existing Agent contract. Certificate-bearing simulated agents already require
one agent per connection; legacy multiplexed channels retain their prior behavior.

## Consequences

Warm replacement needs temporary headroom within the existing connection and
session limits. Do not raise those limits automatically. Saturation or a failed
candidate can still require the original reconnect/backoff path. This is not a
zero-gap promise during network faults, overload or an uncertain candidate reply.

The original transports must still retire at their unchanged jittered ages,
without forced closure in the healthy installed drill. Report actual sampled
resources and retain the strict 500/500 result and final cursor checks. Small
prototype success does not establish fleet capacity or complete ARC-107.

[Research, primary sources, licenses and retained failures](../research/agent-gateway-rebalance.md#age-handoff-prototype-requirements)
record the verified TLS/HTTP2 experiments, old/new compatibility, admission refusal,
slow handshake, lease/cursor and shared-channel behavior. No third-party dependency
or runtime download is added.
