# Optional model-directory cache transport

This package implements the bounded single-instance Valkey RESP2 subset used for derived model-directory bytes. It never owns models, authorization, tenant scope or the authoritative directory revision. Those decisions remain in [the models service](../../modules/models/README.md).

## Entry points and injected settings

[New](client.go) validates local settings and protected password/CA material, then returns a `Client` with `Get` and `Set`. A disabled or absent configuration yields no active client. Construction does not connect to Valkey, so temporary cache unavailability does not gate startup. The [server runtime](../../../cmd/server/runtime_lifecycle.go) injects it into `models.Service.DirectoryCache`.

[ModelCacheConfiguration](../config/model_cache.go) owns enablement, address, TLS/CA, username/password file, namespace, TTL, request budget, maximum payload and concurrent operations. Use the [YAML example](../../../../../config/examples/control/model-cache.yaml.example) and [explicit env mapping](../../../../../config/examples/control/model-cache.env.example). The configuration loader handles these settings; the adapter never reads ambient environment. TLS verifies the selected server; choosing a supported non-TLS configuration is explicit, not a fallback after a certificate error.

## Bounded operations and source fallback

Each operation admits a nonblocking concurrency slot, creates a context deadline, opens one connection, authenticates and performs the exact GET or SET/PX command. Connection closure follows completion or cancellation; there is no independent reconnect loop or persistent connection pool to drain. A caller with too little remaining deadline skips cache work to preserve an equal budget for source fallback. Reply lengths and accepted RESP forms are bounded.

Command reply types follow RESP2: AUTH and unconditional SET/PX require the simple string `OK`; GET requires a bulk string or null bulk miss. A bulk payload whose bytes are `OK` remains valid GET data. A mismatched reply type is an unavailable-cache result, with the same SQL fallback and sanitized error as a transport failure. See the official [AUTH](https://valkey.io/commands/auth/), [GET](https://valkey.io/commands/get/) and [SET](https://valkey.io/commands/set/) contracts (checked 2026-11-03).

[models/directory_cache.go](../../modules/models/directory_cache.go) builds the source fingerprint and validates decoded values against current SQL entries and tenant identity. Invalid, polluted, absent or unavailable cache data falls back to the authoritative directory. [reads_repository.go](../../modules/models/reads_repository.go) fills the cache only after a confirmed successful transaction; a failed or uncertain commit cannot publish derived bytes. A concurrent source change can suppress a cache fill rather than attaching new bytes to an old fingerprint.

Do not repair a cache incident by clearing shared namespaces or by treating cached data as a source of access rights. The next valid read can rebuild a derived entry; reconnecting requires no business mutation replay.

## Verification and changes

With the pinned toolchain and admitted cache/temp roots:

```sh
go -C services/control test ./internal/platform/modelcache
```

[client_test.go](client_test.go) exercises bounded RESP framing, cancellation, local verified TLS, secret admission and deadline preservation. These are controlled transport checks. [The model-directory integration test](../../modules/models/directory_cache_repository_integration_test.go) separately requires a real private Valkey fixture and the selected product database. New cache fields must preserve exact source binding, configured bounds and SQL fallback; do not turn this adapter into a general Redis client or durable storage owner.
