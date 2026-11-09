# GW-108 connection circuit and replay boundary

The gateway process knows one loopback engine address. Control admission
selects the node, deployment revision and backend model. The contract
carries no set of authorized alternate endpoints.

[Azure's circuit breaker pattern](https://learn.microsoft.com/en-us/azure/architecture/patterns/circuit-breaker)
bounds the recovery traffic by admitting a probe after a cooldown.
[Envoy's router](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/router_filter.html)
distinguishes connection failures, HTTP responses and failures after
a stream starts. [RFC 9110 section 9.2.2](https://www.rfc-editor.org/rfc/rfc9110.html#section-9.2.2)
sets the retry boundary for non-idempotent requests. The pinned
[reqwest error API](https://docs.rs/reqwest/0.13.5/reqwest/struct.Error.html#method.is_connect)
exposes connection failures separately from HTTP status and other
transport errors.

Use a process-local circuit on the configured address for the first
backend operation, model discovery or tokenization. An observed
connection failure opens the circuit for the configured connection
timeout. New requests receive the existing upstream-unavailable error
during that interval. Afterward, allow one caller to probe. Receiving
HTTP headers proves the address is reachable, and the request still
has to pass the usual status, content and identity checks. A failed
or cancelled probe renews the cooldown. Old in-flight attempts cannot
overwrite a newer circuit state. Normal HTTP refusals and read
timeouts keep their current request outcomes.

The completion POST remains a single attempt on the configured address.
The preflight circuit runs before a chat reserves its token budget.
After tokenization, a request that already passed preflight follows
its original deadline through generation and outcome publication.
A disconnect after output starts ends that call with its own outcome,
without a second generation attempt. An HTTP refusal also ends that call.

A static fallback list would choose an endpoint outside the current
authorization. An HTTP 5xx or a read timeout cannot prove that the
model did no work, so neither is a signal to replay a generation.
Opening a circuit on HTTP overload would also block unrelated accepted
requests to an engine that is still reachable. The cooldown reuses
the existing connect limit and requires no configuration change.

Acceptance for this slice uses two separate local protocol backends.
A refused connection opens only its own circuit, which blocks a
fresh preflight even if that port has just begun listening. A separate
healthy backend still answers. An explicit HTTP refusal and a stream
interrupted after text starts each produce one dispatch, with the
second backend untouched. A deterministic state test covers a single
recovery probe, probe cancellation and stale failure notifications.

The existing reqwest dependency is MIT or Apache-2.0. The circuit uses
the Rust standard library and the existing Tokio and Axum test stack
(MIT). Those terms permit commercial use and offline redistribution
with their required notices. No new dependency, model or dataset is
added. Replica selection and cross-target failover remain dependent on
control-owned topology and the gateway entrance.

## Verified Linux behavior

The Rust suite passed in a loopback-only Linux amd64 network namespace.
A real TCP refusal opened the preflight circuit. The test then made the
same port listen and verified that the circuit rejected the next request
without a TCP accept. A separate local HTTP backend continued to serve.
In the TLS gateway fixture, an HTTP refusal and a connection interruption
after the first text event each produced one generation dispatch. The
other local backend received neither request. The interrupted call
recorded output started, retained unknown usage, and emitted its error
without a final usage event or DONE.

```sh
cd services/gateway
cargo test --locked --offline
cargo clippy --locked --offline --all-targets -- -D warnings
```
