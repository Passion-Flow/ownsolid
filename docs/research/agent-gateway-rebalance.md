# Agent gateway rebalancing

Research for ARC-107. An agent keeps the session that survived an outage, so rolling a
gateway can move its whole share to a surviving gateway indefinitely. The original
rendezvous order in [ADR 0048](../decisions/0048-agent-gateways-beside-control.md) already
defines which nodes should move back when a gateway returns.

## Upstream behavior

- [Nomad client configuration](https://developer.hashicorp.com/nomad/docs/configuration/client#servers)
  describes a client that shuffles and rebalances its server list. Ownsolid keeps its
  rendezvous order so that a recovering gateway only attracts the nodes that rank it
  above their current gateway.
- [gRPC A9](https://github.com/grpc/proposal/blob/master/A9-server-side-conn-mgt.md) uses
  randomized connection ages to spread reconnects. Age and grace are separate so an
  existing RPC can finish after the server starts refusing new streams. Under
  [RFC 9113 section 6.8](https://www.rfc-editor.org/rfc/rfc9113.html#section-6.8),
  GOAWAY identifies the last stream the peer might have processed. It does not migrate
  an existing bidirectional RPC.
- Ownsolid attaches gRPC with
  [grpc.Server.ServeHTTP](https://github.com/grpc/grpc-go/blob/master/server.go).
  Its [handler transport](https://github.com/grpc/grpc-go/blob/master/internal/transport/handler_server.go)
  does not own the HTTP/2 connection. Setting gRPC server keepalive parameters on this
  handler does not install a maximum age on the Go HTTP listener. The listener owner
  needs to implement and test connection aging and GOAWAY separately.

No new dependency, model or data set is needed. The existing Go runtime and standard
library ([BSD-3-Clause](https://go.dev/LICENSE)) and gRPC-Go
([Apache-2.0](https://github.com/grpc/grpc-go/blob/master/LICENSE)) permit commercial
use, redistribution with the applicable notices, and offline execution. The test
fleet generates synthetic identities and facts. Nomad is a behavioral reference only.

## Decision

When a fallback gateway is serving a live session, schedule a check of one higher-ranked
gateway after a random delay between one and two configured intervals. The default
interval is 60 seconds. If that gateway is still down, try the next higher-ranked
candidate at the next interval. Once a higher-ranked candidate returns an authenticated,
valid heartbeat disposition, end the previous stream and send future claims only to
the new stream. The heartbeat is the existing heartbeat for that cycle when the move
succeeds. A failed check leaves the healthy session in place and the cycle reports its
heartbeat on that session. The check has a request deadline and runs between claims.
No probe runs on the first-ranked gateway or while the client is on HTTPS fallback.

An unauthenticated health probe cannot prove that the candidate can accept this
agent's stream or certificate. Closing a healthy stream before testing the candidate
would make every failed preference check a disruption. Keeping two steady streams
would double the connection count and complicate task delivery. A temporary second
stream is used for a heartbeat only, and the old stream remains the sole claim
stream until that heartbeat succeeds.

No wire messages, version, operation or authentication change. Existing agents
continue to use failover and stay on the gateway they reach. Updated agents can
rebalance against the current v1 gateway, and old and new agents can coexist through
an upgrade. No change to persisted cursors or task leases is needed. The server-side
age and drain work must also keep this wire behavior for old agents.

## Verification

- Hold the preferred gateway unavailable or at capacity. The agent must keep
  heartbeating and claiming on the fallback without switching to HTTPS.
- Return a valid disposition from the preferred gateway. The next claim must use
  that gateway and carry the same acknowledged sequence. A refused disposition must
  keep the fallback. Each probe must have its own bounded budget and the next
  attempt must be spread by jitter.
- With three gateways, a failed first choice must not stop return to a healthy
  second choice. No checks run at the best rank or without a live fallback.
- Run the simulator with one connection per agent against two TLS/HTTP/2 listeners.
  Take one listener out of service and return it. Record distribution before,
  during and after the outage, and check that every simulated agent returns to its
  own first choice. The local listener fixture isolates client behavior. The
  production completion gate also needs real gateway replicas and an age/GOAWAY
  drain while claims and acknowledged work are in flight.

ARC-107 remains open until the listener work and production gate are complete.

The local simulator test passed under the Go race detector. Its 40 agents split
23/17 before the first gateway went down, 0/40 during the outage, and 23/17 after
13.032 seconds from restoration. The test used a 5-second minimum check interval
and local TLS/HTTP/2 listeners with simulated control responses.
