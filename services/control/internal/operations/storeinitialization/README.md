# Protected artifact-store initialization

This package creates and verifies fresh configured artifact namespaces and records their initialization audit. [The dispatcher](../../../cmd/server/main.go) supplies the selected configuration, command, exact root, runtime UID/GID, reason, and explicit empty-store confirmation. It is not an import, repair, or deletion service.

## Scope and entry points

[Run](initialization.go) handles `initialize-{dataset,training,preparation}-store`, `check-{dataset,training,preparation}-store-initialization`, and `record-{dataset,training,preparation}-store-initialization` for PostgreSQL. [RunMySQL](mysql.go) provides the selected MySQL path and its configured-model-store command family. Consult `MySQLSupported` and the dispatcher when adding a command; do not assume symmetric engine coverage from a similar command name.

Roots come from the owning typed model/dataset/training/preparation configuration. The operator-supplied root must match the configured root. UID/GID are explicit positive IDs of the intended runtime owners, not values inferred from the operator's process. Dataset, training, and preparation initialization require the corresponding `--confirm-empty-<kind>-store` and a valid `--reason`; their record operations also require a reason. Configured-model initialization has a distinct strict interface.

## Configured-model CLI boundary

The [configured-model flag validator](../../../cmd/server/configured_model_initialization.go) accepts only the sets below. `--config` is a protected configuration file, the root must exactly match `Models.ArtifactRoot`, and both owner IDs must be greater than zero.

| Command | Complete allowed and required flag set | Engine and effect |
| --- | --- | --- |
| `initialize-configured-model-store` | `--config`, `--model-store-root`, `--owner-uid`, `--owner-gid`, `--confirm-empty-model-store=true` | MySQL uses this package's locked initialization/audit path. PostgreSQL takes an earlier filesystem-only dispatcher path and creates no initialization SQL audit here. |
| `check-configured-model-store-initialization` | `--config`, `--model-store-root`, `--owner-uid`, `--owner-gid` | MySQL only; inspect the existing directory proof and audit |
| `record-configured-model-store-initialization` | `--config`, `--model-store-root`, `--owner-uid`, `--owner-gid`, `--reason` | MySQL only; verify the existing proof before recording its audit |

Do not pass `--reason` to configured-model initialize or check; do not pass an empty-store confirmation to check or record. Supplied flags outside each allowlist are rejected, even with false/empty values. MySQL initialize uses its command name as a structural audit reason, not an invented user-provided reason. [Selected command validation](../../../cmd/server/selected_database.go) rejects the MySQL-only recovery commands for PostgreSQL.

## Storage and SQL effects

The operations implemented in this package require the real installation lock, matching selected database target, compatible schema, and initialized tenant. Retained domain history refuses fresh initialization and requires the appropriate restore path. Dataset, training, and preparation owners create/verify their namespace, tenant binding, store UUID, and ownership. [Namespace-lock handling](namespace_lock.go) borrows and verifies their actual owner lock. The configured MySQL model-store path instead uses `models.WithEmptyStoreIdentity`; its proof kind is `empty_directory_identity` and its audit binds `directory_identity_sha256`. It does not create a namespace UUID. These database/audit guarantees do not apply to the separate PostgreSQL filesystem-only configured-model initializer.

Within the package operations, filesystem mutation and SQL audit insertion are separate effects. Audit verification requires the exact domain proof and metadata, including UID/GID and verified references; multiple or mismatched records are conflicts. A namespace or initialized model directory can exist even when its audit write failed or the outcome is unknown.

## Recovery

1. Keep the original configured root and store metadata after any interrupted initialization or `STORE_INITIALIZATION_AUDIT_WRITE_UNCERTAIN`.
2. Reacquire the required installation and owning-store authority, then run the corresponding check operation against that same root and runtime ownership.
3. If verification reports an intact namespace or configured MySQL model-directory proof with initialization not recorded, use its explicit record operation. Recording verifies the existing proof; it is not permission to initialize over retained data or manufacture a store UUID.
4. Investigate audit conflicts, changed device/path ownership, or domain history. Do not remove the directory, invent a new store ID, or insert an audit row manually to clear an error.

## Maintenance and verification

From `services/control`, the focused entry is `go test ./internal/operations/storeinitialization`. [Initialization tests](initialization_test.go), [MySQL tests](mysql_test.go), and [namespace-lock tests](namespace_lock_test.go) cover their declared database/file boundaries. Database-dependent cases require an admitted isolated fixture; a skipped case establishes nothing about that engine.

Changes to a domain's store format, owner, configured root, or audit shape need corresponding verification and interruption coverage. The successful existence of an empty directory is not a restored artifact store.

The tagged RunDameng path uses the same namespace ownership/recovery protocol
with explicit native history and audit SQL. Stored JSON proof is compared as a
bounded canonical object; it is bound as text. Locking reads use FOR UPDATE in
write-capable transactions. A schema/history query failure occurs before any
filesystem initialization. Dataset/model/training/preparation schema contributions
must be integrated first; an interface alone does not qualify that path.


The cumulative datasets profile permits only its dataset command family and
checks the exact selected14-entry catalog before initialization or audit recovery.
Earlier minimal profiles and later training/preparation commands are refused
before SQL/file effects. Legacy platform and alternative-engine scopes remain
independent. Current cold-install regression retains the original full-catalog
refusal and verifies actual namespace/audit repeat behavior on the bounded graph.

The cumulative training profile permits dataset and training namespace operations against the exact selected15-entry catalog; preparation initialization remains disabled before effects. Existing datasets-only behavior is preserved.
