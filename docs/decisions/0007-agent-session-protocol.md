# 0007: Agent session protocol v1

- Status: accepted
- Date: 2026-11-04

## Context

ARC-005 asks for agent protocol v1: an agent-initiated, long-lived gRPC stream over
HTTP/2 through a stateless gateway tier, with a long-poll fallback. Until now every agent
polled with separate HTTPS requests: a heartbeat, then a claim, then a wait of one
heartbeat interval. A new task waited up to one interval before an agent saw it, and each
cycle cost a new request on a shared request slot.

The machine contract already exists as strict JSON types in `api/agent/v1`, with a JSON
schema and validators that both services use. The control plane serves agents on its
HTTPS listener (the admin origin, or the optional dedicated machine listener), and the
offline install opens one port.

## Decision

**One stream per node for heartbeats and claims.** The agent opens the bidirectional gRPC
method `/ownsolid.agent.v1.AgentSession/Connect`. Each agent message has an `id` that
grows by one and carries exactly one exchange: a heartbeat (the existing `Handshake`) or
a claim (`ClaimRequest`). Control answers each message with a `ControlMessage` naming it
in `reply_to` and holding a disposition, a claim or a problem. The first message must be
a heartbeat. Bulk transfers, results and lease renewals stay on their HTTPS paths;
ARC-006 moves acknowledgements onto the stream.

**Strict JSON instead of protobuf.** The stream uses the content subtype
`application/grpc+json` with `SessionCodec`, the same strict decoding as the HTTPS paths:
unknown fields and trailing data fail, and a message is at most 64 KiB. The payloads stay
the existing validated types, so there is one contract and no code generator, and the
offline build needs no extra toolchain. A protobuf codec can be negotiated later through
the content subtype.

**Scope and identity in metadata.** gRPC method paths cannot carry the ARC-004 scope, so
the stream sends `ownsolid-node-id`, `ownsolid-site-id` and `ownsolid-cell-id` metadata
and the credential as a bearer `authorization` value. Another cell's scope is
`RESOURCE_NOT_FOUND`, as on the HTTPS paths; the cell router (ARC-206) can route on it.

**A stateless gateway on the existing listener.** `internal/platform/agentgateway` holds
only open streams. Every exchange goes to the node service with the same per-address and
per-node rate limits, authentication and persistence as the HTTPS paths, so a gateway can
restart or run in more replicas (ARC-102) without state to move. The gateway is mounted on
the control HTTPS listener, which now offers HTTP/2 through ALPN (`h2`, then
`http/1.1`), so agents keep their one control origin and the install keeps one port. A
session holds no shared request slot and no request deadline; the gateway caps sessions
at the configured live-node limit, closes a session silent for three intervals, and lets
a newer session for a node replace an older one.

**Claims wait for work.** A claim may ask control to hold it for up to one heartbeat
interval. Control watches the node, claims, and claims again as soon as a task for that
node is written, so dispatch takes a fraction of a second instead of up to an interval.
The wake is local to the control process, which is enough for the single-node topology;
the transactional outbox (ARC-011) delivers it to other gateways. A missed wake costs at
most one wait, because the agent claims again after its next heartbeat. An idle agent
still reports once per interval, because the wait is part of the interval.

**HTTPS long-poll fallback.** When the stream fails twice in a row while the HTTPS heartbeat
works, the path to control lacks HTTP/2, for example a proxy that speaks only HTTP/1.1. Once
is not enough: a control restart looks the same, and the load test (ARC-015) saw it move two
thirds of a fleet off the stream for five minutes. The agent then sends heartbeats over HTTPS and long-polls
`POST .../tasks/claims` with `{"wait_seconds": 10}`, and tries the stream again after five
minutes. A long-polled claim is bounded by the gateway's session slots, not the shared
request slots. When control refuses the stream (capacity, scope, credential form) or is
unreachable, the agent backs off instead of switching transports. A stream that control
closed while a long task ran is reopened on the next heartbeat.

**Upgrade order.** Control is upgraded before agents. An older control does not offer
HTTP/2 and refuses `wait_seconds`; ARC-303 adds the version-skew policy.

## Consequences

- Agents on the stream see new tasks almost at once and use one connection.
- HTTP/2 is offered to browsers too; HTTP/1.1 remains available.
- The wake channel is per process until ARC-011, so a multi-replica control plane would
  fall back to one-interval dispatch.
- `trusted_proxy_https` deployments, where TLS ends at a proxy, use the fallback unless
  the proxy forwards HTTP/2 to control.
