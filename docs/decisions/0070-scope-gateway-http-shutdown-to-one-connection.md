# Scope gateway HTTP shutdown to one connection

Status: accepted for ARC-107 implementation; production acceptance remains open.

## Context

The dedicated Agent gateway serves gRPC through net/http and keeps its
existing HTTP health paths. An application age status and socket deadline
can end a stream, but an established response cannot acquire a new response
header to ask the HTTP/2 transport for GOAWAY. The earlier transport check
only exercises a new request after age.

## Decision

Give each accepted dedicated gateway connection one standard http.Server
owner and a listener that yields that connection once. Closing the listener
ends acceptance; the HTTP server retains ownership of the accepted socket.
Retain the common connection budget, verified TLS, request policy, jittered
drain instant and bounded socket deadlines.

At connection age, call that server's public Shutdown with the configured
exchange grace. Go owns GOAWAY and frame serialization. Active exchanges
keep their original contexts and may finish. Force-close an owner whose
grace expires. Peer closure cancels its timer, and process shutdown joins
all remaining owners within the existing process shutdown budget.

Keep v1 messages, authentication, persisted acknowledgement cursors,
lease handling, advertised origin and health paths unchanged. Shared
browser/machine listeners retain their current process owner.

## Consequences

Each live connection has a small additional HTTP owner and shutdown state.
Measure that overhead with the simulator before making a fleet-scale claim.
The lifecycle must release the global connection slot exactly once and
join its goroutines under peer, age and process-close races.

A global server shutdown would retire unrelated connections. Hand-written
GOAWAY frames or access to Go internal types would bypass the transport's
ownership. A native gRPC listener would require a separate HTTP health
endpoint or a protocol multiplexer. Those changes are unnecessary here.

The [research and acceptance requirements](../research/agent-gateway-rebalance.md#automatic-goaway-for-an-established-stream)
link the primary Go and gRPC contracts. No runtime dependency is added.
