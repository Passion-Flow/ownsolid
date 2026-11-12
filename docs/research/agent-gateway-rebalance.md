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

The resumed input 10fc41864033fd64e48fdc98ef601622360a7e17 passed the
complete httpserver, agentgateway, config and cmd/server suites with the
Go race detector on Darwin arm64: 7.719, 5.409, 49.164 and 16.959 seconds,
respectively. This includes the updated server-generated identity and
forged-header frame regression, held reply/cursor behavior, bounded
shutdown and connection-slot release. The Linux amd64 e2e fixture also
compiled. No production wire message, lease or cursor field changed.
The raw four-package race log SHA-256 is
920ad6bb5c725452991ca72b6c75f2323faa43e0842427379ba44fbd038ffc70.

The installed 500-agent extension has not run, and this updated transport
has not run on native Linux amd64. Keep that scope distinct from the old
40-agent client result. ARC-107 remains open pending installed rollover,
physical overlap/resource observations and native qualification.
The socket observer prototype's Linux preflight saw 607604 KiB MemFree
and 0.39 percent full I/O PSI avg10 and returned 75 before starting a
namespace or socket. It did not lower the 2 GiB free-memory threshold or
start a compiler, container or database. Its actual socket behavior is
still unqualified.


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

The bounded socket prototype subsequently passed on native Linux x86_64 in
an isolated network namespace: initially one listener and no established
connections, exactly three established connections with three accepted clients,
then no established connections after closing both ends. Preflight measured
6504960 KiB MemFree, 19437340 KiB MemAvailable and full I/O PSI avg10 0.01.
The namespace subprocess had a 96 MiB address-space limit, two CPU seconds and
a ten-second wall limit. It started no compiler, container or database.

Embed this same observer as e2e test data. Sample only the two named gateway
containers every five seconds, with bounded Docker commands, and retain raw
JSON resource rows and TCP-state counts with UTC timestamps and drill phase.
Record phase checkpoints as well as periodic samples. A stopped gateway is
expected only during the explicit first-gateway outage; missing observations
in every other phase fail the test. Join the sampler before fixture cleanup.
Report maxima as sampled observations, including draining sockets separately
from authenticated sessions. This adds no runtime product endpoint or wire field.

Input cfb48b4863097b0067bcc0dc14a8f5eecfeeedde passed the two focused
e2e observation/log-parser roots with the race detector on Darwin arm64 in
1.564 seconds, followed by e2e package vet and Linux amd64 fixture compilation.
The tests cover live and deliberately stopped gateways, unexpected stops,
missing or foreign stats, malformed and negative socket observations, kill
races and a missing container. Log cases preserve a replacement session after
a delayed old close, reset only on a parsed startup message, retain legacy
logs and refuse incomplete, nonfinite or negative retirement outcomes.
The race log SHA-256 is
a1c9b0f2fc7f12ff2fe3a7e9a64103c718cfa7bf12d1dc940e0b5178fe08dd43.
The embedded Python observer is byte-identical to the native Linux prototype,
SHA-256 d2b250589d2c217a5599d398652de8a0e7bac781d0318d245f22a330d2a1d938.
These results qualify the fixture helpers, not the pending installed
500-agent lifecycle or a capacity claim.

No dependency, model or dataset is added. Docker/Moby uses
[Apache-2.0](https://github.com/moby/moby/blob/master/LICENSE), and the
existing Python interpreter and standard library use
[PSF License Version 2](https://docs.python.org/3.12/license.html).
Both allow commercial and offline use and redistribution with their
applicable notices. Reading the kernel's documented interface does not
copy kernel implementation code into ownsolid.

## Observed retirement timing

Native HTTP transport tests exposed a measurement error: the retirement event
used the scheduled jitter duration as `age_seconds`. An early process shutdown
therefore appeared approximately 55 seconds old even when it ended immediately.
That value establishes a chosen deadline, not elapsed connection life.

Capture the actual drain start after the select chooses its reason, immediately
before public `Server.Shutdown`, and capture completion before logging. Keep
`scheduled_age_seconds` separate from `drain_started_age_seconds` and actual
`age_seconds`; retain UTC timestamps for correlation. Compute elapsed durations
before converting timestamps to UTC. Go's
[monotonic clock semantics](https://pkg.go.dev/time#hdr-Monotonic_Clocks)
preserve elapsed measurements across wall-clock adjustments when both times
retain their monotonic readings. The public
[shutdown contract](https://pkg.go.dev/net/http#Server.Shutdown) separately waits
for active requests within its context budget. A completed connection can thus
be older than its scheduled drain while an admitted exchange finishes.

Keep the installed test's original planned jitter range and forced-close
refusal. Require complete, finite observed ages, a drain at or after the
scheduled age for automatic retirement, and completion at or after drain start.
Retain the existing real wait bound, new transport identities, in-flight task,
cursor, rebalance and resource-observer assertions. Log actual elapsed values;
do not relabel a planned value as an observation or loosen the rollout bounds.
Older log records can still be parsed for ordering, but cannot satisfy the new
installed timing evidence. Test early process stop, delayed automatic drain,
and missing or inconsistent observed fields. No wire/lease/cursor or old-agent
behavior changes, dependency, model or external data are added; the existing
Go BSD-3-Clause license applies.

The native command on the cached qualified transport input passed 38 HTTP
transport roots in 7.160 seconds under the race detector. Its combined Go
list/build/test wrapper exhausted its 180-second wall budget with no gateway
package result or test panic. The gateway suite remains unqualified. Separate
cached native binary compilation from its unchanged 90-second test limit and
retain the first timeout and successful HTTP evidence. Installed 500-node
lifecycle and capacity acceptance remain open.

The measured-timing source eea738bfc11d74f8505eee41c3a3ce0d9fa5cf7b
passed the complete HTTP transport and gateway race suites on Darwin arm64:
39 and 17 roots, in 7.563 and 5.277 seconds. The three pure lifecycle-log
and observer roots passed under race in 1.453 seconds, both vet profiles
passed, and the complete e2e package compiled for Linux amd64. The transport
and e2e race log SHA-256 values are
a0e065586d9c9be9877de5da8738ca53c8917814edacb2ae1d424746755be18e
and ea82e3ebb9ec803fd36bf30181e08e7eff5c257e77a7c0a95abf859942ddea87.

The same full source snapshot then passed native Linux amd64 race tests
in a network namespace with only loopback: all 39 transport and 17 gateway
roots, without skips or failures. All 4,995 snapshot paths and 144 changed
paths were verified against the retained base and current Git bytes/modes.
The original two resource locks, two-CPU limit, free-memory and I/O gates
were retained. Compilation took 10.698 and 8.742 seconds respectively;
test execution kept its 90-second limit. No database or container was
started. The native log SHA-256 values are
b9be9875c1fcf285c4932d58b5e041d06d30b3cd82e22e634f1d4f3d047de729
and d434dbbdfb895f9894d448f4b5cd87ae9e4b83cb3a42d79a13981df277fdb742.

The early-stop native sample reports a scheduled age of 58.815477551 seconds,
an actual drain start at 0.010777572 seconds and actual completion at
0.012410197 seconds. This confirms the repaired observation semantics.
The later rebase preserves all transport, gateway, Agent protocol/client,
simulator and e2e source paths byte for byte. Full integrated repository
and installed 500-agent lifecycle qualification remain separate. No new
fleet-capacity result or completed ARC-107 claim follows from these unit runs.
