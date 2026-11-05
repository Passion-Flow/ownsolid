# Control platform adapters

Platform packages provide bounded shared mechanisms. Domain owners still decide current user, tenant, Workspace, object and business state.

| Package | Owner boundary |
| --- | --- |
| agentadmission | In-memory admission of every agent call before database work: address and per-node call limits, priority seats and retry hints ([ADR 0018](../../../../docs/decisions/0018-agent-call-admission.md)) |
| agentgateway | Stateless gateway for the agent session stream: holds open streams and passes each heartbeat and claim to the node service ([ADR 0007](../../../../docs/decisions/0007-agent-session-protocol.md)) |
| cellroute | Maps scoped public paths to the cell-relative paths modules route on ([ADR 0006](../../../../docs/decisions/0006-cell-and-site-scope.md)) |
| [config](config/README.md) | Main typed configuration, defaults, explicit env-file overlays and protected references |
| [database](database/README.md) | PostgreSQL installation/migration plus MySQL pool, migration journal and selected transaction adapters |
| [httpserver](httpserver/README.md) | Bounded listener, request decoding, static/download responses and structured problems |
| [health](health) | Health/readiness support |
| liveness | In-memory table of when each node was last heard, its latest report and its recorded connectivity, so heartbeats cost no database write ([ADR 0022](../../../../docs/decisions/0022-liveness-outside-the-database.md)) |
| [localization](localization/README.md) | Offline locale/resource/font inventory admission and serving metadata |
| [mail](mail/README.md) | Encrypted durable outbox, rendered templates and SMTP transport |
| [modelcache](modelcache/README.md) | Bounded single-instance Valkey RESP2 adapter for derived model-directory caching; never business model ownership |
| [monitoringmetrics](monitoringmetrics/README.md) | Bounded HTTP measurement/recorder used by the monitoring domain |
| [objectstore](objectstore/README.md) | Private configured S3 transport and staging for server-resolved object identities |
| [pagination](pagination) | Shared bounded pagination mechanism |
| [privateclusterproof](privateclusterproof/README.md) | M28 bounded probe challenge/result verification and probe program; no business admission or cluster-management authority |
| [registryconfig](registryconfig/README.md) | Typed Registry configuration borrowing the verified bundle library |
| [snapshotio](snapshotio/README.md) | Chunk-bounded snapshot writeback; callers retain paths, ownership, integrity and final durability/publication |
| [retention](retention/README.md) | Selected-store retention passes and observable joined maintenance loop |
| [taskbus](taskbus/README.md) | Private NATS JetStream transport, exact broker profile, envelopes and acknowledgements |

## Invariants

[database/transaction](database/transaction/README.md) borrows an already configured pool. Repository-owned SQL names its dialect explicitly; the platform does not rewrite SQL, commit inside borrowed callbacks, or log statements and arguments. Preserve uncertain commit classification. [mysqlstore](database/mysqlstore/README.md) owns its protected connection/session invariants; [mysqlmigrate](database/mysqlmigrate/README.md) owns the separate DDL journal and reconciliation.

Transports do not grant business authority. Object keys use server-resolved UUID scope. Task bus publication/consumption transports existing task intent; it does not authorize task execution. Localization is installed offline data, never a customer-runtime translation API.

Mail and retention currently import modules/audit to preserve transactional audit writes. Registry configuration imports a production-shared bundle package located under tools/release/offline. These are recorded dependency exceptions; do not remove these directories from release inputs or invent unaudited substitutes.

## Modification checklist

Change the owning typed configuration together with schema, documented env example, Compose/Helm mapping and invalid/false/nondefault tests. Update upstream domain consumers and composition when lifecycle or ownership changes. New background work must join the server runtime lifecycle.

Focused example, with pinned toolchain and externally admitted caches/temp roots:

    go -C services/control test ./internal/platform/database/transaction ./internal/platform/config

Real SQL, SMTP, TLS, S3, broker and browser profiles require their declared fixtures and separate receipts. See the [backend audit](../../../../docs/architecture/current-product/backend-audit.md).

## Health and readiness

`health.CheckSelected(ctx, store, verifySchema, method, path)` is called by the identity HTTP handler. GET `/health/live` reports that the process can respond; it does not inspect a database. GET `/health/ready` requires the actual selected PostgreSQL/MySQL store, a non-nil successful schema verifier and `installation_state.bootstrap_complete=true`. Missing verification does not become a successful readiness response. Other methods/paths return the ordinary not-found code. The legacy `Check` wrapper supplies a PostgreSQL adapter; new selected-database composition should pass the actual store and verifier.

There is no health-specific deployment policy or worker to restart. Database/schema/bootstrap recovery belongs to the selected database and identity owners. Do not expand readiness into assumed provider, SMTP, GPU or cluster qualification without a separate owning contract. [health/selected_test.go](health/selected_test.go) verifies missing selected readiness cannot pass; full root readiness still needs the selected-database integration profile.

Focused check, using admitted toolchain/cache/temp roots:

```sh
go -C services/control test ./internal/platform/health
```

## Pagination and scoped cursors

`pagination.ParseWithDefault` receives query values, a caller-composed scope and the owning module's configured default page size. It preserves the legacy parser while adding exact canonical base64/JSON, required timestamp/UUID and empty-value checks. `Encode` produces the opaque cursor using the returned scope fingerprint. The package has fixed protocol caps for limit/cursor size but no database connection or separately adjustable runtime configuration.

The cursor hashes the supplied scope; it is not an authorization token or signed capability. Each list owner must recheck current actor/tenant/Workspace authority, construct the complete filter scope, and apply the matching SQL ordering/keyset predicate. Never carry a cursor into a different filter or rely on cursor possession for access. Unsupported/duplicate query fields and malformed cursors fail; recovery is a new correctly scoped list request, not relaxing decoding. Legacy `Parse` retains its documented older semantics until each owning caller is migrated.

[pagination/configured_test.go](pagination/configured_test.go) covers configured defaults, invalid/canonical representations, scope mismatch and query rejection. A parser test is not an assertion that every consumer list uses the same order or has completed migration.

```sh
go -C services/control test ./internal/platform/pagination
```
