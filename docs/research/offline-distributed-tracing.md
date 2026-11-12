# Offline distributed tracing (OBS-103)

Source review: 2026-11-12. This is a research and prototype boundary, not a
selected trace backend, installed exporter or completed product capability.

## Current product boundary

[Gateway correlation](gateway-request-tracing.md) uses an original server UUID
through the engine and authorized request history. It does not export distributed
spans. Keep that identity intact. [ADR 0009](../decisions/0009-telemetry-and-log-stores.md)
and [ADR 0012](../decisions/0012-telemetry-store-boundary.md) still select
VictoriaMetrics and VictoriaLogs for metrics and logs outside the control
database. Do not reinterpret their existing endpoints as a trace service or put
span payloads in PostgreSQL.

The current reviewed control Go module and gateway Cargo manifest contain no
OpenTelemetry SDK dependency. Instrumentation, authenticated propagation, local
collection, retained trace storage, scoped query and the Console flow all remain
necessary for OBS-103. A trace receiver alone completes none of those flows.

## Proven upstream approach

[W3C Trace Context](https://www.w3.org/TR/2021/REC-trace-context-1-20211123/)
standardizes traceparent/tracestate and permits restarting a trace at an untrusted
boundary. It also identifies correlation and resource-exhaustion risks from
untrusted headers. For the first prototype, create a new internal trace at Admin,
Portal and model-client entrances. Ignore caller sampling flags, clear tracestate
and accept no baggage. Keep request authorization and tenant/Workspace scope in
the original authenticated context. Only authenticated internal handoffs can
continue that context; a trace identifier never supplies permission.

[OTLP 1.11.1](https://opentelemetry.io/docs/specs/otlp/) distinguishes complete
acceptance, partial acceptance and retryable failure. Partial success is not
retried. OTLP/HTTP retries apply to 429, 502, 503 and 504 with bounded backoff;
400 is permanent. Lost acknowledgements can result in replay, so span transport
does not provide exactly-once business execution. Receiver replies, including
decompressed replies, need a size limit.

[Collector resilience](https://opentelemetry.io/docs/collector/resiliency/)
uses bounded sending queues, retry deadlines and optional file-backed queues.
Queue exhaustion, retry expiry, process failure and full storage can still lose
data. [Jaeger 2.23](https://www.jaegertracing.io/docs/2.23/architecture/) separates
collection, storage and authorized query concerns. Its memory-only deployment
loses traces on restart; its single-instance embedded store does not establish
a scalable backend. These primary sources support a private OTLP collection
boundary, not an unmeasured storage or durability claim.

## SDK defaults that need qualification

The reviewed [Go SDK 1.47.0 batch processor](https://github.com/open-telemetry/opentelemetry-go/blob/v1.47.0/sdk/trace/batch_span_processor.go)
has a bounded queue and drops on overflow unless blocking is selected. Blocking
can stall application work. It also reads ambient SDK settings before explicit
options. Use explicit validated queue, batch and deadline options; do not enable
blocking or let process environment override deployment policy.

The [1.47.0 HTTP exporter](https://github.com/open-telemetry/opentelemetry-go/blob/v1.47.0/exporters/otlp/otlptrace/otlptracehttp/client.go)
uses ProxyFromEnvironment in its default transport and can include a peer's
response body and URL in returned errors. Therefore its default construction
and default error reporting do not satisfy ownsolid's offline/secret boundary.
The [exporter options](https://github.com/open-telemetry/opentelemetry-go/blob/v1.47.0/exporters/otlp/otlptrace/otlptracehttp/options.go)
provide WithHTTPClient, which supersedes the SDK's TLS, proxy and timeout options.
The supplied client must therefore own all three explicitly. Configure a positive
serialized request cap and the decompressed reply cap as well. Prototype that
client with private destination admission, CA verification, no proxy, no redirects
and bounded replies. Export failure
reporting must use bounded stable codes without echoing collector bodies,
addresses with credentials or span attributes. Do not copy that SDK into the
product until this path has executed against real TLS and failure receivers.

[OpenTelemetry privacy guidance](https://opentelemetry.io/docs/security/handling-sensitive-data/)
places responsibility for emitted attributes on the integrator. Use an allowlist
of server-resolved scope, bounded operation/route classes, request identity and
stable outcomes. Exclude prompts, completions, documents, job arguments, SQL text,
URL queries, cookies, Authorization headers and raw exception text. Trace and
request IDs belong in spans or authorized correlation fields, never metric
labels. A collector processor cannot repair a secret already exported by a
producer.

## Prototype and acceptance

Begin outside product code with a synthetic authenticated Go request and a
private TLS OTLP/HTTP receiver. No real model, prompt or customer data is needed.
The following must be observed before implementing the owning module path:

- Caller trace IDs, sampled flags and baggage cannot choose internal identity,
  authorize a read or cross a tenant/Workspace boundary.
- The original request UUID remains correlated with internal spans. Cancellation,
  a streaming response and a failed job retain truthful outcomes.
- A stopped receiver and full queue do not block the application. Bounded drops,
  retry expiry, partial acceptance and shutdown loss have explicit diagnostics.
  Partial acceptance is not replayed; permanent failures are not retried.
- A bad CA, redirect, poisoned proxy environment and oversized compressed reply
  are refused without contacting another origin or logging the peer's text.
- Restart and recovery preserve only the durability the chosen queue actually
  provides. Queue bytes, permissions, full-disk refusal and cleanup are measured.
- The disconnected Linux fixture records egress and leaves no collector, receiver
  or workload running after the test.

Gateway, workflow and job owners must then qualify their real versioned handoffs,
including asynchronous links and retries, against the same context contract.
OBS owns the private collection/query boundaries; shared entrypoints, gateway,
job contracts, installer and UI remain with their original owners. No production
instrumentation or new contract is implemented by this note.

## Ten-thousand-node qualification

Do not reuse the seventeen-million metric-series benchmark as trace evidence.
For N producers, admitted sampled span rate r and observed encoded bytes b per
span, ingress is N*r*b bytes/second. Uncompressed one-day payload is
N*r*b*86400; indexing, compression, replicas, queue WAL and retention must be
measured separately. At N=10000, r=1 and an assumed b=1024 this arithmetic is
10,240,000 bytes/second and 884,736,000,000 bytes/day. Those assumptions are a
fixture input, not measured footprint or a recommended default.

Measure an encoded span-size distribution, sampled rates, bounded queue RSS and
disk, persisted backend bytes after shutdown, and query first-read/warm p50/p95/p99
for one authorized trace and a bounded time/scope search at 10,000 simulated
producers. Include receiver outage, restart and retry traffic. Record topology,
retention, caps and rejected work. Trace storage selection, offline packaging and
a scoped Console query must be proven before OBS-103 is ticked.

## Licenses and open dependencies

This research adds no package, image, model or dataset. Source-review references
are pinned candidates, not downloaded artifacts or approved dependency locks:

- [OpenTelemetry Go 1.47.0 license](https://github.com/open-telemetry/opentelemetry-go/blob/v1.47.0/LICENSE):
  Apache-2.0 with the additional Go BSD notice included in that file.
- [OpenTelemetry Collector 0.165.0 license](https://github.com/open-telemetry/opentelemetry-collector/blob/v0.165.0/LICENSE):
  Apache-2.0.
- [Jaeger 2.23.0 license](https://github.com/jaegertracing/jaeger/blob/v2.23.0/LICENSE):
  Apache-2.0; comparison only, no backend selected.
- [Go runtime license](https://go.dev/LICENSE): BSD-3-Clause.

These software terms permit commercial use, offline operation and redistribution
with the required licenses, notices and modification notices. Before a prototype
binary or product dependency is admitted, inventory its complete transitive
licenses, verify the exact offline artifacts and record their digests. Rust and
Python SDK selection, if needed by the owning components, needs the same review.
The W3C document's permissive document license is separate from software licensing;
do not copy a specification into a binary as a dependency.

Reject a cloud tracing dependency, raw span tables in the control database,
unbounded export queues, synchronous export on user requests, trusting caller
scope/sampling, or assuming a debug receiver supplies durable multi-tenant query.
A production backend decision needs its own ADR and native profile.
