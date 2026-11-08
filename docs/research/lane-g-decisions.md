# Lane G decisions and boundaries

## GW-106: Enforce scopes from protected JSON

Bind the control-authorized workspace and authenticated key UUID to an
operator-declared tenant and application. Omit the configuration to retain
the existing path. When configured, deny unmapped keys. Extending the Go
admission contract would provide authoritative IDs and automatic enrollment
but crosses lane ownership and breaks compatibility with strict consumers.
Using an alias or caller-supplied app as identity would group unrelated
applications or let callers choose a new budget. A wrong manual mapping can
misattribute spend or deny a valid key until the operator corrects it.

Use exact rolling seconds and calendar UTC token windows. Commit token
reservations locally before generation and settle from verified final usage.
Memory-only token counters would reset on restart. Retaining uncertain usage
can overcharge an interrupted request up to its maximum output but prevents
repeat disconnects from avoiding the budget. See
[the design and sources](gateway-limits.md).

### Boundaries

The gateway protocol does not convey authoritative tenant, app or key
metadata. The shared Rust path uses an explicit workspace/key mapping
and peer credential, so product-wide GW-106 remains open pending
control-side metadata, Console policy management and installation
provisioning. The opt-in runtime configuration and descriptive 429
messages need integration into the shared configuration and public API
contracts. Existing no-policy responses keep their prior body shape and text.
Lane G leaves the roadmap index to the coordinator.

## GW-101: Verify the inherited stream path with real offline generation

Use the cached CPU engine in a separate network-disconnected container and
a loopback relay. This tests the engine protocol and actual tokens without
changing deployment topology. Synthetic-only tests could miss tokenizer or
SSE incompatibilities. Running the NVIDIA production recipe here would
require matching hardware. The CPU engine version differs from the
production recipe, so a pass establishes compatibility with that CPU
fixture only. [Test design and evidence](gateway-chat-streaming.md).

### Remaining P1 boundaries

- GW-101 passed a combined runtime run with the real Go handler, PostgreSQL
  and offline vLLM. Its shipped entrance and node delivery depend on GW-114,
  which is outside the Rust-only lane.
- GW-102 needs a deployed backend that supports embeddings and reranking
  with endpoint-specific admission and outcome contracts. The current
  approved generation recipe and wire cover text chat.
- GW-103 needs streaming chat and authoritative support for the structured
  operations that the coding-tool protocols expose, including tool use.
  Mapping text alone would leave the requested coding-tool flow incomplete.
- GW-104 and GW-105 need control-owned capability metadata and authoritative
  app/model/key scopes. Inference authorization already comes from control.
- GW-107 needs a queryable usage ledger and export surface. The Rust spool
  intentionally removes acknowledged outcomes and is unsuitable as the
  ledger of record.
- GW-108 needs control-approved replica topology. The Rust process is
  bound to one local backend.
- GW-109 needs the deployment-to-GPU identity and a trace propagation path
  into the agent and model engine. A local request UUID alone stops short
  of that boundary.
- GW-110 needs an explicitly authorized connected deployment and
  external-upstream policy. The current lane must stay offline.
- GW-111 needs the control-owned configuration publisher, preview and
  rollback surface. Editing local JSON does not provide that workflow.
- GW-112 needs a shipped entrance and a representative authorized serving
  setup. The GW-106 microbenchmarks isolate local and shared quota checks.
- GW-114 has a verified Rust image and TLS listener. The agent must still
  start the gateway and provision its credential and certificate.
- GW-118 and GW-120 need changes to the Go provisioning/alias authority and
  Console or Portal. Those paths belong to other lanes.
- GW-121 needs the installation's TLS entrance and the control-owned alias
  routing table to direct a request to its serving node.
- GW-122 can use the Rust listener's existing certificate and key files.
  The agent must accept and mount site-supplied credentials as part of
  gateway delivery.

## GW-106: Publish queued budget updates together

Use a FIFO writer per tenant and share the existing durable snapshot across
ready debits and refunds. The per-update writer serialized filesystem
publication under a mutex and showed a long contention tail. Delayed
checkpoints could admit calls using balances that might be lost on restart,
so the gateway continues waiting for a durable debit. The snapshot format
stays compatible and the existing active-request limit bounds the queue.
An error in a shared write fails all tentative changes in that batch and
closes the tenant's writer. See [the benchmark and failure model](gateway-limits.md).

## GW-106: Use a shared runtime authority

Reuse the bounded gateway TLS listener and the same hierarchical policy
and durable ledger for a designated authority. Each authenticated peer
is scoped to the Go-authorized workspace and sends key UUIDs only.
This closes the independent-counter gap without changing Go contracts
or adding a database to the quota check. Static slices would strand idle
quota and a new datastore would expand the offline delivery surface.
Fail closed on loss of the authority and fence startup from leases left
by the previous process. An authority restart can delay new requests for
up to six minutes. Keep authority state and peer credentials private and
provision exactly one authority per group. Automatic management and
failover require deployment and control integration.
