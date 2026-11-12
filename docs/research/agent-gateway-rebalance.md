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

The same test ran as a Linux amd64 binary on `ownsolid-cpu` in a network
namespace with only loopback. All 40 agents went from 18/22 sessions to 0/40 and
returned to their original 18/22 split in 12.732 seconds. The separate
500-agent installation drill stopped when its Docker-in-Docker container exited
during setup. No installation-level result is recorded for ARC-107.

## Dedicated listener connection age

The standalone `serve-agent-gateway` listener has its own TLS socket and
connection budget. Attach the drain instant in
[Go's ConnContext](https://pkg.go.dev/net/http#Server) and bound the
underlying socket's read and write deadlines through the public
[net.Conn interface](https://pkg.go.dev/net#Conn). Choose the instant
uniformly from the final tenth of the configured maximum age. Set the
hard socket deadline after the largest allowed claim wait and the
existing request budgets for peer validation, the exchange and its
reply. Clamp every transport deadline update to that hard limit,
including a request to clear a deadline. This keeps a stalled TLS or
HTTP/2 peer from keeping the aged connection indefinitely.

The stream owns one exchange at a time. Age ends it between exchanges
using the same v1 `SERVICE_NOT_READY` status as process drain. An
exchange admitted before the age keeps its normal context and can
deliver its reply. The v1 Agent retains its persisted acknowledged
sequence and already retries an interrupted acknowledgement through
the authenticated HTTPS path. The dedicated server's original process
drain stays independent. The connection's HTTP owner initiates standard
HTTP/2 shutdown at the age instant, independently of later requests.
The existing stream drains at the application boundary, with the socket
deadline as the final bound. The earlier response-header mechanism only
notified connections that made a new request after age; the stronger
acceptance below requires automatic notification of an established stream.

Reject use of the global Gateway drain for one connection. It would
cancel existing claims and refuse every healthy connection. Avoid
writing frames directly on the TLS socket, which would race the
standard library's HTTP/2 framer. The embedded Agent endpoint shares a
listener with other traffic and needs its own scoped integration.
The dedicated option keeps older YAML valid with a 1,800-second
maximum and leaves the v1 messages, leases and cursor format intact.

Test verified local TLS and HTTP/2 through the production listener.
Hold an admitted claim across age, complete its reply, observe the v1
drain status, and reconnect to a second listener with the same
acknowledged cursor. Refuse post-age exchanges on the old stream.
Check the pinned HTTP/2 close behavior and bound a transport that
does not reconnect. The Go and gRPC licences above apply and no
new runtime dependency or data is added. This scope does not
qualify installed gateways or fleet load.

The earlier deadline-based two-listener regression passed under the Go race detector and
in an isolated Linux arm64 container with no network. It held a
claim across age, received the next lease and the legacy drain
status, then confirmed that the same listener accepted a new
connection and the second listener received the previous acknowledged
cursor.
That transport test decoded a real HTTP/2 GOAWAY on an aged request
and proved the socket deadline closed a silent connection. The
legacy configuration default, bounds and process-wide drain passed
in the same disconnected run. For the focused replay use:

```sh
go -C services/control test -p 1 ./internal/platform/agentgateway ./internal/platform/httpserver ./internal/platform/config \
  -run '^(TestAgedGatewayFinishesHeldClaimAndPreservesCursor|TestDedicatedTLSAgeSendsGoAwayAndBoundsIdleTransport|TestAgentGatewayConnectionMaxAgeDefaultsAndBounds|TestDrainEndsSessionsAndRefusesNewOnes)$' -count=1
```

## Automatic GOAWAY for an established stream

The existing frame-level test sends a new request after the age instant.
It proves that the response header starts HTTP/2 drain, but does not prove
automatic GOAWAY while an earlier stream remains active. The source-level
reason is that the age middleware runs when a request starts; it cannot
change headers already sent by a long-running gRPC response. A hard socket
deadline bounds closure but is not a graceful-drain notification.

[gRPC A9](https://github.com/grpc/proposal/blob/master/A9-server-side-conn-mgt.md)
requires an age-triggered GOAWAY followed by time for outstanding RPCs.
The pinned [gRPC-Go handler](https://github.com/grpc/grpc-go/blob/v1.85.0/server.go)
delegates HTTP/2 ownership to net/http. The pinned
[Go HTTP/2 setup](https://github.com/golang/go/blob/go1.27.3/src/net/http/http2.go)
registers its graceful connection shutdown with the public
[http.Server.Shutdown](https://pkg.go.dev/net/http#Server.Shutdown).
That API waits for active requests, while the socket owner still needs a
deadline and forced close when its grace expires.

Use one standard HTTP server owner per accepted dedicated gateway
connection, retaining the same TLS, connection budget and request policy.
Its single-connection listener closes acceptance without closing the active
socket. At that connection's existing jittered age, invoke its public
Shutdown with the bounded grace. The server owns all HTTP/2 frame writes.
Join the per-connection owner on peer closure or process shutdown and retain
the hard transport deadline. This scopes the standard shutdown mechanism
to one connection without new dependencies or wire changes. See
[ADR 0070](../decisions/0070-scope-gateway-http-shutdown-to-one-connection.md).

Reject a global server Shutdown because it drains healthy peers too.
Reject direct frame writes and access to Go internal connection objects.
Replacing the handler with native gRPC transport would also require a new
health-listener or protocol multiplexer and changes to deployment routing.
The selected approach preserves the existing gateway origin and health paths.
The existing Go BSD-3-Clause and gRPC-Go Apache-2.0 licences still apply;
there is no new dependency, model, data set or runtime download.

Require a real TLS/HTTP/2 test with one response held open before age.
Without another client request, observe NO_ERROR GOAWAY covering the
accepted stream, then release and verify the complete original response.
Keep the leased-claim/cursor regression, silent-peer deadline, healthy-peer
acceptance and bounded process shutdown checks. Run the native target,
installed two-gateway rollout and simulator distribution checks before
closing ARC-107. Source review alone is not acceptance.

The strengthened held-response test first failed with EOF after 1.91
seconds: no request followed the age instant and no GOAWAY arrived.
The connection-owned Shutdown implementation then passed three race
repetitions of the frame-level and leased-claim checks, legacy age bounds
and process drain. Normal and timed-out process shutdown and release of a
single connection slot each passed three race repetitions. The complete
httpserver, agentgateway, config and cmd/server package suites also passed
with the race detector on macOS arm64, using a short-path private APFS
temporary volume. Target Linux and installed rollout qualification of this
new transport owner, including simulator resource measurements, remain open.

The installed extension reuses the existing network-none single-node and
two-gateway harness with 500 individually enrolled simulated agents. Select
the minimum supported 60-second connection age and a five-second minimum
preference check. Run 100-second simulated tasks on every twentieth node,
confirm their acknowledged attempts before age, and retain those tasks
through automatic connection replacement. Correlate server-generated
connection IDs in session and retirement logs; client headers cannot choose
that ID. Require each original transport to retire naturally within the
configured jitter envelope and each agent to reconnect on its original
preferred gateway. Then kill and return one gateway, verify convergence
to the original assignments, task completion and persisted cursors. Record
physical connection overlap and gateway resources as well as steady counts.
Keep daemon startup failure diagnostics before the fixture is removed.

The connection-identity trace and installed extension are prepared source,
with formatting, layout and shell syntax checked. Their updated test input
has not run: archive transport stalled and a separate native admission
refused the original memory threshold. Preserve that distinction from the
earlier four-package Mac race result. Resume native qualification before
promoting this extension or closing ARC-107.


## Installed resource observations

The connection-ID map proves which transport owned an authenticated
session, but it is not a count of every live TCP socket. A preference
probe or a connection still draining can overlap its replacement.
Record that overlap and gateway resource usage during the installed
500-agent drill before accepting the per-connection server owner at
production scale. Do not infer a 10,000-node capacity result from this
functional fixture.

[Docker stats](https://docs.docker.com/reference/cli/docker/container/stats/)
provides scoped JSON for named containers without adding an endpoint to
the product. Record CPU, memory, process/thread count and network counters
with timestamps at enrollment, automatic age, the single-gateway phase
and return to the original assignment. Preserve the raw rows. Docker's
Linux CLI subtracts inactive file cache from reported memory, unlike the
raw API total; identify which representation is recorded rather than
calling either one RSS. A stopped gateway produces no stats row, which
must be classified as the deliberate stopped phase rather than a zero
resource measurement.

The existing control image already contains Python. A fixture-only,
bounded subprocess can read the gateway network namespace's
[Linux TCP tables](https://docs.kernel.org/networking/proc_net_tcp.html)
and count states for the configured serving port, including IPv4 and
IPv6 when present. Keep established and closing states separate from
LISTEN and TIME_WAIT, so ordinary TCP cleanup is not described as an
active session leak. This proc interface is deprecated in favor of
socket diagnostics; keep it limited to the isolated test fixture and
prefer the kernel's current diagnostics interface for any future
production collector. Do not add a public debug route, grant a new
container capability or change the Agent wire format to obtain test
measurements.

Sample at a declared interval and label the maximum as an observed
sample maximum, not an exact instantaneous peak. Bound every Docker
command. Recheck an execution failure against the named container's
running state so a deliberate kill is distinguishable from a broken
observer; other observation failures must fail the fixture. Prototype
the namespace/port observation with known open and closed sockets before
adding it to the production acceptance test. These observations have
not run yet and the installed test remains unqualified.

No dependency, model or dataset is added. Docker/Moby uses
[Apache-2.0](https://github.com/moby/moby/blob/master/LICENSE), and the
existing Python interpreter and standard library use
[PSF License Version 2](https://docs.python.org/3.12/license.html).
Both allow commercial and offline use and redistribution with their
applicable notices. Reading the kernel's documented interface does not
copy kernel implementation code into ownsolid.
