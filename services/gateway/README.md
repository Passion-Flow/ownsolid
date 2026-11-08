# Model traffic gateway

The Rust gateway serves bounded model-list and chat traffic against the selected local backend. It is a separate data-plane process; the Go control service remains the authority for API keys, model aliases, scope, license and request admission.

## Runtime path

The [source guide](src/README.md) maps request admission, backend streaming, durable outcomes and the modules that must change together.

[src/main.rs](src/main.rs) supports serve, inspect, deliver-outcomes and explicit fence operations. [configuration.rs](src/configuration.rs) owns private JSON/secret paths and validated limits. [routing.rs](src/routing.rs) parses the request, resolves allowed models through control, waits for active capacity, reserves outcome-spool capacity and repeats admission immediately before backend work.

[backend.rs](src/backend.rs), [transport.rs](src/transport.rs) and [streaming.rs](src/streaming.rs) bound backend requests, response/event bytes, deadlines and stream completion. [admission.rs](src/admission.rs) owns queue/active permits and terminal execution accounting. [control.rs](src/control.rs) validates control responses and outcome acknowledgements.

## Persistence and shutdown

[storage.rs](src/storage.rs) owns the private locked spool, exact owner/digest binding, reserved capacity and restart recovery. [reporting.rs](src/reporting.rs) sends retained outcomes separately from the client response. Unacknowledged outcomes must remain durable; a client receiving bytes does not prove control received usage.

Shutdown stops admission, joins stream workers and the reporter and retains recovery state. Operator delivery can flush existing outcomes without authorizing new inference. Fence uses an explicit challenge and new private receipt path.

## Configuration and changes

The TLS listener binds the IPv4 address the operator chooses and offers only HTTP/1.1 over ALPN; the backend address must stay on loopback, because the engine has no authentication of its own ([ADR 0052](../../docs/decisions/0052-the-model-gateway-runs-beside-the-model.md)). Start from [gateway examples](../../config/examples/gateway/README.md) and [configuration contract](../../contracts/configuration/v1/gateway). Limits include connection/queue/thread/body/event/spool/time/disk bounds. Current socket address types are IPv4 and the current TLS/control-origin contracts are specific; do not claim the broader requested transport/IPv6 matrix from these settings.

Update control/distribution, gateway/public contracts, config/schema/env projection, systemd/container recipes and failure/recovery tests together when changing protocol, admission or outcome semantics. Never add a permissive backend fallback after authorization or TLS failure.

## Verification

With pinned Rust and external cargo/build/temp roots:

    cargo test --locked --offline --manifest-path services/gateway/Cargo.toml --all-targets

`make image-gateway` builds the image the agent runs on a serving node from tracked files only ([Dockerfile](../../deploy/images/gateway/Dockerfile)); set `GATEWAY_IMAGE`, and `DOCKER='env DOCKER_DEFAULT_PLATFORM=linux/amd64 docker'` on an arm64 machine. The image holds the binary and no CA bundle, and runs as 10001:10001.

The bounded [quality harness](../../tools/quality/gateway/run.py) adds formatting/build/lint and explicit process/tokenizer profiles. It requires state-root, volume-root and preflight-script inputs. Actual target backend/GPU and full control authorization are distinct gates; synthetic upstreams do not qualify them.

`Configuration::load_with_environment` is the single JSON/explicit protected env-file owner; `main.rs` forwards the optional trailing `--env-file` for every operator/serve command. All fields map through the documented gateway.env.example with final typed validation; ambient variables never select runtime policy.

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
authorized chat and model-list calls. Once a policy is configured, every key
must be registered for its authorized workspace. Add rotated keys before
switching clients and restart the gateway to apply policy changes.

Token budgets use UTC calendar hours, days and months. Chat reserves prompt
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
