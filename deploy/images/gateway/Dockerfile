# Model gateway image, run by the agent on a serving node beside the model (ADR 0051).
# Build context: a tar of tracked files, see `make image-gateway`.
FROM docker.io/library/rust:1.99.1-slim-trixie@sha256:7eab7ce19273ce785e0ca7578bdf7bc1fcbae621775a8197379a04c9f6e03cd5 AS build
WORKDIR /src/services/gateway
# Dependencies first, from the lockfile alone, so a source change does not download them again.
# Cargo reads a manifest only with a target, so a placeholder stands in for the sources.
COPY services/gateway/Cargo.toml services/gateway/Cargo.lock ./
RUN mkdir src && echo 'fn main() {}' > src/main.rs && cargo fetch --locked && rm -r src
COPY services/gateway/src/ ./src/
RUN cargo build --locked --offline --release && install -D -m 0755 target/release/projectone-gateway /out/projectone-gateway

# The gateway trusts only the CA files its configuration names, so the image carries no CA bundle.
FROM docker.io/library/debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2
COPY --from=build /out/projectone-gateway /usr/local/bin/projectone-gateway
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/projectone-gateway"]
CMD ["serve", "--config", "/run/projectone-gateway/gateway.json"]
