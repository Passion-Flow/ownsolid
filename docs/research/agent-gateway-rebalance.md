# Agent gateway rebalancing

Current acceptance: the dedicated gateway path passed the unchanged native
500-agent lifecycle test on source `821227ea5b94a8357f695c290e78af3b81c54f22`,
including strict terminal 500/500 streams and final persisted cursors. Complete
repository check/build/test also passed that source. The earlier 497/500 run
remains a failed historical input; see [current measured acceptance](#authenticated-handoff-installed-acceptance).
Earlier results below apply only to their stated input and fixture scope.

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
[ADR 0071](../decisions/0071-scope-gateway-http-shutdown-to-one-connection.md).

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


## Complete repository gate and installed input

The integrated source 129573ae8977cce83c1e81d9d386e45c1a176c3b, tree
0ff7eff5700397de8be28342adb797fd4f9506e5, passed `make check`, `make build`
and `make test` on Darwin arm64 from 2026-11-12 17:59:17 to 18:17:25 UTC.
Console passed 1,112 browser cases and Portal 410; 28 and 10 existing live-fixture
cases remained skipped, with zero retries. The Go, Rust and web unit suites
also passed. The complete check, build and test log SHA-256 values are
2de133be71bc1be09d156b43eb16a28d1655b3c21875d24259bcea8552d02b1f,
292814dcb86f4c34031711636f7994f21df23a432e5a9ee1dcbb77492d9186ca
and fd3472bf61412d4103b0724eccf94cda26371823b2d35bd1ff5b4fca7e69d37b.
The regular-file workstation lock, two-CPU compiler limits, offline dependency
cache, private APFS temporary volume and original assertion bounds were retained.
The temporary volume was detached and its mount directory removed.

A separate native Linux amd64 build verifies every byte and executable mode of
all 5,014 source paths for that same snapshot before compiling the installer,
agent, simulator, egress recorder, both existing e2e binaries and control. Control
SHA-256 is 4440b3b63ec5316d380aff58c509f7186f008e24bf2df701041dc6b462a3a655;
simulator SHA-256 is c4479bb013e20235415ccd412452d059b39861ef52afdb4acaf67d324f6a23ca.
The fixture image combines these exact native control bytes, the complete
qualified Mac web build and the pinned Python runtime base. This exercises the
installed runtime, not the standard release-image build factory. The fixture
uses no host port, a network-none outer Docker daemon, two CPUs and a 6 GiB
memory ceiling. Preserve named container logs and routes before owned cleanup,
and send the existing TEST-NET recorder probe before accepting zero egress.
The 500-agent installed run is separate and remains unqualified until its actual
result, task/cursor checks and sampled resource observations are recorded.


The first installed attempt reached schema migration, runtime-principal preparation
and schema validation, then refused administrator bootstrap with BLOCKLIST_MISSING.
No simulator agent was enrolled. The fixture assembler's private umask had left
its copied password blocklist at mode 0600; the non-root image user could not read
it. Preserve that failed run and its one captured recorder probe. Correct only
the fixture's public resource modes: blocklist 0644, frontend directories 0755 and
assets 0644. Before retrying, run the exact image as UID 10001 on network none and
require readable control, blocklist and both frontend indexes. Reuse the unchanged
qualified source, binaries and web artifact. No product security check or lifecycle
assertion is relaxed. A subsequent low-free-memory preflight returned 75 before
creating a container; the retry retained both original resource locks and limits.


## Production default log format

The installed 500-agent run exposed an observation-fixture defect: raw gateway
logs use Go's default slog handler, whose event message follows the standard-log
date, time and level without a `msg=` key. The parser accepted JSON and explicit
text-handler keys only. All 500 agents enrolled and connected, and raw logs show
actual automatic retirement, but that parser cannot establish the original
assignment or qualify the rollout. Preserve the failed installed result.

The pinned Go source in `src/log/slog/handler.go` writes the record level, then
its message, then attributes through the default log.Logger. The
[standard logger](https://pkg.go.dev/log#pkg-constants) supplies the default date
and time, and [slog](https://pkg.go.dev/log/slog#hdr-Handlers) distinguishes the
default handler from TextHandler/JSONHandler. Parse only the actual default
prefix and a complete event-name token, plus the two previously accepted formats.
Do not search arbitrary attribute text for an event name. Reproduce the installed
lines, including replacement-after-old-close, restart and observed retirement,
and refuse unrelated messages or attributes containing those same event words.
This changes the test observer only; transport, protocol, bounds and task/cursor
assertions remain unchanged. The existing Go BSD-3-Clause license applies.


## Busy-agent reconnect boundary

The corrected parser's installed run on 59c9c86a enrolled 500 agents in 2 seconds,
connected every agent in 11 seconds and established 261/239 original sessions with
25 acknowledged tasks. All 500 original transports retired without force. Gateway
A's observed drain age was 54.002745–59.996931 seconds and completion 54.003984–68.886238;
B's was 54.048568–59.963101 and 54.378634–68.933708. Maximum observed scheduling delay
was 0.003435 and 0.001201 seconds respectively. No external attempt was recorded;
one harness probe was seen. The run then failed the in-flight assertion after
waiting 100.7 seconds for every replacement session. Retain this failure; it is
not a completed lifecycle result. Its test-log SHA-256 is
1e1f24e0bda0c767290f79794bbc71e714079fb597b5feaf1bd3374e66c8fd2d.

The existing production runtime runs one cycle through acknowledged execution
before its next session heartbeat/claim. Its long-task owner renews through HTTPS.
The simulator follows the same serial cycle: work renews through HTTPS and the
next heartbeat reopens its aged session only after execution and result delivery.
Requiring every replacement session before checking that every 100-second task is
still running therefore contradicts the existing execution contract. Increasing
the task duration cannot make that serial condition hold and is rejected.

Keep the 100-second task, all 500 original retirement and replacement assertions,
both 25-task exact-attempt checks, the existing three-minute move and two-minute
dispatch bounds, all 500 survivor/return checks, renewal reporting and cursors.
Check all acknowledged tasks are still running immediately after retirement,
with an unexpired lease as an additional renewal assertion. Then wait for their
successful completion and all replacement sessions within the original absolute
three-minute age deadline. During a gateway kill, require every idle agent to
reach the survivor while all 25 busy tasks retain their current leases. After those
tasks complete, require all 500 agents on the survivor within the original kill
move deadline, before returning the first gateway. Finally require the original
500-node preferred assignments and unchanged successful attempts. This preserves
the real work/transport boundary instead of adding concurrent heartbeats to the
agent solely to satisfy a fixture. No production protocol or execution behavior
changes. The existing Go/gRPC licenses apply and no dependency or data is added.


## Installed lifecycle run with busy-task boundaries

The isolated native Linux amd64 run on 149a9501 exercised 500 individually enrolled
simulated agents through the actual installer, schema 60 PostgreSQL and two gateway
processes. It retained the two-CPU/6 GiB outer fixture, network none, original
three-minute move/two-minute dispatch limits and 100-second work. Its source delta
from 59c9c86a contains only research and two e2e files; the unchanged production
binaries were reused byte for byte after complete source verification. The updated
native e2e race/vet passed before the installed run. The actual selected test
**failed** at its terminal report assertion and is not a completed acceptance run.

| Observed stage | Result |
| --- | --- |
| Enrollment / every agent connected | 500 agents in 2 s / 11 s |
| Original preferred sessions | 258/242; 25 acknowledged tasks |
| All original transports retired | 65.4 s; no original transport forced closed |
| A observed drain / retirement ages | 54.011145–59.981629 s / 54.233425–68.994617s |
| B observed drain / retirement ages | 54.063368–59.980572 s / 55.569327–69.194800s |
| Maximum observed drain scheduling delay A/B | 0.040451 s / 0.026864s |
| First batch / all original transports replaced | 25 successful tasks / 99.4 s |
| Idle agents on surviving gateway after kill | 475 in 26.8 s; 25 busy tasks retained valid leases |
| Second batch / all agents on survivor | 25 successful tasks / 500 agents in 101.3 s |
| Original preferred distribution restored | 258/242 in 44.5 s after return |
| Lease renewals / results | 600 successful renewals, no refused renewal; 50 results |
| Terminal generator report | 497 connected and on stream out of 500; zero revoked |
| Egress |No external attempt; one recorder probe observed |

The last generator window recorded 500/ 500, but the stop-time snapshot recorded 497
while repeated 60-second age cycles continued. The strict 500 terminal assertion
remains unchanged. Its failure occurred before the final persisted-cursor readback;
that final gate was not executed. The earlier lifecycle, in-flight lease and exact
attempt assertions do not substitute for that missing acceptance. Preserve the
complete report, 500-agent protocol logs, raw observer rows and original failure.
Do not lower the threshold, report a successful selected test or tick ARC-107.

There were 122 observer rows at a declared five-second sampling interval, including
23 deliberately stopped A rows. A's sampled maximum was 258 established serving-port
sockets, 90.18 MiB Docker CLI cache-adjusted memory, 30.51 percent CPU and 21 PIDs.
B's was 500 sockets, 165.3 MiB memory, 34.56 percent CPU and 22 PIDs. TIME_WAIT maxima
were 32/43 and are kept separate from live sockets. These are sample maxima from
this functional fixture, not instantaneous peaks, RSS or a 10,000-node capacity
result. They do not prove sustained scale or real hardware behavior.

The selected run log SHA-256 is
053604392a15ab01720727d0b8dbc4bc7aec69ad858f4e415a8bd3f08c3a397d.
The owned outer container, nested database/workloads and staging directory were
cleaned. Shared test-server containers, the previous release dumps and all foreign
resources were left untouched. Gateways still share control's database role
(ARC-111); shared-listener integration also remains outside this dedicated path.


## Integrated repository acceptance

After the audit-owner integration, source 353b46af30c5dba75e3a82b3358a5f712cf3f5ea
passed the complete repository check, build and test gate on Darwin arm64. The
unchanged production transport, Agent protocol/client, simulator and complete e2e
paths are byte-identical to the native 149a9501 installed input. The integrated
full gate also runs the affected e2e race roots and vet before the ordinary gate.
Console passed 1,112 and Portal 410 browser cases, with 38 existing live-fixture
skips and zero retries. No selected installed-test failure is reclassified by this
ordinary gate. The original three-minute bounds, 100-second work and exact terminal
500/500 assertion remain in the committed fixture. ARC-107 remains unticked.

The integrated check, build and test log SHA-256 values are
caae568c7c4e8673413a3f2a1dfd54bb41b01480839767b7a38c19b0381dec36,
ac0bd47fca1353d3affad18d7c08622e0c722d3fdcfb1f0df2e3cc528f7aafcc
and 5747bcdf90495217928272fc7b68b6accddc3928e63d90a7e82a44b784b7308c.
The affected e2e race log SHA-256 is
21ee38b0e59002b7ef59e2a3fd21a047e2cdc9c81f7e443f21629fef74fd70a9.


## Age handoff prototype requirements

The installed 497/500 terminal result remains a failure. Do not change the report
accounting, suppress a known disconnect, stop aging, extend the tasks or relax the
strict installed assertions to hide it. The next experiment addresses an actual
planned-retirement exchange gap: establish an authenticated replacement between
exchanges before the old transport reaches its unchanged age, while preserving
natural retirement and all lease/cursor semantics. This is a prototype candidate,
not an accepted product implementation or a new capacity result.

The implemented [gRPC connection-management design](https://github.com/grpc/proposal/blob/master/A9-server-side-conn-mgt.md)
uses GOAWAY and a grace period to retire transports; it does not promise to migrate
one application's long-lived stream. The existing ownsolid preference probe
already retains the old stream until a candidate heartbeat succeeds. The gateway
already replaces an authenticated same-node session only after its first successful
exchange, and its old cleanup does not remove the replacement. Reuse those
properties; do not create a second task executor or replay an outstanding claim.

[gRPC custom metadata](https://grpc.io/docs/guides/metadata/) can carry optional
ASCII response hints without adding fields to the strict version-1 JSON frames.
Prototype first-reply relative drain/close durations derived only from the owned
socket's actual ConnectionDrainAt/close deadline. Send them only after a successful
authenticated first heartbeat. The server's jittered age, graceful reply window
and hard socket deadline stay unchanged. A request header can never select the
socket identity, its age or the response hint.

Compatibility is explicit: old agents ignore the extra response metadata and
retain their existing reconnect behavior. New agents talking to old servers, a
shared listener without age, or missing/duplicate/malformed/out-of-range hints
retain that same legacy path. No message schema/version, task sequence, cursor,
credential, certificate or persisted agent checkpoint changes. Reject a required
new frame or a simultaneous fleet upgrade. Validate bounded integer durations;
use a conservative local monotonic schedule rather than trusting remote wall time.
A candidate probe is between exchanges, never an automatic claim/result replay.
If it fails or capacity is full, keep the original authenticated path and the
existing single retry owner.

Closing the old gRPC ClientConn immediately would destroy the very natural-age
proof the installed test requires. Keeping an unused ClientConn forever is also
wrong: gRPC may reconnect a channel after its original transport ends. The pinned
[gRPC-Go 1.85.0 stats contract](https://pkg.go.dev/google.golang.org/grpc@v1.85.0/stats#Handler)
provides per-connection TagConn/ConnEnd events. Client RPC contexts do not inherit
TagConn context, so do not assume they do. The pinned source instead attaches the
actual transport's [peer local/remote addresses](https://pkg.go.dev/google.golang.org/grpc@v1.85.0/peer#FromContext)
to ClientStream.Context; obtain them only after the first reply commits the
attempt. An external prototype may bind that actual transport to its lifecycle
record, then retire the unused ClientConn when that transport ends, with a bounded
close-deadline fallback. Never correlate using a caller header or log credentials,
metadata payloads or TLS keys. Keep at most one retiring ClientConn per Session;
missing transport binding must keep the legacy behavior, not create an unbounded
retirement list. Client close must join/resolve its own close callback safely.

Prototype all this outside product code before choosing it. Use real normally
verified loopback TLS/HTTP2 and the production codec/gateway/client through
explicit source overlays, with synthetic backend facts. Require several natural
age cycles without a failed healthy heartbeat/claim; the unchanged server-generated
retirement IDs and ages must match. Delay an admitted lease reply across age, keep
its single lease/cursor and exact acknowledgement, and prove cancellation joins
all listeners/client connections. Also require old-server/new-client and retained
old-client/new-server behavior, invalid metadata, wrong credentials/certificate,
failed candidate, saturated session/connection budget and preference failback.
The existing MaxSessions and common connection budget cannot silently grow to
make a warm replacement pass. A saturated failed probe must leave the current
session usable; this is not a zero-gap guarantee at arbitrary saturation.

Use the existing pinned Go BSD-3-Clause toolchain, gRPC-Go 1.85.0 Apache-2.0,
protobuf-go 1.36.12 BSD-3-Clause and x/net BSD-3-Clause with their retained notices;
commercial use, redistribution with notices and offline execution are allowed.
See [gRPC-Go](https://github.com/grpc/grpc-go/blob/v1.85.0/LICENSE) and
[protobuf-go](https://github.com/protocolbuffers/protobuf-go/blob/v1.36.12/LICENSE).
No new dependency, model or dataset is introduced. Even a passing small prototype
must still be followed by full repository gates and the unchanged native installed
500-agent simulator drill before ARC-107 can be accepted. No such new run is
claimed here.


The external feasibility probe on source `2e4650c7b68feae6dffa64d5505c2eae6c073431` passed
`TestPrototypeAuthenticatedStreamBindsNaturalTransportEnd` under the race detector
on Darwin arm64: three authenticated replacements on actual verified TLS/HTTP2,
4.38 seconds in the root and 5.910 seconds in its package. Each committed stream's
actual peer local/remote addresses matched its own TagConn lifecycle record. Every
retired channel had one transport begin and one end, with no retained transport;
the old stream's cleanup preserved the replacement session. The three original
physical connections retired naturally at their unchanged jittered ages with
`forced=false`. No old ClientConn was closed before its transport end. The final
active connection was closed explicitly during owned fixture shutdown and is not
counted as natural-age evidence. The external test and log SHA-256 values are
`9dcb1da41ab710c39ac97c1c0df54d4a55c9f951533ab759869ffbb045c2cce4` and
`97666a1db004bf1630546e03131951c7715a7afe62a8e43bece3ebfbd5ffab45`.

This qualifies the transport-binding feasibility only. It used the production
listener, gateway and codec with a synthetic backend and a raw pinned gRPC client;
it did not yet change or qualify the real agent's automatic handoff, compatibility,
lease/cursor behavior, saturation or the installed 500-agent drill. All code and
logs remain outside the repository. The prototype released its resource lock and
joined its listener; no service or container remains running.


An external materialized snapshot of `ecc7a3b041ee0ff62e43ba038ccc8ec3c86a98ca`
then passed the six prototype roots (12 including metadata subcases) using the
actual agent Session implementation with candidate source changes, production
listener/gateway/codec, normally verified server TLS and actual node certificates.
There were no skips. The six-second automatic handoff flow observed seven
authenticated physical connections and five unchanged natural retirements, no
healthy heartbeat/claim errors, exactly one acknowledgement on attempt one, no
HTTPS acknowledgement in the healthy flow, and sequence seven on every subsequent
claim. A separate admitted lease reply crossed its actual connection drain time;
it kept attempt one and sequence seven, received one acknowledgement through the
existing HTTPS fallback, and was not claimed again after reconnecting.

The full-session-budget refusal retained the original authenticated stream without
raising MaxSessions. Missing, duplicate, overflowing, negative, noncanonical and
close-before-drain metadata retained the legacy path. Missing node certificates
and wrong credentials could not replace the authenticated session. The retained
raw client also ignored the additive headers while completing its three natural
retirement cycles. The actual-client package run took 23.081 seconds under race;
the unchanged existing agent client suite with the candidate source took 6.464
seconds under race. Both had vet enabled and exit zero. Their log SHA-256 values
are `e99f83734f23a907063fbebca98b75c15e294429a5c96d7d5dfc773a2adb40e0` and
`b28317f3e28475e860b206e2f8ca5b3d22ede0544efd84a2a17f97d58af2cdca`.

The external harness failures remain retained separately: the first run supplied
certificate filenames to UseCertificate, which correctly requires PEM contents,
and a virtual-only bridge directory could not be entered by vet. Materialize the
bridge only in the owned external source snapshot, rather than skipping vet. The
next run's stack trace showed the test log collector recursively forwarding through
slog's default standard-log bridge; use an explicit text handler and restore the
prior standard logger after all owned listeners join. This was a harness deadlock,
not a diagnosed SendHeader defect. The candidate uses SetHeader to attach metadata
to the first real heartbeat reply, avoiding an unnecessary independent header flush.
A subsequent lease fixture returned bare JSON rather than the existing data
envelope and was corrected without changing the product decoder or assertions.
No production source or capacity claim was changed by these experiments.

Before product adoption, also bound a slow candidate probe by the original
transport's remaining healthy time and prove that failed handshakes do not consume
that whole window. Retired channels must remain bounded to one and join on explicit
client shutdown. These small runs do not establish the unchanged installed
500-agent drill; its earlier 497/500 failure remains open.


The slow-candidate experiment reproduced that missing bound on the unchanged
candidate: a deliberately held new TLS handshake consumed the full request budget,
the original socket naturally aged meanwhile, and the next old-stream heartbeat
failed. The retained negative log SHA-256 is
`9d38bd1fec9e71554c804c2b42dada86be1981617f1c042f4bdf4ec9c2652000`.
The corrected probe uses the minimum of the normal request budget, the old stream's
remaining idle window, and half its remaining positive drain window. This leaves
time for the existing healthy stream when a candidate handshake does not complete;
an already expired transport still uses the existing bounded request budget.
It adds no retry, capacity or server-age exception.

The same strict slow-handshake test then passed along with all seven prototype
roots (13 with metadata subcases) under race and vet, in 23.656 seconds on
Darwin arm64, without skips. The healthy flow again retained seven authenticated
transports, five natural retirements, no heartbeat/claim errors, one acknowledgement
and cursor seven. The existing complete agent client suite also passed under race
in 6.212 seconds. Both were ordinary Go package runs in a materialized external
snapshot, with no overlay-only directory or disabled vet. Their log SHA-256 values
are `39dd7ad1b399e0cf08e2993757c7b8574f690518ae0051bad79d5ff7a6ff22e0` and
`f791796b229a282efd17a7f13bf57b5017f0c7d8451037a9665c38a08106bc50`.

For implementation, keep the optional header names and bounded relative-duration
format in the existing versioned Agent contract. Put the actual outgoing gRPC
transport lifecycle in a cohesive public `services/agent/sessiontransport` package:
it belongs to this one Go service, and the development simulator can borrow it to
exercise the same lifecycle rather than copy the map, peer binding and retirement
worker. Do not create a new service or an empty shared module, and do not make the
contract module depend on gRPC. The simulator's dependency on the existing agent
module is repository source, not a new third-party package or runtime download.
Validate that dependency offline before adopting it. Preserve the simulator's
legacy shared-connection mode; certificate-bearing agents already require one
agent per connection, so a legacy multiplexed channel must not be silently replaced
by one node's handoff. No product adoption or new simulator run is claimed here.


## Shared Agent transport qualification

The public Agent transport candidate was exercised from both real implementations
in an external materialized snapshot of `56a75ee89422a4f0e3e42a7dc5e66c73fa3f79f8`.
All nine real-TLS/gRPC prototype roots (15 with metadata subcases) passed under race
and vet in 31.081 seconds. The simulator completed six authenticated handoffs across
seven transports with no healthy heartbeat/claim errors, one acknowledgement,
attempt one and cursor seven. Its legacy two-node shared connection remained one
physical transport with two usable streams. The shared retirement worker tests
cover physical end, explicit and concurrent close/join, deadline expiry, missing
or foreign lifetime, late duplicate end events and concurrent record release.
The actual Agent client, transport helper and Agent contract packages passed under
race in 5.483, 1.529 and 2.436 seconds respectively.

The complete simulator module also passed under race, with fleet, link, report and
configuration packages in 114.087, 2.116, 1.485 and 1.528 seconds. GOPROXY and GOSUMDB
were off and the pinned local toolchain was used throughout. The prototype and
simulator log SHA-256 values are
`f19d0c9c5278d961c7e932527b5f238ffa71a7c9068a865dc757ec65c4b720b5` and
`22f11a9fb742f42110d0c21338dd9905d71f1a2959aa0b01d1cab8c8101681eb`.
This is a small controlled flow and package regression run, not a fleet-scale or
native installed result. Its bridges and synthetic backend remain outside product
source. Preserve the production simulator's existing connection accounting.

A separate simulator link-package compilation with GOWORK=off, GOPROXY=off,
GOSUMDB=off and -mod=readonly passed against the same candidate module declaration.
Its command deliberately selected no tests and is only dependency/build evidence;
log SHA-256 `a5e745bb7a7d080ba849be742cedf3ab3133631dd133daa0cdb40f15e31297b5`.
The earlier full module-graph inventory query with GOWORK=off failed because
nonselected graph metadata was absent from the local offline cache. That query is
not a successful inventory or a failure of the compiled transport; retain its
log SHA-256 `81dcfe45e9636b445d171d21e8c224cc196af9042177c936cb51f77429296109`.
No package version was changed or downloaded to bypass it. Product source still
requires complete repository gates and the unchanged native installed drill.


## Implementation regression qualification

The initial age-first ordering could indefinitely postpone a recovered preferred
gateway: every heartbeat after an old retirement finished could choose another
same-gateway age handoff before checking preference. The new permanent regression
`TestConnectionHandoffDoesNotStarveRecoveredPreferredGateway` reproduced the exact
simultaneously-due case and failed without changing its assertions; negative log
SHA-256 `b972b51ad0aa3363e0d4c60782f6f05dc1bfeeca91db9eff6a8e21df70a9a3de`.
Give an eligible preference probe priority over same-gateway age refresh. Apply
the same positive-drain-window budget to either kind of candidate. With a valid
age binding, a successful preference move also retires its unused old channel
naturally; legacy preference moves retain their prior close behavior. At most one
retirement remains live, and both callers still defer probes while it is pending.

The corrected product code passed race checks for the complete Agent contract,
Agent client, outgoing transport, gateway, HTTP transport, simulator link and report
packages, followed by the complete affected vet profiles including simulator fleet.
Agent client/transport took 6.569/1.510 seconds; gateway/HTTP took 5.213/7.059 seconds;
simulator link/report took 1.598/1.416 seconds. Permanent tests cover valid and
legacy headers, authority ordering, bounded hint metrics, no ACK replay, cursor
preservation, capacity refusal, one retirement, shutdown join, shared channels and
the recovered preference regression. The Agent, control and simulator race logs
have SHA-256 values `20bf03147db7aa23a93e0acaa43a6393580d11ec69906490d1dc87fa1f2f4d75`,
`def85f8e676699eaee2f0c554ec46545b1bc1054c381d196c59bc610b66f2115` and
`d7cd033a731c3297785d7c58abb26fc3eb531ce522a93e2cdf7e1d2f6063be91`.
The sorted Go-source/module-input fingerprint is `12727b32504ae011e1fb46e357c74b17c6453ab3c3c1a0c22de7dff0e57712df`.
Full check/build/test and the unchanged installed native 500-agent drill remain
required. Do not infer acceptance or a new capacity claim from these focused runs.


## Native terminal-status and fixture-directory findings

Input `f086998549595603686e56ec8c2156a2873065c1` passed complete Mac check,
build and test: 1,191 Console and 410 Portal browser cases, with the existing
28/10 environment skips. Native source verification covered all 5,062 tracked
paths, and all seven current Linux binaries compiled without downloads. The first
native client race run failed; its retained log SHA-256 is `fc8b81765b1d9b2f7f7ff3b293da0a9e47a6ceae9b11dafc1ddc741f16d55832`.
No installed 500-agent run was started from that failed prerequisite.

One failure was the runner's working directory. A compiled Go package test must run
from its package directory, as go test does; the model-range test correctly refused
a missing relative fixture when the runner used the module directory. Correct the
external runner's directory, not the fixture path, digest check or assertion.

The other failure was the existing strict revoked-session test receiving
CONTROL_CONNECTION_FAILED on a repeated immediate refusal. The pinned
[gRPC ClientStream contract](https://pkg.go.dev/google.golang.org/grpc@v1.85.0#ClientStream)
and [implementation](https://github.com/grpc/grpc-go/blob/v1.85.0/stream.go)
require reading RecvMsg after a SendMsg EOF to obtain a remotely generated final
status. Treating that EOF as an ordinary network error can lose an early
Unauthenticated status. Add a deterministic sender-EOF/receiver-refusal regression
before changing the implementation. Share one send/receive helper between the
actual Agent and simulator; preserve final refusal codes, one send, one receive,
existing cancellation/timeouts and frame validation. A non-EOF send error remains
final. Do not replay a heartbeat, claim, acknowledgement or result, and do not
classify an empty successful stream termination as an answered application message.
The raw gateway test client's I/O must honor the same upstream rule. The existing
Go BSD-3-Clause and gRPC Apache-2.0 dependencies suffice; add no dependency.


The deterministic regression failed on both revoked-credential and missing-certificate
refusals before the fix, each reporting CONTROL_CONNECTION_FAILED instead of the
final authentication status. Its log SHA-256 is
`160360d4fb77cc23c8bbaeb0e48d625c65226ad70f73faed019aa055360a6936`.
The shared single-send/single-receive helper now reads the final RPC status after
send EOF. The original strict authentication assertions pass, alongside permanent
no-replay, local-send-error, empty-termination and decoded-reply tests. The actual
Agent and simulator adapters both use that helper; the raw gateway fixture observes
the same documented I/O contract without changing any refusal assertion.

After the fix, all affected Agent contract, client/transport, gateway/HTTP and
simulator link/report race profiles and affected vet profiles passed. The Agent,
control and simulator race log SHA-256 values are
`ddaafb6fc230627c18c90bbcc64d96a4454947b35fadabf9558a84865226e21f`,
`8eef3de17d4dfee0164de516d14bfe0f3c6cd354631729a258ab02cfc2b94d93` and
`1f754eabbb4703f7411773e53b252ba9abb0e8e0997887c4cc6855f30294d526`.
Native package execution and installed lifecycle acceptance remain pending. Keep
both original native failures; the earlier full Mac result does not erase them.


## Instance-owned gateway idle baseline

The corrected native client and outgoing transport suites passed on cf93c92e with
proper package working directories. The next native gateway race run exposed an
existing test-only global mutation: TestSlowlyPacedSessionOutlivesTheDefaultIdleTime
restored idleTimeout while an HTTP/2 session handler still read it. Preserve the
strict race report, SHA-256 `342d1d84d19a3cab60ae1d5cd5328f523ca4a668cb20c6b71b302962cc1b9909`.
A successful Mac suite does not erase this native failure.

Replace the mutable package default with the unchanged protocol constant captured
by each Gateway at construction. A private constructor supplies the explicitly
bounded baseline for the existing short-clock fixtures. Production New always
uses the same version-1 default; runtime pacing can still extend the idle window.
Do not add a configuration switch, change the 200 ms fixture clocks or relax the
silence and held-claim assertions. Add an actual two-gateway independence test.
An external source overlay passed all native gateway race tests and vet, including
that test, before adoption. Its archive SHA-256 is
`095997d301860ea4afa816e00826f355ebd475ec1596730b4a5d222ee05d704a`
and race log SHA-256 is
`eb3368dc05d440c28d57bc8017ba004925085ccd46c89c495b7429611da259aa`.
The native race command finished in 5.193 seconds with no skips. No dependency,
wire frame, server age enforcement or retry policy changes.

Complete Mac check/build/test also passed cf93c92e with 1,191/410 browser cases and
the existing 28/10 environment skips. The native fixture race still prevented
installed acceptance at that input. Requalify the adopted immutable baseline and
retain the unchanged installed 500-agent assertions.


## Authenticated handoff installed acceptance

Source `821227ea5b94a8357f695c290e78af3b81c54f22`, tree
`08ac231f728e6eb93a1fcde86838725b00520b7f`, passed complete offline-cache
make check, make build and make test on Darwin arm64. Chromium passed 1,191
Console and 410 Portal cases with the existing 28/10 live-environment skips and
zero retries. The owned private APFS scratch was detached and removed.

Native Linux amd64 qualification verified every byte and executable mode of all
5,068 source paths, compiled all seven actual installation inputs with Go 1.27.3,
and passed complete client, sessiontransport, agentgateway and httpserver race
and vet profiles with no skipped cases. The private network namespace admitted
only loopback. The retained earlier sender-EOF failure and global-idle race are
resolved by this input, without relaxing their assertions or fixture clocks.

The actual installed test then passed in 323.13 seconds, with its original
500 agents, 60-second maximum age, 54–60-second jitter envelope, both 25-task
batches of 100-second acknowledged work, three-minute move bounds, two-minute
dispatch bounds and strict terminal assertions unchanged. The fixture ran the
real installer, schema-60 PostgreSQL and two real dedicated gateway processes,
using individually verified node certificates. The image used freshly compiled
native control bytes and the same-source qualified Mac web assets; the standard
release-image factory is outside this test. The outer isolated Docker daemon had
no host ports, network none, two CPUs, a 6 GiB memory ceiling and 4,096 PID ceiling.

| Measurement | Actual result |
| --- | --- |
| Enrollment / all connected | 3 s / 11 s |
| Original preferred sessions A/B | 244 / 256 |
| Original transports retired naturally | All 500; 56.1 s after the age checkpoint; zero forced |
| A observed drain / retirement age | 54.024153–59.987182 s / 54.025736–59.991372 s |
| B observed drain / retirement age | 54.025462–59.974299 s / 54.026935–59.975899 s |
| Maximum observed drain scheduling delay A/B | 0.007622 / 0.001703 s |
| All original transports replaced | 100.2 s after the age checkpoint, including completion of the busy tasks |
| Idle agents reaching survivor after kill | 475 / 475 in 8.2 s |
| All agents reaching survivor | 500 / 500 in 101.2 s, including completion of the second busy batch |
| Original preferred assignment restored | 244 / 256 in 39 s after readiness |
| Work / ACKs / results | 50 / 50 / 50; all original acknowledged attempt one |
| Lease renewal | 600 successful, zero failed |
| Authenticated age handoff | 3,289 successful, zero failed |
| Final connected / on stream | 500 / 500 |
| Persisted cursors | Final installed readback passed for every simulated node |
| Revoked / resync / lost replies | 0 / 0 / 0 |
| Egress | Zero product attempts; one observed fixture probe |

The intentionally killed gateway produced recorded connection failures: 218
claims, 15 heartbeats, 244 stream opens and 976 failed preference probes. Keep
these totals in the raw simulator report; the passing fault test does not mean
zero network errors or establish a steady-load SLO.

There were 122 observer rows at the unchanged declared five-second interval,
including 23 deliberately stopped A rows. These are sampled maxima, not an
instantaneous peak or RSS:

| Gateway | Established serving-port sockets | TIME_WAIT | Docker CLI cache-adjusted memory | CPU | PIDs |
| --- | --- | --- | --- | --- | --- |
| A | 477 | 24 | 106.2 MiB | 21.86% | 21 |
| B | 932 | 51 | 198.5 MiB | 41.53% | 21 |

The physical overlap is real: authenticated warm replacement and natural
retirement consume temporary headroom inside the existing limits. Neither the
limits nor the simulator connection accounting was increased to accept the run.
No 10,000-node capacity, real accelerator, customer-environment or separate-host
gateway claim follows from this 500-agent functional drill. Connection aging
applies to dedicated serve-agent-gateway listeners; the shared control listener
retains its legacy behavior. The separate training artifact-owner failure and
gateway database role remain ARC-111 work.

The owned outer container, nested database/workloads and installation staging
were removed. Its exact fixture image is retained only as an input to the next
owned ARC-106 measurement; shared images, source snapshots and foreign resources
remain untouched. Raw native logs, simulator JSON, socket observations, route-read diagnostics,
egress and image evidence are retained outside Git.

Full check/build/test log SHA-256 values are
`c418a96622f450b1426dc74cf8dbe283b920c60c736e45d9e3b142ddbe3503df`,
`129dcbcd4df5a0f9304cefa377e3a71be68de2c4782209981ed6e8d9991f6006`,
`39f212e85ea71a24d3eed33f15245d94b882b707e8332c7f380d33cf171d021c`.
The selected installed log SHA-256 is
`5338ca5e650289a30456c48a42bdab800183597b7006b4696c234abb9a7b6447`.
Control and simulator binary SHA-256 values are
`9147131da211b22006605bc39bf1fc660dbbed71331f459a7eacfd0c69fad9a9` and
`9e7d17c095107e4302fbeb942cc7ffa06d7f8f2a529e74ab599b6bf6ffe5d88e`.
The fixture image ID is `sha256:0c57f136cefe12a877997a1477846ffe173912a715b3a8e0b42b6dcfdfde0b46`.
