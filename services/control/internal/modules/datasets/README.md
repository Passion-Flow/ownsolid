# Dataset lifecycle

Snapshot copying uses [snapshotio](../../platform/snapshotio/README.md) for bounded
writeback. Dataset path/context/size/hash admission, exclusive output, final
durability, audit and partial-failure retention remain here. See the
[backup and restore runbook](../../../../../docs/operations/backup-and-restore.md).

The [native MongoDB import test](mongodb_native_integration_test.go) exercises
one local TLS/SCRAM source-to-version path with PostgreSQL and CPython. It
retains external-source assertions and needs its protected fixture inputs.
The test source alone does not establish a passing target qualification.

MongoDB receipt reservation binds each retained object identifier as PostgreSQL
UUID before converting it to JSON text. Both the first insert and recovery update
reuse that typed parameter; a direct text cast would make PostgreSQL infer
conflicting UUID/text types before either statement can execute. The actual
source-to-version gate covers initial reservation and retained-object recovery.

Datasets owns scoped source upload, editable drafts/row history, validation and publication jobs, immutable versions and training-input references. It also imports granted MongoDB or object snapshots through their owning adapters.

The native Dameng source path now has explicit selected SQL for upload, draft/row
editing, processor claims, immutable publication, retirement, reference inventory
and coordinated restore invalidation. [dameng_statements.go](dameng_statements.go)
owns the native statements; [dameng_repository.go](dameng_repository.go) owns the
native field and exact cursor-time contracts. JSON is bound as text, fingerprint
bytes remain binary, BIT predicates are explicit, and bounded pagination uses a
validated integer literal. Native inspection inserts each row/history record in
the existing acceptance transaction. A statement failure leaves that transaction;
unknown COMMIT returns no successful projection and never automatically replays.
The worker retains user → Workspace → dataset → draft → job lock order.
Claim-time snapshot storage/scan/context failures leave the transaction without
failure-state writes; only explicitly marked content validation may complete a
failed job in place. Deferred result-close errors cannot be mistaken for content
validation. The native active-name index uses conditional virtual scope/name
columns so every component is NULL for retired datasets.

This is source implementation, not enabled serving or target qualification. The
native account/Workspace graph, schema registration, runtime grants and
composition remain coordinator dependencies. The separate owned
`database/dameng/migrate/contribution_datasets.go` supplies the seven real tables,
cyclic FK ALTERs, indexes and immutable guards for central registration. Mongo receipt persistence/advisory
slots still explicitly require PostgreSQL, so native Mongo import is unavailable;
this increment uses the upload entrance. See the [native repository](dameng_repository.go) and
[central schema contribution](../../platform/database/dameng/migrate/contribution_datasets.go).

[service.go](service.go), upload.go, drafts.go and rows.go form the business input surface. [worker.go](worker.go), worker_authority.go and runtime_repository.go separate claims/state from the [processor](processor.go). Publications and publication_storage.go bind prepared artifacts before making a version usable. Snapshot/initialization/integrity/retirement files handle durable namespace and recovery.

On Linux, a kernel-confirmed missing owned PID or terminal Z/X state without
RSS permits bounded reconciliation of that same child's `Cmd.Wait` receipt.
The status path can disappear before open (ENOENT) or during the read (ESRCH).
A task can also lose its address space while it still reports a nonterminal
state. When such a status omits RSS, the sampler checks the exact PID's
`PF_EXITING` flag. The existing child-stop budget bounds the wait subject to
the original wall and cancellation limits. Unconfirmed or malformed reads
fail closed. Missing receipts fail and join the owned child group. Nonzero
exits and all result, digest and publication fences remain authoritative.
The tagged tests cover exit flags and use real CPython children to exercise
the descriptor race, delayed receipt delivery, live errors, cancellation and
the receipt deadline. See the
[Linux exit research](../../../../../docs/research/dataset-worker-exit.md).

Platform migration [000013](../../../migrations/postgresql/000013_datasets.up.sql) retains datasets/drafts/rows/jobs/attempts/versions; MongoDB import receipts are separate. Every mutation rechecks current identity/Workspace and uses explicit scoped SQL. Python prepares files without business credentials; Go independently accepts current source/attempt and output hashes. Published training input must not silently change when a draft changes.

Source upload captures its current membership authority when it claims the upload
lease, before copying immutable bytes outside SQL. Final import admission requires
the same membership lifetime, a still-uploading draft and the exact live lease;
removal/regrant cannot authorize an old request. Cancelled, cleared, expired or
replaced leases refuse late admission with `DATASET_ATTEMPT_STALE`. Retained bytes
alone do not establish a source receipt or runnable import. A fresh authorized
upload may reconcile them after the old lease expires; cancellation stays final.

The module is large because import/draft/publication/storage have different state boundaries. If separated later, preserve one version authority and shared transaction/source binding; do not unify all stages into a generic job-success flag.

Synchronize the [dataset wire contracts](../../../../../api/datasets/v1),
[HTTP contract](../../../../../api/openapi/v1/datasets/openapi.json),
[data runtime](../../../../../runtimes/data-processing/README.md), training and
model-preparation consumers, [frontend](../../../../../web/README.md),
[typed configuration](../../platform/config/datasets.go), control images and
[import requirements](../../../../../docs/research/dataset-import.md).

Focused package check: go -C services/control test ./internal/modules/datasets. The processor harness and real selected-SQL publication/retirement/worker tests are separate; a successful JSONL parse is not a published usable dataset.

`TestActualDatasetUploadFencesAcrossSourceCopy` adds selected PostgreSQL/MySQL
source-copy interleavings under `repository_integration`: current Portal/Admin,
membership replacement, cancellation and cleared/expired/replaced leases, with
independent file/SQL/audit readback and explicit reconciliation. Its source does
not itself establish a passing execution. The
[actual source-copy cases](upload_fence_repository_integration_test.go) use the
selected-SQL fixture described in the [development environment](../../../../../docs/development/environment.md).
Keep PostgreSQL, MySQL and installed execution evidence separate in the
[product status](../../../../../docs/product/status.md).


Storage is selected through `Identity.Repository`. Normalized-source and version
inventory use the owning PostgreSQL/MySQL/Dameng statements. Receipt-bound
CSV/Parquet originals use the registered PostgreSQL schema-60 table. Other
backends retain their normalized-source inventory. Workers join before the
exclusive store closes.

MongoDB attachment carries an internal context value from the durable export into
the existing CreateDraft and both Upload transactions. After the ordered identity
and Workspace checks, `mongodb_attachment.go` locks the dataset and original
receipt and verifies the current source grant, credential fingerprint, membership
lifetime, attempt, object, proof and live export lease before mutation. Ordinary
text uploads have no connector context. Unknown COMMIT retains the original
checkpoint for explicit reconciliation. `mongodb_attachment_repository_integration_test.go`
exercises real PostgreSQL draft/upload/job/audit and source-copy interleavings from
a synthetic exported receipt; it does not read MongoDB or execute the processor.
The full MongoDB source/immutable-version and integrated HTTP/browser gates remain
separate. The cumulative datasets/training profiles still exclude the connector.

`OpenStoreForVerification` opens the existing ownership lock descriptor O_RDONLY
with the same exclusive nonblocking flock and namespace/private-root/link/mode/
limit checks. It creates or adopts no state. Ordinary `OpenStore` retains O_RDWR.
This fixed verification operation adds no deployment switch and is not a general
mutation sandbox; callers use readonly mounts. Native PostgreSQL `VerifyRestore`
selects it through the [PostgreSQL restore contract](../../../../../api/release/v1/postgres_restore.go).
Unsupported readonly-exclusive locking fails closed and still needs filesystem/
CSI qualification. The original readonly failure remains in its dated checkpoint;
new image/fresh-target execution has separate evidence.

## Streaming source intake

[SourceImporter](source_import.go) accepts an upload body or an object receipt
already authorized by the configured object store's owner. It converts JSONL,
JSON arrays, CSV, and Parquet to private JSONL and returns byte and row counts,
field types, null counts, and bounded diagnostics. Its output SHA-256 binds
the published JSONL bytes without a second disk pass. Callers choose the format
and may opt into skipping recoverable records. CSV accepts UTF-8 with an
optional BOM or explicitly declared GBK. Use a new destination in a private
directory, and configure budgets for the source, output, record, fields,
diagnostics and duration. Parquet additionally stages the original bytes on
disk and requires the [optional decoder runtime](../../../../../runtimes/data-processing/README.md#parquet-source-intake).

If the upload or object body is interrupted, intake reports
`DATASET_IMPORT_SOURCE_READ_FAILED`. Retry with a complete upload or a new
read of the same authorized object. The failed transfer publishes no
candidate or private previews.

The intake result and artifact are ready for a product caller to register
under its authenticated workspace. A commit-uncertain error means the
destination may exist and requires reconciliation before a retry. The older
draft and object-ingestion routes continue to use their existing JSONL contract.

To include the real Parquet decoder and the 128 MiB RSS test in the Go suite,
set `OWNSOLID_TEST_PARQUET_PYTHON` to the protected Python 3.12 executable
with the pinned Parquet dependency, resolve macOS temporary paths, and run
`go test ./services/control/internal/modules/datasets/... -count=1 -v` from
the workspace root. See [the source import research](../../../../../docs/research/dataset-import.md)
for supported encodings and recovery rules.

An optional training format validates each accepted row against a text,
messages, instruction or preference schema before writing the artifact. The
report retains the selected schema and adds a field path to applicable issues.
See [training format research](../../../../../docs/research/dataset-training-formats.md)
for the accepted shapes and the separate training handoff requirements.

An optional [cleaning policy](../../../../../docs/research/dataset-cleaning.md)
trims selected string fields and filters rows with blank fields before training
validation. Filtered, changed and rejected counts are distinct. Private
before/after samples are bounded, excluded from JSON statistics, and available
to the Go caller after a successful artifact commit. The candidate can be
staged for an authorized preview before workspace registration.

## Draft tokenizer preview

The optional control adapter reads the first one to eight authorized draft
rows, up to 64 KiB combined. It selects a ready model in the same workspace,
stages the approved tokenizer files and verifies their full hashes before
running the installed training tokenizer. It returns the rendered prompt,
completion and EOS separately, with counts and the first excess token when
the training budget is exceeded.

The adapter is dormant until control composition injects a trusted Python
executable, the installed training root, the existing model service and a
private scratch root. The HTTP request deadline must cover the child wall
budget plus the model-store read. The dataset child file-size budget must
cover the largest pinned tokenizer file. Public API publication and S-503
are separate release steps. See the
[reproduction and handoff](../../../../../docs/research/training-tokenizer-preview.md#control-plane-bridge).

## Opt-in near-duplicate publication

Admin and Portal publication requests can set the optional
`deduplication` field to `near_prompt_v1` to group similar prompts and
remove eligible near duplicates. Omit the field to use exact
prompt/completion deduplication. The optional test ratio works with
either policy. Both policies group similar prompts for three-way splits.
The rule keeps different completions, guarded text changes, and chain
endpoints without a direct retained match. Approximate removals include
`match_rule` in the duplicates asset. The version still retains the original
effective source and mapping, and a retry uses the original job's rule.
When the comparison budget is exceeded, the attempt returns
`DATASET_NEAR_DUPLICATE_BUDGET_EXCEEDED`. Publish a smaller draft or select
the default exact policy. See the
[policy and evidence](../../../../../docs/research/dataset-near-duplicates.md).

## Opt-in test split

Admin and Portal publication requests can set the optional integer
`test_basis_points` to reserve a test split. Omit it to retain the two-way
default. Each held-out ratio must be between 100 and 5,000 basis points
and their sum cannot exceed 9,000. Validation and test ratios target
connected groups, and unequal group sizes can yield different row proportions.

The handler calls `PublishWithOptions` with the selected test ratio.
Three-way publication groups matching sources and near prompts under
either duplicate removal policy. It requires three connected groups and
keeps one or more in each partition. Both retry paths retain the original
ratio. A publication request that changes the ratio under the same
idempotency key returns `IDEMPOTENCY_CONFLICT`.

Schema 2 records the test ratio, group count, row count, and digest-bound
`test.jsonl` alongside the existing training and validation assets.
Dataset storage and file snapshots accept both schemas. The training
transfer still exposes its existing two assets. See the
[three-way policy and public contract handoff](../../../../../docs/research/dataset-three-way-splits.md).

## Single-turn messages sources

Admin and Portal can send `Dataset-Source-Row-Format: single_turn_messages_v1`
on the existing NDJSON source attachment. Omit the header for the legacy
prompt/completion input. The optional choice must occur once. Each JSONL
line requires a source ID, a textual user message, and a textual assistant
reply. Inspection retains the raw upload and its physical line numbers
while writing prompt/completion rows for existing publishing and training
consumers. Replays and retries keep the original source format. A clone
starts from the canonical effective rows. See the
[format and public integration contract](../../../../../docs/research/dataset-training-formats.md#published-single-turn-messages).


## Registered draft import receipts

Migration 000060 registers the PostgreSQL receipt and a six-column draft
scope key. Existing JSONL drafts receive no invented receipt. The immutable
owner up SQL remains unchanged; the formal down refuses retained receipts
with `DATASET_DRAFT_IMPORT_DOWNGRADE_REQUIRES_RESTORE` and rejects stale
rollback snapshots before DDL. Restore a predecessor backup to downgrade
an installation containing import receipts.

The actual numbered scripts, existing-principal grant resume and eight
production migration/restore histories passed on the disconnected Linux
arm64 fixture. Full repository check, build and test also passed. See the
[qualified input and limits](../../../../../docs/research/dataset-import.md#formal-schema-60-acceptance).
The testdata SQL files retain the original owner artifact; current fixtures
execute the registered migration and its retention guard.

The adapter and HTTP fixtures still require explicit construction. Production
startup does not register the CSV or Parquet routes. The dataset inventory and
integrity checks must admit both original and normalized files before that
registration, followed by public contract and S-503 acceptance. See the
[formal integration boundary](../../../../../docs/research/dataset-import.md#production-lineage-integration).

## Complete source snapshots

Ordinary backup and startup integrity include both the receipt-bound CSV/Parquet
original and its normalized JSONL. Paths, sizes and hashes come from the exact
draft/receipt scope. Copies and hashes stream under the existing file/byte and
wall-time policies. A complete local file snapshot restores without fetching a supplemental
original from object storage.

JSONL-only snapshots retain manifest version 1. A snapshot containing a retained
original uses version 2; its store namespace marker stays at the existing version.
The reader accepts version 1 and requires both roles for version 2. Import checks
the complete reference inventory against the restored database before writing
the destination. Missing/changed files, unpaired/unknown roles, mismatched
metadata and unknown files fail closed. Operator export still requires the
installation lock and stopped runtime paired with its logical database dump.
