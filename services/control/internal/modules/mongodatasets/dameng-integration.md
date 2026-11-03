# MongoDB source import with native Dameng metadata

This is a source delivery for M21 in the existing dataset lifecycle. MongoDB
remains a real read-only source, using the existing Go driver, snapshot session,
allowlisted URI options, primary read preference, bounded projection and ordered
ObjectIDs. The metadata database is independently selected. MySQL remains
explicitly unsupported for this M21 path. Nothing here selects a fallback engine.

## Ownership and dependencies

`datasets/mongodb.go`, `mongodb_repository.go` and `mongodb_slot.go` own import
authorization, receipt persistence and the connection-scoped admission slot.
`mongodatasets/reader.go` and `snapshot_source.go` own the real source read.
The new native `contribution_mongodatasets.go` owns `mongodatasets_v2` only.
Ordinary dataset service/repository/worker/upload/storage and central migration,
driver, configuration, fixture and runtime grants are other owners' boundaries.

**Required shared implementation:** [dameng-root.patch](dameng-root.patch)
adds the private `uploadWithSourceGuard` method to the actual existing upload
implementation. Its public `Upload` entry passes nil and keeps ordinary upload
behavior. M21 supplies its current receipt/source fence. Both the initial
claim/replay and the final source receipt execute that fence in the existing
authorized SQL transaction, before locking the draft. The patch contains the
implementation, but is deliberately not applied to the other owner's file.
The M21 callsite requires it: this worktree alone is not a compilable integrated
delivery until the coordinator applies/reconciles that patch. Do not replace it
with an unconditional no-op or a second standalone upload implementation.

Also integrate the real native shared `SessionStore`, dataset selected
repository/SQL and ordered identity/Workspace authority. `datasets_v2` must expose
the actual scoped unique keys below. Shared `CreateDraft`/`Upload` error handling
must preserve `STORAGE_COMMIT_OUTCOME_UNKNOWN` ahead of joined cancellation or
cleanup errors. The M21 wrapper already does so for its own transactions.

## Receipt, authority and recovery

The existing `reading -> exported -> imported` path and failed/expired retries
remain. New attempts retain the creation key, request, source grant and intent.
Unexported retries select a new object; exported retries retain the exact durable
object, digest, byte count, row count and ObjectID interval. Each attempt appends
its object to the bounded retained inventory, including repeated references.
Canonical native JSON is bound as strings; SHA-256 intent remains binary.

Native private `SOURCE_GRANT` records version 1, the complete configured source,
a fingerprint of the protected source/URI/CA bytes, the current identity epoch
and the Portal membership authority UUID (Admin uses null). No password, URI or
CA contents are persisted in this envelope. These private fields are excluded
from API DTOs. Source changes, credential/trust-file rotation, removed source
grants and replaced memberships prevent replay or attachment under the old
receipt. M21 uses the existing configuration owner; no new environment variable
or runtime reload authority is introduced.

All native reads and writes use explicit tenant/Workspace/dataset/actor scope.
The source grant is rechecked after real reads and at durable export, draft
binding, upload admission, final import and receipt reads. Reopened source files
retain the existing pinned-root, permission, size and hash verification. A draft
is durably bound before upload, and its creator, creation key, digest and size
must match. Existing received-source upload replay preserves later row edits.

Every SQL/scan error ends that transaction immediately. No duplicate-key error
is followed by inline lookup on a poisoned native connection. Unknown commits
return no successful DTO and issue no compensating failure write. Retry uses the
original creation key in a new authorized transaction to determine the committed
state. Other failures attempt a separately bounded authorized failure update and
retain cleanup errors. No orphan object is deleted by this path; retained source
objects remain the dataset integrity/maintenance owner's responsibility.

## Session admission

One shared `transaction.SessionStore.WithSession` connection stays pinned across
source read and dataset attachment. Native SQL obtains the current schema and
uses exclusive `DBMS_LOCK.REQUEST(id,6,0,FALSE)`: 0 acquires, 1 tries the next
configured slot, other results fail. The 30-bit ID hashes a fixed M21 namespace,
schema and slot. A collision only reduces admission. Explicit bounded release
must return 0, and the shared owner physically closes the session afterward.
Release/close failure cannot project success. A failed acquisition statement is
not followed by release SQL on that connection. Metadata uses separate ordinary
transactions, so its failure does not poison the pinned admission connection.

The existing typed source/close/upload/commit budgets bound the outer lifetime.
The runtime pool must permit the pinned slot(s) plus metadata progress and the
host's existing headroom. Restricted runtime accounts need documented execution
access to DBMS_LOCK; granting DDL or administrator access is not a workaround.
DM MPP is outside this DBMS_LOCK profile. Real cross-connection acquisition,
cancellation, release and connection retirement are target qualification gates.

## Native schema

Register `mongoDatasetsFragment(current)` as `mongodatasets_v2`, after
`foundation_v1` and `datasets_v2`. It contributes one 25-column native table,
two ordinary indexes and insert/update guard triggers. It resolves existing
constraint names by exact ordered key columns, and fails catalog validation if
any required key is absent:

- `USERS(ID)`;
- `DATASETS(TENANT_ID,WORKSPACE_ID,ID)`;
- `DATASET_DRAFTS(TENANT_ID,WORKSPACE_ID,DATASET_ID,ID)`.

The creation uniqueness is the full non-null tenant/Workspace/dataset/actor/key
tuple, so no conditional unique-key NULL projection is needed. Proof is entirely
absent or complete; imported state requires a bound received draft. Checks reject
malformed JSON, UUIDs, digests, ObjectID ranges, invalid attempts and timestamp
ranges. UTC timestamps are microsecond aligned. Native triggers enforce immutable
scope/intent/request/grant, immutable completed proofs and draft binding, exact
attempt increments, expired/failed retry admission, an append-only canonical
retained array and immutable imported rows. Draft relationships include creator,
creation key, expected bytes, source digest and final source-received status.
Existing historical migration numbers are untouched.

## Integration gates and executable checks

Before enabling M21 on native DM, the data owner must add `MONGODB_DATASET_IMPORTS`
to the native dataset source snapshot/integrity inventory. Keep the current
digest/bytes proof and scoped object namespace, including the retained object
inventory used for cleanup/recovery; never drop a source because the native branch
formerly omitted MongoDB. The root must update owned architecture/contract/status
and retained-source build manifests at the integrated checkpoint, and confirm
the server selected-engine readiness includes this fragment and session support.

After the mandatory patch/shared integration, the coordinator may run:

```sh
go -C services/control test ./internal/modules/mongodatasets ./internal/modules/datasets
go -C services/control test -tags='repository_integration,dataset_processor_integration,mongodb_integration,odbc_manager' ./internal/modules/datasets -run '^TestMongoDamengActual' -count=1
```

The second gate additionally requires CGO, the actual admitted native driver and
restricted isolated DM fixture, `PROJECT_ONE_REPOSITORY_ENGINE=dameng`, the shared
fixture's protected native configuration, `PROJECT_ONE_DATA_PYTHON`, and the
existing protected `PROJECT_ONE_MONGODB_TEST_SOURCE_FILE` selecting the real
eight-row synthetic loopback MongoDB source. The common fixture currently needs
its native implementation integrated; no silent skips/fallbacks are provided.
No real customer data is needed. The existing PostgreSQL/native-MongoDB quality
gate must also be rerun because both paths now share the upload fence and explicit
source close outcome. The UUID cast fixes `$12::uuid::text` and `$4::uuid::text`
are retained in the PostgreSQL statements.

Unit sources exercise slot contention/release/error sequencing, unknown commit
priority, receipt/proof bounds and captured protected-source immutability. The
actual native gate exercises real import through immutable publication, an
acknowledgement fault after a real committed import (without editing terminal
rows), idempotent/audit readback, SQL guard rejection and fresh readback,
membership/source revocation, source revocation during draft copying, and real
session contention/cancellation/reacquisition. These tests are authored, not run
by this lane. Syntax, triggers, native grants and qualification remain pending.

Official references checked for this source design: [DM DBMS_LOCK](https://eco.dameng.com/document/dm/zh-cn/pm/dbms_lock-package.html),
[DM DBMS_LOB](https://eco.dameng.com/document/dm/zh-cn/pm/dbms_lob-package.html),
[DM JSON](https://eco.dameng.com/document/dm/zh-cn/pm/json),
[DM triggers](https://eco.dameng.com/document/dm/zh-cn/pm/trigger),
[MongoDB Go transactions and sessions](https://www.mongodb.com/docs/drivers/go/current/crud/transactions/).
