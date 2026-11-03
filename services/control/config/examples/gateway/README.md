# Gateway Configuration Boundary

The Rust startup loader, HTTPS listener, bounded forwarding and durable reporter
are implemented. Current cumulative Go/React/private-client/native-fence checks
are indexed in docs/execution/mvp/m07-closeout-current.md; real target
qualification remains separate. This secret-free example is not a provisioned gateway or working credential. Replace both IDs using the scoped Admin provisioning result and select the approved node-local listener/backend ports. Copy to a new private operator-owned directory, set0600 and provide protected real certificate/key/CA/credential files. Initialize the explicit existing private state directory; never point it at unrelated state. Do not run this example against a public endpoint.

The only application source is the explicit --config JSON startup file (binary syntax: projectone-gateway serve --config /absolute/private/gateway.json). There is no per-field environment fallback or frontend-bundled backend URL. JSON rejects unknown/duplicate fields. Configuration is loaded/validated once and requires restart; no hot reload is implied. CLI/configuration failures expose stable codes without contents. Source schema: contracts/configuration/v1/gateway/schema.json. Single default owner: services/gateway/src/configuration.rs Limits::default. Operator-specified fields override those defaults individually.

Required identity fields are gateway_id/node_id UUIDs. listen_address/backend_address are numeric IPv4 loopback host:nonzero-port, never DNS/public IP. control_origin is a trusted HTTPS origin without userinfo/path/query/fragment/trailing slash. Certificate/CA paths contain public data; credential/private-key and configuration files require current-owner0600 and a private0700 containing directory. All files must be regular, canonical and nonsymlink, with bounded reads; secret bodies never appear in errors. state_directory is an existing canonical current-owner0700 directory. There is no internal storage fallback.

Limits and defaults:

| Field | Default | Allowed bounds / meaning |
| --- | --- | --- |
| runtime_threads / blocking_threads |2 /4|1..8 /1..16 worker threads |
| maximum_connections |64|1..1024; must cover active+queued requests |
| header_seconds / header_bytes |5 /16384|1..30 seconds for TLS/header;8192..65536 HTTP buffer bytes |
| request_body_bytes |65536|1..1048576 bytes |
| request_memory_budget_bytes |67108864|<=268435456; must cover (active+queued)*body limit |
| active_requests / queued_requests |4 /8|1..64 active;0..512 queued |
| queue_milliseconds |2000|1..10000 ms |
| connect_seconds / control_seconds |2 /5|1..10 /1..30 seconds; connect<=control |
| request_seconds / idle_seconds |90 /15|1..300 /1..60 seconds; idle<=request |
| shutdown_seconds |5|1..30 seconds |
| operator_seconds |30|1..300 seconds; one explicit metadata-delivery command |
| response_bytes |1048576|1..1048576 bytes |
| stream_event_bytes / stream_total_bytes |65536 /4194304|256..65536 event, event<=total<=16777216 bytes |
| default_output_tokens / default_temperature |128 /0|1..maximum_output_tokens;0..2 finite temperature |
| maximum_messages / maximum_output_tokens / maximum_context_tokens |32 /512 /2048|1..128 messages;1..512 output tokens;1..65536 context tokens, bounded again by the actual backend maximum |
| spool_max_entries / spool_max_bytes |1000 /16777216|1..65536 entries;<=268435456 bytes and >=entries*4096 |
| outcome_retry_seconds |2|1..60 seconds |
| control_idle_connections |2|0..64 idle connections per configured control origin |
| backend_idle_connections |4|0..active_requests idle connections for the fixed local backend |
| minimum_free_disk_bytes |67108864|4096..9007199254740991 free bytes required before admission |

These are adjustable bounded operating defaults, not measured capacity guarantees. Protocol identifiers, supported text roles remain named contract invariants. Nondefault configured-port parsing, permission and memory/deadline boundary tests are in the Rust module; native network/operator and actual Go/React browser checks are delivered; actual systemd/NVIDIA target checks remain pending. Later runtime contracts and deployment mappings must remain synchronized as M07 closes.

The listener accepts HTTP/1 over TLS with32 maximum headers, one request per connection and no keep-alive. The configured connection cap applies before TLS, without waiting admission tasks. A connection lifetime covers header/body, two control RPC bounds, queue, generation and terminal-delivery bounds. The generation deadline also independently governs the SSE producer; one buffered event is permitted, slow consumers time out, disconnect drops upstream, and terminal DONE follows successful durable metadata persistence. GET /health/live reports process liveness; GET /health/ready reports local spool/admission readiness only. It does not attest Go authorization, backend readiness or GPU qualification; every API request still obtains fresh Go authorization.

On restart, persisted unacknowledged outcomes gate new admissions until all are acknowledged by the matching Go authority. Permanent receipt conflict/validation/authority failures degrade readiness and preserve metadata; restart cannot bypass the pending-record gate. A live process that degraded after an integrity failure requires reviewed restart after correction, even when metadata delivery later succeeds.

Offline operator commands are `inspect`, `fence --challenge UUID --receipt-file NEW_PATH`, and `deliver-outcomes`, each with the explicit --config file. Stop the exact owned serving process first; its original state lock must be available. Receipt files must be new paths outside the managed state directory under an existing private owner directory. A persistent fence blocks ordinary serve forever for that state; delivery-only mode cannot reserve capacity, and acknowledged metadata delivery leaves the fence intact. `inspect` validates/reconciles only owned complete stages under the exclusive lock; conflicts/corruption remain preserved. See docs/milestones/m07-gateway/recovery.md for the implemented ordered Go recovery workflow; cumulative regression and target qualification remain independently tracked.

## Explicit environment file

All runtime fields and limits map one-to-one through [gateway.env.example](gateway.env.example). The Rust typed loader validates protected JSON first, then the optional `--env-file` and the final configuration. No process-environment lookup or shell expansion occurs. Bare strings or JSON double-quoted strings and JSON numbers are accepted; duplicates, unknown fields, empty required values, invalid UTF-8, boolean-for-number and invalid relationships fail closed. Secrets remain protected file references. Restart is required.

Append `--env-file "$GATEWAY_ENV"` after existing serve/inspect/deliver-outcomes/fence arguments. The file is canonical, owner-only0600 under an existing0700 directory. Original gateway/node/control-origin/spool identity rules still apply; overriding configuration cannot rewrite an existing binding or fence. Systemd renderer optional `environment_file` supplies this exact argument. Compose and Helm gateway projections consume the same private JSON/env/credential directory; neither adds defaults or ambient Rust policy.

## Container deployment inputs

`deployment-compose` and `deployment-helm` examples/schemas are generated from
`tools/deploy/gateway/deployment.py`, with complete adjacent English env comments.
That nonsecret typed owner selects exact image, architecture, existing UID/GID,
private configuration/state directories and deployment resources. Rust JSON/env
remains the application policy owner. See tools/deploy/gateway/README.md and
the two deployment guides for host-network, existing-node and storage constraints.
