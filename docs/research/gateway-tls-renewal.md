# GW-124 serving certificate reload

An agent-managed gateway starts with a signed certificate and protected
private key. Replacing a listener by stopping its process would close open
chat streams. The serving process needs a way to accept a replacement pair
while retaining connections negotiated with the previous pair.

[Envoy SDS](https://www.envoyproxy.io/docs/envoy/latest/configuration/security/secret)
lets a running listener consume newly issued TLS material. The pinned
[axum-server TLS API](https://docs.rs/axum-server/0.8.0/axum_server/tls_rustls/struct.RustlsConfig.html#method.reload_from_config)
provides the same separation for this listener: cloned configurations share
an atomic server configuration, and the acceptor reads it for new TLS
handshakes. [rustls](https://docs.rs/rustls/0.23.46/rustls/struct.ConfigBuilder.html)
validates the parsed certificate and private key pair before a configuration
can serve.

Reuse the existing protected files and HTTP/1.1 ALPN configuration. Load and
validate both candidate files before swapping the server configuration. Let
the supervisor send SIGHUP once it has published both files. A rejected
candidate leaves the current configuration active and emits a stable code.
The process and established connections retain their current state.

Restarting the process interrupts in-flight streams and creates a gap in
acceptance. Watching the two files independently could publish a partial
update while the supervisor is replacing the pair. A network-facing reload
endpoint would add a privileged control path for a node-local operation.
An explicit signal gives the supervisor a single completion boundary.

The Rust listener slice passes when an actual gateway process keeps its
old certificate after an incomplete pair and accepts a new certificate
after the pair is complete. A TLS stream already in progress must finish
with its normal DONE and usage accounting across the update. New clients
continue to negotiate HTTP/1.1. The gateway uses the configured certificate
paths, while the agent or operator verifies issuer, advertised hostname and
validity before publishing. Automatic renewal scheduling, certificate
issuance and agent delivery remain separate integration work for GW-124.

No new dependencies, models or data are needed. The pinned crates used by
this change are axum-server and Tokio (MIT), rustls (Apache-2.0, ISC or MIT),
and test-only rcgen (MIT or Apache-2.0). Those licenses permit commercial use,
offline use and redistribution with their required notices.

## Verified native listener behavior

The process integration test passed on Linux amd64 in a loopback-only
network namespace. It replaced only the certificate and sent SIGHUP.
The process reported the mismatch and the original trust root still
established a new TLS connection. It then held a chat stream open,
installed the matching key and signaled again. A new client verified
the replacement certificate, and the existing stream finished with
the expected usage and DONE.

```sh
cd services/gateway
cargo build --locked --offline
PROJECT_ONE_GATEWAY_BINARY="$(realpath "${CARGO_TARGET_DIR:-target}/debug/projectone-gateway")" \
  cargo test --locked --offline --features process-integration \
  native_gateway_reloads_serving_tls_without_interrupting_a_stream
```
