# Gateway request, execution and outcome path

[main.rs](main.rs) is the serve/inspect/deliver-outcomes/fence command boundary; [lib.rs](lib.rs) exposes the runtime modules. [configuration.rs](configuration.rs) validates protected paths, listener/control/backend inputs and resource limits. Protocol/error files define bounded public messages and safe diagnostics.

The request path is listener → routing → current control admission → backend transport/streaming. [admission.rs](admission.rs) owns queue/active permits and terminal accounting. [routing.rs](routing.rs) reserves durable outcome capacity and obtains fresh business admission before backend work; the earlier model lookup is not a reusable authorization decision. [control.rs](control.rs) validates owner responses and acknowledgements.

[backend.rs](backend.rs), [transport.rs](transport.rs) and [streaming.rs](streaming.rs) enforce body/event/deadline limits and distinguish completed response from interruption. [outcome.rs](outcome.rs), [storage.rs](storage.rs) and [reporting.rs](reporting.rs) retain result/usage until exact acknowledgement. The private locked spool has capacity and identity invariants; client-visible bytes do not prove successful reporting. [storage/fence.rs](storage/fence.rs) is explicit operator fencing, not general spool deletion.

The SSE decoder recognizes LF, CRLF and CR line endings across arbitrary transport
fragments, including mixed endings. Its event limit counts original bytes,
including a trailing LF received after the CR has dispatched an event. Multiple
`data:` lines preserve their separating LF even when the first value is empty;
only the exact `[DONE]` payload may terminate a completed response with validated
usage. The final usage event and DONE share one terminal write after EOF and
local outcome publication. The decoder returns at most one event per consume call so transport retains
client backpressure. These framing rules do not add unsupported SSE field types
or change durable outcome publication.

[responses.rs](responses.rs) projects validated chat backend events into the
foreground text Responses wire format. It bounds accumulated output text by the
configured JSON response byte limit. The chat and Responses handlers share one
admission and generation path. Responses lifecycle events alone do not mark
text as started. The client gets terminal text and usage only after the same
EOF validation and durable outcome publication as chat.

Keep configured startup/shutdown, queue stops, worker/reporter joins and uncertain outcomes intact. No fallback may bypass control authorization, TLS verification or spool admission. Current IPv4 socket and transport constraints are described in the [service guide](../README.md).

Synchronize control/distribution, [gateway configuration](../../../api/configuration/v1/gateway/schema.json), public protocol/contracts, protected examples and process/recovery tests. Run `cargo test --locked --offline --manifest-path services/gateway/Cargo.toml --all-targets` only with admitted pinned toolchain/external output paths; tokenizer-integration and actual process/backend profiles require their explicit feature/fixture inputs. This source guide makes no target inference or capacity claim.

Protected configuration supports a trailing explicit `--env-file` through the same typed owner. JSON validates independently before overrides; all limits retain their single Rust defaults/relationships. Numeric false/null, duplicate/unknown/empty keys and insecure files are refused.
