# Agent gateway tier

Research behind ARC-102 and [ADR 0048](../decisions/0048-agent-gateways-beside-control.md):
the gateway tier scales horizontally, and each agent connects to one or two gateways.

## The problem in this code base

The agent session (ADR 0007) runs inside the control process: `platform/agentgateway` holds
the streams and hands every exchange to the node service in the same process. Everything an
agent sends therefore goes through one process on one host: the TLS handshakes of a
reconnecting fleet, the connections and goroutines of 10,000 long-lived streams, and about
1,000 exchanges a second at a 10 s interval. Earlier items were built for more than one
gateway but never ran one:

- Task wakes go through the transactional outbox, which every gateway reads (ADR 0028).
- Node views are invalidated from the same outbox, per process (ADR 0047).
- Liveness sits in a per-process table that the process flushes; the database refuses to make
  a node worse when it holds a newer sighting than the writer (ADR 0022).
- The shed level is stored and every process reads it back (ADR 0043).
- Admission limits are per process by design (ADR 0018).

What stands in the way is the installation lock. `serve` takes an exclusive PostgreSQL
advisory lock for the life of the process, so a second control on the same database refuses to
start (`INSTALLATION_PROCESS_ALREADY_RUNNING`). That lock guards bootstrap, migrations,
restore, maintenance and all of control's singleton workers, and making control itself run
several replicas is REL-101's work. The agent path does not need any of that.

## How others solve it

| Source | What it does | What we take from it |
| --- | --- | --- |
| [Borg (EuroSys 2015), section 3.3](https://research.google/pubs/large-scale-cluster-management-at-google-with-borg/) | The elected Borgmaster does not talk to every Borglet. Each replica runs a stateless *link shard* that handles a subset of the machines and reports only differences to the master | Agent traffic is served by stateless processes in front of one authority, and repeated reports cost the authority nothing (ADR 0022, 0047 already do the second half) |
| [Kubernetes HA topology](https://kubernetes.io/docs/setup/production-environment/tools/kubeadm/ha-topology/) | API servers are stateless replicas over one store; kubelets reach them through a load balancer, while controllers and the scheduler elect one leader | Stateless serving replicas next to singleton workers. The gateway is a serving replica of the agent path only, and control keeps its workers |
| [Nomad client `servers`](https://developer.hashicorp.com/nomad/docs/configuration/client#servers) | Clients are given a list of servers and fail over between them; the client shuffles and rebalances its server list over time | An agent is given the gateway addresses and fails over in its own order. Periodic rebalancing is ARC-107 |
| [Teleport proxy peering](https://goteleport.com/docs/zero-trust-access/deploy-a-cluster/proxy-peering/) | Without peering every agent tunnels to every proxy. With it, an agent connects to `agent_connection_count` proxies and proxies forward to each other, so proxies scale without more agent connections. A value of 2 or more is advised, because a client reaches the agent only through a proxy holding its tunnel | Connections per agent must not grow with the number of gateways. One session is enough here: control reaches an agent through the database, so any gateway can serve any agent, and the second gateway is a failover target, not a second tunnel |
| [Rendezvous hashing (Thaler and Ravishankar, 1998)](https://en.wikipedia.org/wiki/Rendezvous_hashing) | Each client ranks the servers by a hash of (server, key) and picks the highest; adding or removing one server moves only the keys it wins or held | Each node orders the gateways by SHA-256 of (address, node ID). Nodes spread evenly, a node's second choice is its failover, and a gateway that leaves moves only its own nodes |
| [Go `http.Server.Shutdown`](https://pkg.go.dev/net/http#Server.Shutdown) | Closes listeners and idle connections, sends HTTP/2 GOAWAY, then waits for active handlers | A long-lived stream is an active handler, so the gateway ends its sessions itself before the shutdown waits for them |
| [PostgreSQL `pg_locks`](https://www.postgresql.org/docs/current/view-pg-locks.html) and [advisory locks](https://www.postgresql.org/docs/current/explicit-locking.html#ADVISORY-LOCKS) | `pg_locks` lists granted advisory locks with the holding backend's pid; any role may read it | A gateway can see, without a lock of its own, whether a control holds the installation and whether that holder is new |

Licences: no new dependency. gRPC-Go (Apache-2.0), pgx (MIT) and the Go standard library
(BSD-3-Clause) are already in use and work offline.

## Chosen approach

**A gateway is the control binary in another mode.** `ownsolid-control serve-agent-gateway`
reads the same configuration with an `agent_gateway` section (listener and certificate pair),
opens the database pool without the installation lock, checks the schema as serving control
does, and builds the agent path with the same constructor control uses, so an exchange is
answered the same way wherever it lands. It serves the session stream and the two health
probes for the agent entrance's origin and nothing else. Enrollment, results, renewals,
events and transfers stay on control's agent entrance: they are a small share of the traffic
and need control's file stores.

**It runs the agent workers and no others.** Liveness (its own table and flush), task wakes
and node views (its own outbox reader), the shed level and, when configured, the liveness
replica and telemetry. Reconciliation, retention, the mail worker, module processors, the
operator socket and outbox trimming stay with control.

**It serves only an installation whose control is running.** Every 5 seconds it reads which
backend holds the installation lock. With no holder it refuses streams and exchanges with
`SERVICE_NOT_READY`; when the holder changes, which is what a control restart or upgrade
looks like, it checks the schema again before it serves, and a schema that differs stops the
gateway as it stops control. Restore and maintenance commands that require a database with no
other clients therefore refuse while gateways run, which is the safe direction: stop the
gateways first.

**Agents order the gateways themselves.** `session_addresses` in the agent configuration
lists up to 16 `host:port` addresses. The agent dials them in its rendezvous order and
verifies the certificate for the control origin's host name, so every gateway presents the
agent entrance's certificate and no verification is relaxed. A gateway that does not answer
or is full gives the next attempt to the next gateway; HTTPS is probed only after every
gateway failed in a row, so a gateway failure does not send its fleet to control's HTTPS
paths. A session that works stays where it is; returning to the first choice after a roll is
ARC-107. The simulator uses the same ordering.

**A stopping gateway drains.** On SIGTERM it refuses new streams and ends every session with
`SERVICE_NOT_READY`, so agents reopen on their next gateway at once instead of waiting for the
connection to be cut, then flushes its liveness table and closes the pool.

**Rejected.**

- *Several control replicas.* Needs leader election for every singleton worker and changes
  the installation lock that restore and maintenance rely on; that is REL-101.
- *A gateway that forwards exchanges to control over RPC.* Moves TLS and connections off
  control but not the exchanges, adds a hop and a second protocol, and leaves control's
  process the limit. ADR 0022 and 0047 already make the per-exchange work cheap enough to run
  anywhere the database is reachable.
- *Every agent connected to every gateway.* Connections grow with the gateway count, the
  failure Teleport's peering exists to avoid.
- *Two simultaneous sessions per agent.* Doubles connections and needs deduplicated
  heartbeats and claims, for no gain: control reaches the agent through the database.
- *A load balancer as the only distribution.* Works and stays possible (one address in
  `session_addresses`), but an offline site may have none, and a client-side order keeps
  failover working without one.

## Failure modes and how the tests cover them

| Failure | Behaviour | Test |
| --- | --- | --- |
| A gateway is killed | Its agents fail one attempt and reopen on their second gateway; no HTTPS probe while a gateway is untried | `TestSessionMovesToTheNextGatewayWhenOneIsDown`, `TestAgentsLeaveAStoppedGatewayForTheNext`, `TestAgentGateways` (kill) |
| A gateway is full | Refused with `ResourceExhausted`; the agent tries the next one | `TestSessionMovesToTheNextGatewayWhenOneIsFull` |
| Every gateway is down, control is up | After one round the agent probes HTTPS and may fall back as before | `TestSessionProbesHTTPSOnlyWhenNoGatewayAnswers` |
| A gateway stops | Sessions drained, agents move at once, the stop takes seconds | `TestDrainEndsSessionsAndRefusesNewOnes`, `TestAgentGateways` (drain) |
| Control stops or restarts | Gateways fence within one check and serve again after a schema check on the new holder | `TestReadyFencesStreamsAndExchanges`, `TestAgentGateways` (control stopped) |
| A task is written while its node's claim is held on a gateway | The outbox wakes it there | `TestAgentGateways` (tasks through two gateways and through one) |
| A request for another origin or path reaches a gateway | Refused; nothing but the stream and health is served | `TestAgentGatewayServesOnlyTheSessionAndHealth`, `TestAgentGatewayRefusesAnotherOrigin` |
| A gateway certificate that does not name the agent origin | Refused at start | `TestAgentGatewayServesTheAgentOriginWithItsCertificate` |
| Nodes pile onto one gateway | Rendezvous order spreads them within a few percent | `TestSessionAddressOrderSpreadsAndMovesLittle`, `TestAgentsSpreadOverTheGateways` |

## What the finished item does

- `serve-agent-gateway` serves agent sessions from a host of its own against the installation's
  database, with control running.
- Agents with `session_addresses` spread over the gateways, never open a session on control,
  and receive tasks written through the console API.
- Killing or stopping a gateway moves its agents to the next one within minutes, without
  HTTPS fallback; stopping control fences the gateways and they serve again when it returns.
- Every scenario ran end to end with two gateways and 500 simulated agents on `ownsolid-cpu`.

## Open questions

- Gateways connect as the serving role and share its connection limit, which the first run found:
  the installer had sized it to control's pool, so a second gateway was refused. The limit is now
  configured (`database.role_connection_limit`). A role of the gateways' own with narrower grants is
  ARC-111.

- Placing gateways on hosts, their certificates and the agent configuration is installer work
  for the topologies that have more than one host (REL-101, ARC-104). The single-node install
  keeps the gateway inside control.
- Admission limits, session caps and the resync rate are per gateway (ADR 0018, 0011). At
  several gateways the fleet-wide resync rate is the sum; set it per gateway accordingly.
- Rebalancing after a gateway returns, maximum connection age and GOAWAY on age are ARC-107.
- Load at fleet scale across gateways belongs to ARC-105.
