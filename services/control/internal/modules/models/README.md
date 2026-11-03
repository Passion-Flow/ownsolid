# Model artifact ownership

Models owns import intent/chunks, approved model versions, durable bytes, integrity/readback, derived-model registration and retirement. Deployments and tools consume this owner's verified references; they must not turn an arbitrary filesystem path into a model.

[service.go](service.go) exposes import/list operations. [importer.go](importer.go), [import_lock.go](import_lock.go), [storage.go](storage.go) and [verification.go](verification.go) separate bounded import execution and stored-byte verification. Derived registration/storage/snapshot files bind accepted preparation output. [directory_cache.go](directory_cache.go) uses the configured cache without replacing business ownership.

Model/version/import state originates in migration 000006. Bytes and SQL must agree on catalog/source/scope and digest. Queued/uploaded does not mean verified; cancellation, interrupted import, inconsistent directory identity and retirement need existing worker/recovery paths.

This is a large owner with distinct import, verified storage and derived-model areas. A future split should preserve one authority and the exact acceptance/retirement transaction; do not create parallel “base” and “derived” authorization systems just to reduce file count.

Synchronize contracts/agent model catalogs and model-lifecycle references, [M06](../../../../../docs/milestones/m06-models), control/deployment consumers, [models frontend](../../../../../apps/web/packages/models), model-cache policy/examples and offline/image source manifests.

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
