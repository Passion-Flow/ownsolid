# Model traffic gateway

The Rust gateway serves bounded model-list, chat, foreground text Responses and Messages traffic against the selected local backend. It is a separate data-plane process; the Go control service remains the authority for API keys, model aliases, scope, license and request admission.

## Runtime path

The [source guide](src/README.md) maps request admission, backend streaming, durable outcomes and the modules that must change together.

[src/main.rs](src/main.rs) supports serve, inspect, deliver-outcomes and explicit fence operations. [configuration.rs](src/configuration.rs) owns private JSON/secret paths and validated limits. [routing.rs](src/routing.rs) parses the request, resolves allowed models through control, waits for active capacity, reserves outcome-spool capacity and repeats admission immediately before backend work.

[backend.rs](src/backend.rs), [transport.rs](src/transport.rs) and [streaming.rs](src/streaming.rs) bound backend requests, response/event bytes, deadlines and stream completion. [admission.rs](src/admission.rs) owns queue/active permits and terminal execution accounting. [control.rs](src/control.rs) validates control responses and outcome acknowledgements.

## Published model aliases

Use the model name returned by the authorized HTTPS model list. The
gateway checks that the durable admission belongs to the same alias
before calling the engine. A fresh target for that alias can serve an
admitted request after a cutover. An inconsistent alias identity returns
`gateway_unavailable` and records the request without token usage.

## Chat message content

Chat Completions accepts a nonempty string or an array of nonempty
`type: "text"` content parts for the existing system, user and assistant
roles. The gateway joins text parts in order with newline separators,
then uses the same text for tokenization and generation. The request
body, context and output limits apply to both forms. Invalid or
unsupported parts fail before authorization or dispatch.

## Foreground Responses

Send `POST /v1/responses` over gateway HTTPS with `Authorization: Bearer <workspace API key>`
and `Content-Type: application/json`. Use the key's public model alias.

```json
{"model":"public-alias","input":"Summarize the operating procedure.","max_output_tokens":128,"store":false,"stream":true}
```

The text subset accepts a nonempty string `input`, optional string
`instructions`, `max_output_tokens`, `temperature` and `stream`.
Storage and background execution default to false and accept only false
when supplied. The gateway rejects item arrays, tools, structured output,
reasoning parameters and conversation state. There is no stored response
to retrieve or cancel by ID.

Omit `stream` or set it to false to receive one JSON response. A streaming
caller receives named SSE events with increasing sequence numbers and stable
response and output item IDs. Close the connection to cancel. A verified
upstream stop returns `completed`. A token-limit or content-filter stop
returns `incomplete` with its reason and verified usage. The terminal response
reports input, output and total tokens after local outcome persistence. Cache
and reasoning breakdowns are unavailable from this path. A mid-stream failure
can deliver partial text followed by `response.failed` with an ownsolid
error code and null usage.

Responses and Chat Completions share admission, token reservations, workspace
limits and usage reporting. This endpoint covers foreground string-to-text
creation. Full coding-tool interoperability and Anthropic Messages are tracked
in [GW-103](../../docs/product/roadmap/gateway.md).

Run `cargo test --locked --offline responses` from this service for the
loopback protocol and quota tests. The native `gateway_integration` profile
in control's distribution package includes
`TestRealGoRustPostgresForegroundResponses`. It needs the compiled gateway
and an isolated PostgreSQL fixture. See the
[development environment](../../docs/development/environment.md#database-integration-tests).

## Anthropic Messages text

Send `POST /v1/messages` over gateway HTTPS with `anthropic-version: 2026-10-21`
and `Content-Type: application/json`. Supply one ownsolid workspace API key in
`x-api-key` or `Authorization: Bearer <workspace API key>`. Use the public
model alias for the key.

```json
{"model":"public-alias","system":"Use short sentences.","messages":[{"role":"user","content":"Summarize this procedure."}],"max_tokens":128,"stream":true}
```

The local text subset accepts alternating `user` and `assistant` turns,
starting and ending with a user turn. A turn or top-level `system` is a
nonempty string or an array with one plain `text` block. `max_tokens` is
required and follows the gateway's configured bounds. Sampling uses the
local gateway defaults. Unsupported versions, headers for client-selected
workspaces or beta features, assistant prefill, tools, attachments, sampling
overrides and custom stop sequences are rejected before generation.

Omit `stream` or set it to false to receive a JSON message. A streaming caller
receives `message_start`, indexed text block events, `message_delta` with
verified output tokens and `message_stop`. The initial input count comes
from the backend tokenizer. The gateway reports final usage only after
storing its outcome. Supported stop reasons are `end_turn` and `max_tokens`.
Unsupported backend stop semantics produce an error. If a stream fails after
headers and the connection permits it, the gateway sends a named `error`
event. Close the connection to cancel the request.

JSON and SSE use the same workspace authorization, budget and outcome
reporting as the other generation routes. Error envelopes include an ownsolid
`error.code` alongside the protocol error type and both request ID forms.
Full coding-tool interoperability remains tracked in
[GW-103](../../docs/product/roadmap/gateway.md).

Run `cargo test --locked --offline messages_` in this service to check the
loopback protocol. The native control `gateway_integration` test
`TestRealGoRustPostgresForegroundMessages` exercises the same endpoint against
the real distribution owner and an isolated PostgreSQL fixture.

## Tool requests

The Responses and Messages routes reject tool declarations and tool result
history before generation. The pinned serving recipe has no parser for a
native call envelope. See [tool round-trip research](../../docs/research/gateway-tool-roundtrip.md)
for the tested schema, binding and serving boundaries.

## Backend connection recovery

A connection failure during model discovery or tokenization temporarily
opens a circuit for this gateway's local backend. Preflight calls return
`upstream_unavailable` for the configured `connect_seconds` interval, then
one caller can probe recovery. The log records circuit opens and recovery.
An HTTP response closes a recovery probe, and the gateway still checks the
response status, content and model identity.

Each completion is sent once. An upstream HTTP refusal, a timeout or a
stream interruption returns the outcome for that attempt. The gateway
keeps the control-authorized destination throughout the request.

## Persistence and shutdown

[storage.rs](src/storage.rs) owns the private locked spool, exact owner/digest binding, reserved capacity and restart recovery. [reporting.rs](src/reporting.rs) sends retained outcomes separately from the client response. Unacknowledged outcomes must remain durable; a client receiving bytes does not prove control received usage.

Streaming chat sends text as it arrives. With `stream_options.include_usage` enabled, the final usage event immediately precedes `[DONE]`. Both are sent after the gateway validates upstream completion and stores the outcome locally. An invalid or incomplete upstream stream can deliver partial text followed by an error.

Shutdown stops admission, joins stream workers and the reporter and retains recovery state. Operator delivery can flush existing outcomes without authorizing new inference. Fence uses an explicit challenge and new private receipt path.

## Configuration and changes

The TLS listener binds the IPv4 address the operator chooses and offers only HTTP/1.1 over ALPN; the backend address must stay on loopback, because the engine has no authentication of its own ([ADR 0052](../../docs/decisions/0052-the-model-gateway-runs-beside-the-model.md)). Start from [gateway examples](../control/config/examples/gateway/README.md) and [configuration contract](../../api/configuration/v1/gateway/schema.json). Limits include connection/queue/thread/body/event/spool/time/disk bounds. Current socket address types are IPv4 and the current TLS/control-origin contracts are specific; do not claim the broader requested transport/IPv6 matrix from these settings.

Update control/distribution, gateway/public contracts, config/schema/env projection, systemd/container recipes and failure/recovery tests together when changing protocol, admission or outcome semantics. Never add a permissive backend fallback after authorization or TLS failure.

## Reload the serving certificate

The node supervisor can send SIGHUP to a running gateway after publishing a
verified serving certificate and key at the existing configured paths.
Verify the chain, advertised hostname, validity and private key match before
replacing the files. Use atomic file replacements, retaining the configured
ownership and permissions, then signal the process after both replacements.
New TLS connections use the replacement pair. Open streams finish on their
existing connections.

Check the process log for `gateway_tls_reloaded`. A rejected candidate emits
`gateway_tls_reload_failed` with a stable code and leaves the previous pair
active. Correct the files and signal again. The signal reloads the serving
pair. The node agent's automatic renewal workflow is tracked in GW-124.

## Verification

With pinned Rust and external cargo/build/temp roots:

    cargo test --locked --offline --manifest-path services/gateway/Cargo.toml --all-targets

`make image-gateway` builds the image the agent runs on a serving node from tracked files only ([Dockerfile](../../deploy/images/gateway/Dockerfile)); set `GATEWAY_IMAGE`, and `DOCKER='env DOCKER_DEFAULT_PLATFORM=linux/amd64 docker'` on an arm64 machine. The image holds the binary and no CA bundle, and runs as 10001:10001.

The bounded [quality harness](../../tools/quality/gateway/run.py) adds formatting/build/lint and explicit process/tokenizer profiles. It requires state-root, volume-root and preflight-script inputs. Actual target backend/GPU and full control authorization are distinct gates; synthetic upstreams do not qualify them.

`Configuration::load_with_environment` loads private JSON and the explicit
protected env file. Operator and serve commands accept a trailing `--env-file`
for settings listed in `gateway.env.example`. Configure quota policy and peer
roles in the private JSON. The gateway validates the resulting configuration
at startup. Ambient variables never select runtime policy.

## Scoped quotas

Add an optional `quota_policy` object to the private gateway JSON passed to
`serve --config`. The example uses illustrative UUIDs. Use actual tenant,
workspace, application and API key IDs. The key ID is the UUID shown in the
key record or public key prefix. The quota configuration takes IDs only.

```json
{
  "quota_policy": {
    "tenants": [
      {
        "id": "11111111-1111-4111-8111-111111111111",
        "workspaces": ["22222222-2222-4222-8222-222222222222"],
        "limits": {
          "requests_per_second": 20,
          "concurrent_requests": 4,
          "tokens": {"hourly": 50000, "daily": 250000, "monthly": 3000000}
        },
        "applications": [
          {
            "id": "33333333-3333-4333-8333-333333333333",
            "limits": {"concurrent_requests": 2, "tokens": {"daily": 100000}},
            "keys": [
              {
                "id": "44444444-4444-4444-8444-444444444444",
                "workspace_id": "22222222-2222-4222-8222-222222222222",
                "limits": {"requests_per_second": 5}
              }
            ]
          }
        ]
      }
    ]
  }
}
```

Each limit is a positive integer and may be omitted independently. Explicit
child limits must be within each ancestor's corresponding limit. Requests
still count toward all configured parent ceilings. RPS is measured over a
rolling second and capped at 10,000 per scope. RPS and concurrency apply to
authorized generation and model-list calls. Once a policy is configured, every key
must be registered for its authorized workspace. Add rotated keys before
switching clients and restart the gateway to apply policy changes.

Token budgets use UTC calendar hours, days and months. Generation reserves prompt
tokens plus the maximum output before dispatch and refunds the difference
when complete usage is verified. Incomplete or interrupted generation retains
its reservation. A 429 identifies the limiting dimension in the error code
and the affected scope in the message. The integer `Retry-After` is a
one-second polling hint for concurrency and rate, or the reset delay for
token budgets. A refund may free capacity before reset.

Keep the private state directory, including `quotas/`, with this gateway
instance across restarts. The directory holds durable token counters. RPS
history and active permits are process-local. Budget I/O errors prevent new
token reservations for the affected tenant. Standalone configuration governs one instance. The control plane continues
to enforce its own admission policy. Shared mode below uses the same
policy across authenticated peers. Automatic scope assignment and installer
support require integration with the control plane.

Run the loopback TLS and quota tests and the separate release benchmarks:

```sh
cd services/gateway
cargo test --locked --offline
cargo clippy --locked --offline -- -D warnings
cargo test --release --locked --offline benchmark -- --ignored --nocapture --test-threads=1
```

## Share quotas across gateway instances

Keep one authoritative gateway for the group. Configure its existing
`quota_policy` with the tenant, application, workspace and key assignments,
and add this object to the same private JSON. Use its loopback or concrete
private IPv4 listener, with a certificate valid for the selected origin.
Every peer needs a distinct protected file containing a random 32-byte
secret encoded as 64 lowercase hexadecimal characters. Copy each file
securely to its authority and corresponding peer. Use separate secrets
for the Go gateway credential and the quota peer credential.

```json
{
  "shared_quota": {
    "mode": "authority",
    "lease_seconds": 150,
    "maximum_leases": 256,
    "peers": [
      {
        "gateway_id": "55555555-5555-4555-8555-555555555555",
        "workspace_id": "22222222-2222-4222-8222-222222222222",
        "credential_file": "/run/ownsolid/gateway/quota-peer.hex"
      }
    ]
  }
}
```

On each peer, use the gateway ID registered in that list. Configure
`shared_quota` as shown here, and define the shared policy on the authority
only. `ca_file` holds the CA that signed the authority's serving
certificate.

```json
{
  "shared_quota": {
    "mode": "client",
    "origin": "https://quota.internal.example:19443",
    "ca_file": "/run/ownsolid/gateway/quota-ca.pem",
    "credential_file": "/run/ownsolid/gateway/quota-peer.hex",
    "timeout_seconds": 2
  }
}
```

The peers still authenticate and admit requests with Go control. The quota
authority receives gateway and scope UUIDs, estimates and final usage. It
holds shared concurrency through model execution and writes reservations
before inference. The peer ends model work before its lease expires, so
choose a lease duration covering the configured request, queue and control
deadlines. Serving nodes must keep bounded clock drift and remain awake
during inference. Unknown usage retains the reserved amount.

Keep the authority's private state directory across restarts. Restart
pauses new quota leases for up to 360 seconds to let old leases expire.
Each group uses one authority and state directory. When that endpoint is
unavailable, peers return HTTP 503 and stop new inference. The shared path
uses private operator JSON, TLS and the existing local durable ledger.
Automatic policy assignment, credential provisioning and authority
failover require installation integration.

Run the two-peer TLS integration and the loopback lease benchmark with
`cargo test --locked --offline two_tls_gateways` and
`cargo test --release --locked --offline shared_quota_round_trip_benchmark -- --ignored --nocapture`.
