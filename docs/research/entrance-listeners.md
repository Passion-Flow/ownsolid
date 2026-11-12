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
