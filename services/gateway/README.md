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

Start from [gateway examples](../../config/examples/gateway/README.md) and [configuration contract](../../contracts/configuration/v1/gateway). Limits include connection/queue/thread/body/event/spool/time/disk bounds. Current socket address types are IPv4 and the current TLS/control-origin contracts are specific; do not claim the broader requested transport/IPv6 matrix from these settings.

Update control/distribution, gateway/public contracts, config/schema/env projection, systemd/container recipes and failure/recovery tests together when changing protocol, admission or outcome semantics. Never add a permissive backend fallback after authorization or TLS failure.

## Verification

With pinned Rust and external cargo/build/temp roots:

    cargo test --locked --offline --manifest-path services/gateway/Cargo.toml --all-targets

The bounded [quality harness](../../tools/quality/gateway/run.py) adds formatting/build/lint and explicit process/tokenizer profiles. It requires state-root, volume-root and preflight-script inputs. Actual target backend/GPU and full control authorization are distinct gates; synthetic upstreams do not qualify them.

`Configuration::load_with_environment` is the single JSON/explicit protected env-file owner; `main.rs` forwards the optional trailing `--env-file` for every operator/serve command. All fields map through the documented gateway.env.example with final typed validation; ambient variables never select runtime policy.
