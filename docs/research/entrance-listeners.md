# Entrance listener isolation

Research for ARC-110, reviewed on 2026-11-13. The default remains distinct
hostnames on one TLS browser socket, as decided in
[ADR 0027](../decisions/0027-entrance-addresses.md).

## Proven mechanisms

Go's [net.Listen](https://pkg.go.dev/net@go1.27.3#Listen) binds a socket separately
from the advertised application origin. Its
[HTTP server shutdown](https://pkg.go.dev/net/http@go1.27.3#Server.Shutdown)
requires joining shutdown after Serve returns; a deadline requires explicit
handling. Retain the existing prebound listener group, common connection budget,
sibling cancellation and bounded concurrent shutdown. Bind every selected socket
before starting business workers or accepting requests; a later bind failure must
release every earlier owned socket.

[Kubernetes API server configuration](https://kubernetes.io/docs/reference/command-line-tools-reference/kube-apiserver/)
separates bind address, secure port and advertised address. This is a comparable
infrastructure pattern, not a dependency or a reason to copy Kubernetes defaults.
[RFC 6265 section 8.5](https://www.rfc-editor.org/rfc/rfc6265#section-8.5)
establishes that ports do not isolate cookies. A dedicated browser socket must
therefore preserve origin, audience, cookie and CSRF checks. Binding two ports
alone is not an authorization boundary.

## Selected approach

Add optional server.portal_listen_address. Omission preserves the current shared
Admin/Portal browser listener byte-for-byte in configuration meaning. When set,
server.listen_address serves only Admin and the new bind serves only Portal.
Both continue to use the selected browser transport policy and certificate pair;
that pair must cover both declared browser origins. Addresses and origins remain
independent. Do not infer a public hostname or port from a local socket.

The existing optional machine_server remains a third control-process socket.
Agent gateway replicas already have their own typed bind. The model gateway is
an independent Rust process with its own configuration; changing its code,
installer placement or firewall configuration is outside this increment.
A complete four-entrance deployment still requires their separately verified
composition. This increment must not claim full ARC-110 delivery by itself.

At each split browser listener, validate the original request with the existing
browser transport resolver, then require that listener's audience before passing
the request to the unchanged identity dispatcher. A valid Portal Host/SNI on the
Admin socket must be refused, including health, assets and runtime configuration.
Retain the shared identity handler and request admission; do not duplicate the
business graph or multiply its connection budget. Rejecting a request must emit
the existing redacted transport audit and correlated public problem response.

Extend the existing explicit protected environment mapping and serving-intent
comparison together. The version-1 expectation may add the optional exact public
portal_listen_address field. Old eight-field expectations assert its absence;
they must reject a configuration that selects a split listener. An older binary
rejects the new configuration/expectation field, so deploy that configuration only
after upgrading the serving binary. No agent wire or stored identity changes.
Old v1 agents and existing single-listener configurations retain their behavior.

Reject exact configured bind collisions structurally, including machine and
separate agent-gateway binds. Actual prebinding remains the authority for wildcard,
DNS and address aliases that differ as strings. An explicit empty/null/invalid
Portal bind is refused; use omission in a new valid snapshot to return to shared
serving. Ambient environment never selects a listener.

Rejected alternatives: another service/process would duplicate workers and
installation ownership; redirecting the wrong socket would expose a listener that
operators intended to firewall; changing Host or manufacturing TLS/context would
bypass the existing authority checks; inferring origins from binds would break
proxy and advertised-address configurations; independent per-listener budgets
would multiply the configured aggregate connection limit.

## Dependencies and offline behavior

No new dependency, model or dataset. Existing Go is BSD-3-Clause, google/uuid is
BSD-3-Clause and go.yaml.in/yaml/v3 is MIT/Apache-2.0; their repository notices
permit commercial/offline use and redistribution with the applicable notices.
See [Go](https://go.dev/LICENSE), [uuid](https://github.com/google/uuid/blob/master/LICENSE)
and [YAML](https://github.com/yaml/go-yaml/blob/main/LICENSE). No Kubernetes code
is imported. Serving, TLS verification, startup validation and shutdown use only
local configuration/material and intranet sockets, with no runtime network fetch.

## Testable completion

- Full typed YAML and protected environment selection, invalid/null/duplicate and
  collision refusals, omission compatibility and exact serving-intent matching.
- Actual verified TLS and HTTP listeners admit only their own browser authority;
  wrong-port Host/SNI, assets, runtime config, health and forwarding confusion are
  refused before the handler. Existing cookie/audience/CSRF authorization remains.
- Three sockets share one connection budget. Occupied/aliased second or third
  binds release all owned sockets. Sibling failure cancels every active request;
  cancellation joins every server and bounded shutdown failure stays a failure.
- Real PostgreSQL production composition signs into both browser entrances,
  preserves sessions/state after restart and refuses cross-entrance requests;
  machine routes retain their existing dispatcher and permission boundary.
- Complete check/build/test passes. Installed native and real browser evidence
  is recorded only when run. No simulator capacity claim follows from listeners.

ARC-110 remains unticked until its complete UI, operator and four-entrance
installed path meets the repository definition of done.

## Cross-platform wildcard overlap

The first local race run exposed that an already listening loopback socket does
not necessarily prevent a wildcard bind on the same port on macOS. The pinned
Go BSD [socket implementation](https://github.com/golang/go/blob/go1.27.3/src/net/sockopt_bsd.go)
enables address reuse for TCP listeners. Kernel bind refusal alone is therefore not a portable proof that
selected entrance sockets do not overlap. The initial external-socket alias
fixture and its failing log remain retained; do not claim that unrelated sockets
can always be detected as conflicts by a portable TCP bind.

Compare the actual TCP addresses of the listener group's owned sockets after
binding each new socket and before serving. Refuse equal bound IP/port or a
wildcard covering the same port, close the new socket and release earlier owned
sockets through the existing failure path. This also covers DNS names resolving
to the same bound address, without DNS during structural configuration validation.
Only inspect sockets owned by this group; do not enumerate or close foreign
processes. Test overlap between the selected group endpoints on both platforms,
while retaining separate exact-occupied-socket and foreign-ownership assertions.

## Exact optional intent keys

Go's [JSON struct decoding](https://pkg.go.dev/encoding/json@go1.27.3#Unmarshal)
accepts case-insensitive field matches. The existing strict token walk rejects
duplicates/null and uses DisallowUnknownFields, but that alone does not forbid a
case-folded known field. Required intent keys already have exact raw-map presence
checks. Extend that same exactness to the optional Portal bind: a ninth field
must be the canonical portal_listen_address key, never an alternate spelling.
Preserve the eight-field omission contract. The added uppercase-key regression
must fail on the retained pre-fix parser overlay and pass on the corrected parser;
no valid deployment value, error contract or statement assertion is relaxed.


## Current source qualification and canonical fixture routes

Source fe11bdf09c36129ad85c7eac3d075e3d53fe2655 passed make check, make build
and make test with the pinned offline toolchains. Console passed 1134 browser
cases and Portal 410; the existing 28 and 10 environment-dependent browser cases
remained skipped, not installed acceptance. The three transport/configuration/
serving-intent packages passed 200 test roots with race detection and no skips,
plus vet and Linux amd64 compilation. The uppercase optional-key regression
failed on the retained previous parser and passed on the corrected implementation.
The full check/build/test log SHA-256 values are respectively
a687cb0cddab2a703a930a622be9b76aba98e4c4dec0fb4e462764a7bf8269d3,
0e54550afb7756d294a446b76a77ee5a85565989d290cb5a371a92c86a848ad6,
and 05f1655389c7568e84c47e5729391e81a696bbd560f91b8a0a69989773fd9875.

The native composition admitted both browser logins and wrong-socket refusals,
but failed the node-join request with RESOURCE_NOT_FOUND before enrollment. Its
new helper had reused an historical composition helper's cell-relative URL.
[The actual cell router](../../services/control/internal/platform/cellroute/route.go)
and [ADR 0006](../decisions/0006-cell-and-site-scope.md) require a public
site/cell prefix for node-owned resources. The established
[HTTP fixture](../../services/control/internal/testing/httpfixture/client.go)
already uses cellroute.Public before constructing requests. Use the same public
path construction in the new entrance fixture; do not add an unscoped route,
change a production dispatcher or weaken the expected status. Keep explicit
unscoped and foreign-scope refusals so the fix cannot hide routing errors.

The second transport subcase also failed isolated-schema migration with a
statement timeout after the first case's disposal. Preserve that failure and its
log; give each subcase its own generated schema namespace while retaining the
same migration, statement and setup deadlines. Require both complete cases in a
fresh native run before claiming composition acceptance. The retained failed
composition log SHA-256 is
0a0361cfef064ca8312b5038bbb15eb4b6b0f626956865464bb66f0fdfd3be18.
Its network-none container and protected database/password inputs were removed.
No complete installed, actual-browser or scale claim follows from this run.


## Qualified production server composition

Source 274d771c8d3a97e3163d3dcb3b21630fccfac57f passed
TestActualSplitBrowserThreeIngressServerComposition on native Linux amd64 in
8.61 seconds without skips: direct_https 3.08 seconds and explicit browser http
5.53 seconds. Each subcase owned a separate generated schema on actual PostgreSQL
18.6/vector 0.8.7. Normal serving startup used its own non-superuser runtime LOGIN,
with private audit-head rights explicitly removed from the historical fixture.
The unchanged production catalog and readiness predicates remained enforced.

Both cases actually signed in through the Admin and Portal sockets, rejected
cross-audience cookies, refused the sibling Host/SNI on assets/runtime/health/API
paths, created the scoped join token, enrolled a CSR and used its returned client
certificate for the machine heartbeat. Correct credentials without a certificate
were refused. Unscoped and foreign-cell machine paths were refused, as were
machine paths on either browser socket. The heartbeat's complete stored facts and
last-seen time matched its returned committed state.

After actual server shutdown/reopen, both issued browser sessions and the same
certified node remained usable. Each subcase joined both server lifetimes' workers,
released all three sockets and reacquired the installation lease. There were two
completion markers and four worker-join markers; no failed test was automatically
retried. The native source receiver checked all 5040 tracked paths' bytes/types
and executable modes before compiling with the pinned Go 1.27.3 toolchain.

The admitted run held both Linux resource locks with MemFree 20582440 KiB,
MemAvailable 22379924 KiB and full I/O PSI avg10 0.62. The pinned container used
network=none, no published port, two CPUs and 1 GiB. Its database used disposable
loopback trust for the historical runtime-role fixture; this is not PostgreSQL
SCRAM/TLS qualification. Browser HTTPS and machine mTLS used actual generated
local trust and normal chain/name verification. HTML, node facts and License
claims were explicitly synthetic; no GPU, physical Agent, installed release or
React browser flow is claimed by this composition test.

The 392282-byte verified source delta SHA-256 is
9e9c633f32d08a0013ca802e7865294dc9322e17bebe73dd91d5cbcc7a0fd1c0.
The native binary SHA-256 is
f13b65a72e1ea5c951502c3d92d9312a9f89c73ab4f1c9275b4aeaa72a88fe6f;
the actual composition log SHA-256 is
5bb7ba346ca195b00c6e073f9448ebb5c47dde6d9922f6532a4a052931cc56d6.
Container/volume removal returned zero, and the container, protected DSN and
password were confirmed absent. Prior failed inputs and logs remain retained.

Between the full fe11bdf0 check/build/test gate and this native input, the only
changes were this research note and the tagged composition test; product runtime,
contracts, dependencies and ordinary test inputs were identical. The corrected
input additionally passed make check and tagged server vet. The transport and
serving-intent race evidence therefore still applies to exactly the same runtime.
ARC-110 stays unticked pending its complete four-entrance installed/operator and
actual-browser acceptance. This increment establishes no fleet capacity result.
