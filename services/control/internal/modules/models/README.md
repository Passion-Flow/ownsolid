# Model artifact ownership

Models owns import intent/chunks, approved model versions, durable bytes, integrity/readback, derived-model registration and retirement. Deployments and tools consume this owner's verified references; they must not turn an arbitrary filesystem path into a model.

The startup owner can validate an enabled upload-retention policy with
`ImportExpiryPolicy.Validate` before registering the existing model worker.
It must also authorize the workspace against the selected PostgreSQL installation.
Keep the policy immutable after startup. A nil policy disables expiry.
`Service.Run` is a blocking worker loop. Cancel its context and join it before
closing its repository or store. Active uploads retain their own file lock and
request context. An acknowledged expiry intent survives cancellation before
discard and is resumed by the next worker. See the
[startup and shutdown contract](../../../../../docs/research/model-import-cleanup.md#typed-startup-and-shutdown-contract).

[service.go](service.go) exposes import/list operations. [importer.go](importer.go), [import_lock.go](import_lock.go), [storage.go](storage.go) and [verification.go](verification.go) separate bounded import execution and stored-byte verification. Derived registration/storage/snapshot files bind accepted preparation output. [directory_cache.go](directory_cache.go) uses the configured cache without replacing business ownership.

Model/version/import state is stored by the cumulative `000011_models` migration;
the later lifecycle migration binds derived publication. Bytes and SQL must agree
on catalog/source/scope and digest. Queued/uploaded does not mean verified;
cancellation, interrupted import, inconsistent directory identity and retirement
need existing worker/recovery paths.

[import_manifest.go](import_manifest.go) resolves the persisted catalogue digest
to canonical metadata and the installed archive and directory verifiers.
Admission, chunk writes, recovery and publication use that authority.
The existing Qwen digest retains the shared Agent verification path.

After upload completes, the worker checks the approved USTAR member layout
before extracting any model payload. It refuses executable and unknown
members, invalid Safetensors structure and changed loader configuration.
The full extraction still verifies committed chunk and file hashes. If the
worker reports an invalid bundle or weight format, discard the upload and
import a fresh archive of the pinned revision. Repeating verification on
the same unsafe bytes leaves the import failed.

[license.go](license.go) resolves reviewed terms for an exact verified catalogue
and evaluates the installation's on-site use. It uses the pinned license text
hash. A derivative requires its own reviewed terms.

The scoped derived model detail checks its retained catalog and publication
graph, then validates the frozen preparation input against the original
training job, adapter and holdout references. PostgreSQL and MySQL return
the verified training and holdout versions separately to internal callers.
The published manifest and the HTTP response retain their existing shapes.
The native approved-base detail path retains its existing behavior.
See the [lineage contract handoff](../../../../../docs/research/training-dataset-lineage.md#dev-contract-handoff).

The staged PostgreSQL family domain uses unnumbered
`testdata/model_family_candidate.up.sql`; it is not part of installed migrations.
It backfills one independent family per legacy version and atomically assigns
default membership when an import or derived version is inserted. Publishers
can call `StageVersionFamilyWithin` with a current Admin and an exact anchor
before their version insert in that same transaction. Empty anchor creates a
new family. The caller owns commit and must suppress any result on a failed or
unknown outcome. Membership is immutable; retirement never removes it.

`History` and `HistoricalVersion` expose bounded currently authorized Admin
metadata, including retired versions. They resolve family from an exact anchor
and validate the existing retained graph. They perform no physical file IO and
grant no execution permission. `VersionHistoryPolicy` reports the configured
UI page default and fixed wire bounds. Continuation is signed with the installed
Identity HMAC key and bound to actor, session, audience, scope, family, anchor
and filter. Pagination requires that installed key; both reads fail closed if the
candidate schema is absent. Public contracts,
routes, formal migration/role/catalog admission and S-202 remain with their
owners. See the [family domain contract](../../../../../docs/research/training-dataset-lineage.md#durable-family-domain-contract).

After the import's version and audit event commit, the worker reclaims the
upload chunks and failed stages. During import the store still needs space
for the bundle and extracted model. The worker retains staging and logs
`model_import_cleanup_failed` with the import ID if it finds unexpected files
or cannot verify the published copy. Inspect the model store and restore damaged
published files from a verified backup before retrying. The worker retries
automatically, and the import's history and model files remain in the store.

The optional PostgreSQL expiry worker is a candidate. A trusted caller
can set `Service.ImportExpiry` before starting `Run` to reclaim incomplete
uploads for one active workspace after an explicit period without new
committed bytes. The worker writes a system audit and durable intent,
discards recognized staging files, then records the cancelled import
with `MODEL_IMPORT_EXPIRED`. The original import ID and byte count remain
visible through the existing read and idempotency paths. After expiry,
start a fresh import with a new idempotency key. Startup configuration and
the Console are pending, so deployed installations leave expiry disabled.

This is a large owner with distinct import, verified storage and derived-model areas. A future split should preserve one authority and the exact acceptance/retirement transaction; do not create parallel “base” and “derived” authorization systems just to reduce file count.

A leased file transfer computes a digest of the requested range. When
that range covers the complete file, the owner also compares it with the
pinned catalogue before releasing a download. A mismatch returns
`MODEL_FILE_CHANGED`. The Agent verifies the complete digest of a
multi-range file before use.

Synchronize contracts/agent model catalogs and model-lifecycle references, [M06](../../../../../docs/milestones/m06-models), control/deployment consumers, [models frontend](../../../../../web/README.md), model-cache policy/examples and offline/image source manifests.

Focused native check: go -C services/control test ./internal/modules/models. Actual import, empty-store identity, derived transfer and SQL tests have separate fixture requirements. Imported bytes do not establish inference/GPU performance.

Snapshot copying uses [snapshotio](../../platform/snapshotio/README.md) for 8 MiB
writeback barriers. This owner retains approved-file verification, exact count/
hash, exclusive creation, final permissions/Sync/Close, directory Sync and audit.
Partial failures cannot become ready. See the
[M17 contract](../../../../../docs/milestones/m17-offline-compose/snapshot-writeback.md).

The native DM approved-base source path is described in [native-chain.md](native-chain.md). It adds explicit import/worker/read SQL and a pinned-session DBMS_LOCK fence while retaining artifact verification and unknown-outcome recovery. The shared native SessionStore, schema registration and real integration qualification remain required; derived registration and retirement are not completed by this slice.

The native schema source for this first path is the single `models_v2` contribution in `platform/database/dameng/migrate/contribution_models.go`. Central registration, shared prerequisites and fresh/upgrade DM execution remain separate gates; approved-base/publication-disable restrictions are deliberate.

`native_schema_test.go` checks the owned conditional unique-key declarations against terminal/active lifecycle cases, including DM's all-NULL composite-key rule. It reads source metadata only; registered migration execution, catalog readback and concurrent constraint enforcement remain real-DM validation requirements.


M06 cumulative integration (2026-10-26): explicit `models` extends maintenance
through entry11 and `upgrade-models`, retaining the installed prefix/principal.
Only approved immutable import, deployment/GPU ownership, exact leased file
transfer and bounded model worker are added. Later owners remain structurally
inactive. Minimal restore invalidates imports and preserves uncertain GPU leases;
see [current integration](../../../../../docs/execution/mvp/m06-cumulative-integration-20261026.md).
