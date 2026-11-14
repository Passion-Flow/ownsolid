# Gateway lane integration boundaries

These are the remaining cross-component interfaces. The lane can build and
test changes in the Rust gateway and distribution owner. These items stay
open until the named integration is usable.

| Item | Dependency and decision |
| --- | --- |
| GW-101 | The latest changes passed the native Go/Rust/PostgreSQL profile and Rust failure cases. The bounded offline vLLM rerun timed out at 361 seconds before model loading or socket readiness, with the running process repeatedly waiting on kernel file or buffer I/O. The changed path still needs a real-engine run. [Chat streaming research](gateway-chat-streaming.md). |
| GW-102 | The generation recipe and admission wire cover text chat. Embedding and rerank need an approved pooling deployment, explicit endpoint capability, and accounting for inputs without generated tokens. [vLLM scoring](https://docs.vllm.ai/en/latest/models/pooling_models/scoring/) uses a different request and result contract. Keep chat admissions bound to chat until serving and wire owners add the new operations. |
| GW-103 | Responses and Messages require their own lifecycle and streaming events plus structured tool calls. The current gateway strips unsupported fields and the engine path accepts a single text choice. Map the structured lifecycle when the public contract and serving recipe support it. [Responses streaming](https://developers.openai.com/api/reference/resources/responses/streaming-events), [Messages](https://platform.claude.com/docs/en/api/messages/create). |
| GW-104 | Distribution already persists alias-to-deployment bindings. Capability metadata needs a serving-owned source and a public discovery contract so an alias can advertise exactly which operations it supports. |
| GW-105 | Workspace, alias, expiry, membership and revocation are enforced. An application binding must be persisted with the key and checked on admission, with lifecycle, console and contract support. Keep app identity server-owned. |
| GW-106 | Standalone and shared quota enforcement have a tested Rust path. Control-owned tenant and application mapping, policy management and installation delivery remain open. [Quota research](gateway-limits.md). |
| GW-107 | Distribution retains request and usage history. Export needs a documented, permission-checked interface for a consistent bounded range and the UI that reaches it. The retry spool deletes acknowledged outcomes and cannot serve as the ledger. |
| GW-108 | The local backend circuit isolates preflight connection failures and allows one recovery probe. Generation refusal and interruption retain their original destination and outcome. Replica selection and failover need a control-authorized target set and an owning route. [Replica resilience](gateway-replica-resilience.md). |
| GW-109 | Gateway correlation reaches the model engine. Operator-visible deployment and GPU trace linkage needs placement and telemetry integration. [Request tracing](gateway-request-tracing.md). |
| GW-110 | Connected sites need explicit egress policy, separate upstream credentials and operator-configured TLS trust. The current fixed loopback backend remains the only runtime destination. |
| GW-111 | Distribution has alias history and cutovers. A configuration publisher needs versioned policy, preview and rollback surfaces in the control API and console, plus safe activation on gateways. |
| GW-112 | The component benchmark exercises the real gateway listener with a synthetic control and model. Final capacity evidence needs the shipped entrance, real authorization and a representative serving workload. [Throughput research](gateway-throughput.md). |
| GW-118 | Credential replacement needs lease-bound agent delivery and coordinated treatment of in-flight outcomes from the old credential. The administrator inventory and rotation workflow need the console. |
| GW-120 | A user-editable description needs alias persistence and API fields, plus editing in the console and reading in the portal catalogue. |
| GW-121 | One entrance needs installation TLS routing and a control-authorized alias lookup across node gateways. It must resolve overlapping names using server-authenticated scope. |
| GW-122 | The Rust listener already reads certificate and key files. Agent-managed gateways need a way to validate and mount site-managed material and deliver the matching trust chain. |
| GW-123 | Profile capability registration, task dispatch, and offline provisioning belong to the agent and delivery paths. |
| GW-124 | The Rust listener reloads a protected certificate pair on SIGHUP and preserves open streams. Control CA renewal and the agent's delivery, validation and signal are still needed. [Certificate reload](gateway-tls-renewal.md). |
| GW-125 | Publishing the correct CA requires a control route with installation trust metadata and presentation next to the console and portal endpoint examples. |

The remaining P2 work follows the P1 boundaries. Route policy depends on
authoritative targets and usage, cache and safety hooks depend on reliable
tenant and key authority, and key lifecycle changes need their operator or
portal flows. The existing client examples can be validated against the
delivered entrance after GW-121 and GW-125.

## Repository verification boundary

On `ownsolid-cpu`, `make check`, `make build`, the gateway tests and Clippy
passed. The browser-free repository test stopped in the offline factory at
`TestPreparedDirectoryReadDetectsMutationAndCancellation/mutation` and
`TestPreparedSnapshotRejectsPostreadChanges/entry_added`. The first failure
reproduced in a focused rerun against unchanged factory source. The release
tool is outside the gateway lane.
