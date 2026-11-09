# 0048: Agent gateways beside control

- Status: accepted (ARC-102)
- Date: 2026-11-08

## Context

The agent session stream (ADR 0007) is served inside the single control process, which holds
the installation lock for its whole life. The fleet's TLS handshakes, long-lived connections and
exchanges all land on one process on one host. The outbox, node views, liveness and shed level
were already designed for more than one gateway; what was missing is a process that can serve
agents without being control. Research: [agent gateway tier](../research/agent-gateway-tier.md).

## Decision

**`serve-agent-gateway` is a mode of the control binary.** It reads the control configuration
with an `agent_gateway` section (`listen_address`, `tls_certificate_file`,
`tls_private_key_file`), is PostgreSQL only, opens its own pool without the installation lock,
and refuses to start on a schema other than its own or an installation not yet bootstrapped. It
builds the agent path with the constructor control uses (`identityhttp.NewAgentGateway`) and
serves, for the agent entrance's origin only, the session stream and `/health/live` and
`/health/ready`. Everything else is not found. Enrollment, results, renewals, events and
transfers stay on control's agent entrance.

**It runs only the agent workers**: its liveness table and flush, its outbox reader for task
wakes and node views, the shed level, and the liveness replica and telemetry when configured.
Its metrics carry `service="agent-gateway"`.

**It serves only while control holds the installation.** Every 5 seconds it reads the holder of
the installation lock from `pg_locks`. With none, streams and exchanges are refused with
`SERVICE_NOT_READY` and readiness fails. A new holder is checked against the schema before the
gateway serves again; a different schema stops the gateway. Restore and maintenance, which need
a database without other clients, refuse while gateways run: stop them first.

**Agents choose their gateway.** The agent's `session_addresses` lists up to 16 `host:port`
gateway addresses. An agent holds one session, on the first gateway of its rendezvous order
(SHA-256 of address and node ID) that answers. The certificate is verified for the control
origin's host name, so every gateway presents the agent entrance's certificate. A gateway that
does not answer or is full sends the next attempt to the next gateway; HTTPS is probed only
after every gateway has failed in a row. A working session stays where it is. Without
`session_addresses` the session goes to the control origin, as before. "One or two gateways"
means one session and its failover gateway, not two sessions: control reaches an agent through
the database, so any gateway serves any agent.

**A stopping gateway drains.** SIGTERM refuses new streams and ends every session with
`SERVICE_NOT_READY`, so agents move at once; the liveness table is flushed before the pool
closes.

**A dedicated gateway also ages individual connections.** The listener sets a
jittered drain point and a later socket deadline. Age retires the v1 stream
between exchanges, preserving the current reply and the Agent's persisted
acknowledgement cursor. The common embedded listener follows its existing
lifecycle. See [the ARC-107 listener research](../research/agent-gateway-rebalance.md#dedicated-listener-connection-age).

## Consequences

- The session tier scales by adding gateway processes on more hosts; connections per agent stay
  one whatever the gateway count.
- Control is still one process. Its agent HTTPS paths and its own embedded gateway keep working,
  so the single-node install is unchanged.
- Admission limits, the session cap and the resync rate apply per gateway (ADR 0018, ADR 0011);
  the fleet-wide resync rate is their sum.
- Gateways connect as the serving role, so its connection limit covers control's pool and every
  gateway's. `database.role_connection_limit` sets it (installer: `resources.database_role_connections`);
  `prepare-runtime-access` sets an existing role to it, the one attribute of the role that follows
  configuration, and serving accepts a limit from the pool up to 1,000. A gateway that finds the
  limit full stops with `DATABASE_CONNECTION_LIMIT_REACHED`. A role of the gateways' own, with only
  the grants the agent path needs, is ARC-111.
- Deploying gateways on hosts, with their certificates and the agents' address lists, is
  REL-101 and ARC-104. Rebalancing after a gateway returns is ARC-107.
