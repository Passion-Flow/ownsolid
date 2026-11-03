# Native Dameng object storage integration

Source contract at base `c3a0c8ff586ceed0e356206831f614d7e6326857`.
This owner implements native repository statements for the existing M22 service.
The native schema contribution is included; central registration, composition
activation and the actual shared native fixture remain integration prerequisites.
No Dameng execution or product
qualification is asserted by this document.

## Execution and recovery

The existing `objectstore.S3` and protected credential/staging owners still perform
real multipart creation, part upload, completion, whole-object digest/length
verification, authorized streaming read, reconciliation and cleanup. This module
adds no remote client, credential source or deployment setting. Configuration
continues through `config.ObjectStorageConfiguration`, its existing environment
mapping and the selected shared database connector.

`repository.go` selects independent statements for all three dialects and refuses
an unknown dialect. The native branch uses quoted uppercase relation/column
names, positional business parameters and no compatibility-mode fallback.
There are 71 production selector callsites, including one four-table metadata
triplet loop and one explicitly unreachable native bulk-UUID branch. Restore
uses two additional native statements to read bounded rows and update each row.

Current session, actor epoch, installation tenant, Workspace membership, grant
issuer/recipient, license and object provenance checks remain on the same owners.
Native installation authority takes `FOR UPDATE` in the existing write
transaction; it does not add locking SQL to a read-only transaction or introduce
`FOR SHARE`. Preserve the existing actor/session, Workspace, installation and
object lock order across other native owners.

Native limits are validated existing integers in 1..100 and rendered using
`strconv.Itoa`; all request values remain bound. Restore uses the existing fixed
maximum page size. Empty UUID cursors bind SQL NULL; populated cursors retain the
validated canonical UUID, so empty-string compatibility settings cannot change
first-page behavior. There are no timestamp cursors in this module.

JSON actor/input/receipt values bind as text for CLOB columns. SHA-256 fingerprints
remain nonempty binary 32-byte values. Nullable UUIDs/timestamps continue through
the shared native driver. Required shared conversions are its existing bool/BIT,
integer, string/CLOB, byte slice/VARBINARY, nullable pointer and timestamp support;
this batch adds no new shared conversion.

All database-generated instants use
`SYS_EXTRACT_UTC(SYSTIMESTAMP(6))`; durations use
`NUMTODSINTERVAL(?,'SECOND')`. Bound instants must already meet the native UTC,
1970..9999 and exact microsecond contract; never truncate them to make binding
succeed. See the official [DM functions reference](https://eco.dameng.com/document/dm/zh-cn/pm/function)
and [query reference](https://eco.dameng.com/document/dm/zh-cn/pm/check-phrases).
Function/statement execution against the selected DM version remains a gate.

A SQL failure returns immediately, allowing the owning transaction to roll back
and the poisoned native connection to retire. There is no SELECT or automatic
retry on that connection after unique/check conflicts. A caller reconciliation
uses a new transaction and the original creation key/fingerprint or retained
operation ID. Commit-unknown returns no successful object/grant/ingestion
projection; it never authorizes another external mutation.

Database restore revokes grants, reads pending objects in bounded UUID order,
closes result consumers, then assigns a distinct application-generated canonical
UUID per pending operation. The old operation ID is in the update predicate.
It preserves remote upload/ETag, source digest and destination receipts; it marks
the operation sent with `OBJECT_RESTORE_RECONCILE_REQUIRED`. Any SQL/scan/row-count
failure exits the borrowed restore transaction. No old worker can acknowledge
using its retained operation ID after a successful restore transaction.

Dataset ingestion still calls the real `Datasets.GetDraft`, `CreateDraft` and
`Upload` APIs. The ingestion ID remains the destination idempotency key.
Tenant/Workspace/dataset, creator, expected length and source digest must match.
The target is durably recorded before streaming; source verification precedes
the submitted receipt. Resume reads that same target; it must preserve later
operator edits and must not create a second draft or recopy already submitted
source bytes.

## Native schema contribution and ordering

`../../platform/database/dameng/migrate/contribution_artifactstores.go` contains
the complete native definition in the single explicitly delegated shared file:

- `artifactStoresFragment(current)` / `artifactstores_v2` requires
  `foundation_v1`; it returns four tables, two indexes and six object/part/grant
  INSERT/UPDATE triggers (12 changes).
- `artifactStoresTargetsFragment(current)` / `artifactstores_targets_v2`
  requires `artifactstores_v2`, `datasets_v2` and `models_v2`; it returns three
  separate scoped-FK ALTERs with exact Before/After shapes and two ingestion
  INSERT/UPDATE triggers (5 changes).

Register the second fragment after both target owners. This explicit later ALTER
phase resolves an artifact -> dataset/model -> artifact cycle without deleting
FKs or inventing placeholder tables. The intermediate first phase is not a
serving schema; central readiness must require the entire registered catalog.
The FK helper discovers the exact existing scoped primary/unique constraint name
by its ordered columns; absence causes catalog dependency validation to fail.
It never substitutes an ID-only key. There are no central registrations, step
numbers, DDL executions or modifications to the historical 31 steps in this lane.

LOB immutability uses native `DBMS_LOB.COMPARE` with explicit null handling,
not scalar CLOB comparison or a compatibility-mode change. Native triggers use
separate events, SELECT COUNT INTO for cross-row checks and
`RAISE_APPLICATION_ERROR(-20022,'OBJECT_STORED_PROVENANCE_INVALID')`.
See the official [DM LOB package](https://eco.dameng.com/document/dm/zh-cn/pm/dbms_lob-package.html)
and [trigger syntax](https://eco.dameng.com/document/dm/zh-cn/pm/trigger).
Actual syntax, dictionary readback, error normalization and guard execution are
still native gates. JSON bytes are immutable in DM; reserialization is not an
authorized input rewrite.

## Exact schema contract

The authoritative baseline is
`services/control/migrations/postgresql/000023_artifact_objects.up.sql`,
SHA-256 `9cb1e059ab5b316685afebf970ba1c8e186a99a8011a987d90181e898ae904cd`.
The native contribution preserves its four tables, two indexes and four logical
before-insert/update guards as eight single-event native triggers. The definitions
below describe that authored contribution, not executed native DDL.

Use quoted uppercase identifiers. Every UUID below is canonical nonzero text
with exact 36-character representation and case-sensitive equality. Every
timestamp is UTC `TIMESTAMP(6)`; creation/update defaults must use UTC rather than
session-local time. Preserve nullability below. String limits are character
limits where the source is varchar, with UTF-8 byte limits enforced separately
for JSON. Do not silently narrow Unicode names to byte length.

| Table | Required columns (question mark means nullable) |
| --- | --- |
| ARTIFACT_OBJECTS | UUID: ID, TENANT_ID, WORKSPACE_ID, CREATED_BY, MEMBERSHIP_AUTHORITY?, EXECUTION_MEMBERSHIP?, CREATION_KEY, STORE_ID, OPERATION_ID?. BIGINT: REQUESTED_EPOCH, EXECUTION_EPOCH, EXPECTED_BYTES, PART_BYTES, REVISION default 1. VARBINARY(32): FINGERPRINT. Text: REQUESTED_AUDIENCE(8), NAME(200), PURPOSE(24), SHA256(64), STATE(16), REMOTE_UPLOAD_ID(4096)?, REMOTE_ETAG(1024)?, OPERATION_KIND(16)?, OPERATION_PHASE(16)?, ERROR_CODE(64)?. CLOB: OPERATION_AUTHORITY?. Timestamps: OPERATION_EXPIRES_AT?, UPLOAD_EXPIRES_AT, EXPIRES_AT, CREATED_AT default UTC now, UPDATED_AT default UTC now. |
| ARTIFACT_OBJECT_PARTS | UUID: TENANT_ID, WORKSPACE_ID, OBJECT_ID, STAGE_ID. INTEGER: NUMBER. BIGINT: BYTES. Text: SHA256(64), ETAG(1024)?, STATE(16). |
| ARTIFACT_OBJECT_GRANTS | UUID: ID, TENANT_ID, WORKSPACE_ID, OBJECT_ID, RECIPIENT_USER_ID, CREATED_BY, CREATOR_MEMBERSHIP?, RECIPIENT_MEMBERSHIP, CREATION_KEY. BIGINT: CREATOR_EPOCH, RECIPIENT_EPOCH. Text: ACTION(24). VARBINARY(32): FINGERPRINT. Timestamps: NOT_BEFORE, EXPIRES_AT, REVOKED_AT?, CREATED_AT default UTC now. |
| ARTIFACT_OBJECT_INGESTIONS | UUID: ID, TENANT_ID, WORKSPACE_ID, OBJECT_ID, CREATED_BY, CREATION_KEY, MODEL_IMPORT_ID?, DATASET_DRAFT_ID?, TARGET_DATASET_ID?. VARBINARY(32): FINGERPRINT. Text: KIND(16), STATE(16), SOURCE_VERIFIED_SHA256(64)?, ERROR_CODE(64)?. CLOB: INPUT, RECEIPT?. Timestamps: SOURCE_VERIFIED_AT?, CREATED_AT default UTC now, UPDATED_AT default UTC now. |

Required keys and references:

- Objects: PK(ID); unique(TENANT_ID,WORKSPACE_ID,ID) and
  unique(TENANT_ID,WORKSPACE_ID,CREATED_BY,CREATION_KEY); scoped Workspace FK;
  CREATED_BY -> USERS(ID).
- Parts: PK(OBJECT_ID,NUMBER); composite object FK
  (TENANT_ID,WORKSPACE_ID,OBJECT_ID) -> objects(TENANT_ID,WORKSPACE_ID,ID).
- Grants: PK(ID); unique(TENANT_ID,WORKSPACE_ID,OBJECT_ID,ID) and
  unique(TENANT_ID,WORKSPACE_ID,OBJECT_ID,CREATED_BY,CREATION_KEY); scoped object
  FK; recipient and creator FKs to USERS(ID).
- Ingestions: PK(ID); unique(TENANT_ID,WORKSPACE_ID,OBJECT_ID,CREATED_BY,CREATION_KEY);
  scoped object FK and creator FK; (TENANT_ID,WORKSPACE_ID,TARGET_DATASET_ID)
  -> DATASETS(TENANT_ID,WORKSPACE_ID,ID);
  (TENANT_ID,WORKSPACE_ID,MODEL_IMPORT_ID) -> MODEL_IMPORTS(TENANT_ID,WORKSPACE_ID,ID);
  (TENANT_ID,WORKSPACE_ID,TARGET_DATASET_ID,DATASET_DRAFT_ID)
  -> DATASET_DRAFTS(TENANT_ID,WORKSPACE_ID,DATASET_ID,ID).
- Indexes: objects(TENANT_ID,WORKSPACE_ID,CREATED_AT DESC,ID DESC);
  grants(TENANT_ID,WORKSPACE_ID,RECIPIENT_USER_ID,OBJECT_ID).

Required checks and guards (retain the source's full null-safe comparisons):

- Objects: positive epochs/revision; expected bytes 1..5497558138880; part bytes
  5242880..5368709120; ceiling part count <=10000; lowercase 64-hex digest;
  fingerprint exactly 32 bytes; audience admin/portal; purpose
  model_bundle/dataset_source. States: creating, uploading, completing, ready,
  deleting, deleted, aborting, aborted. Operation kind:
  create/part/complete/delete/abort/ingest; phase prepared/sent. All five operation
  fields (ID/kind/phase/expiry/authority) are null together. Ready needs ETag.
  EXPIRES_AT >= UPLOAD_EXPIRES_AT. Insert must be creating with prepared create,
  no remote identity and execution authority equal to requested authority.
  Identity/request/scope/content/creation provenance is immutable; remote upload
  and ETag cannot change after first set; expiry cannot increase; revision cannot
  decrease; terminal states cannot acquire operations. Retain the exact state
  transition graph in the source guard.
- Parts: number 1..10000, bytes 1..5368709120, 64-hex digest, intent/uploaded and
  uploaded iff ETag is nonnull. Insert requires the exact scoped uploading object
  with remote upload ID, no active operation, valid part offset and exact expected
  length. Identity, size, digest and stage ID are immutable; uploaded is terminal
  with immutable ETag.
- Grants: read/ingest_model/ingest_dataset only; fingerprint exactly 32 bytes;
  EXPIRES_AT > NOT_BEFORE. Insert must be unrevoked on the scoped ready object,
  expiring no later than that object. All issuance provenance/time bounds are
  immutable; a nonnull revocation instant cannot change.
- Ingestions: model/dataset and intent/running/submitted/failed only; fingerprint
  exactly 32 bytes; INPUT and any RECEIPT are JSON objects <=16384 UTF-8 bytes.
  Model rows cannot carry dataset IDs; dataset rows require TARGET_DATASET_ID and
  cannot carry MODEL_IMPORT_ID. Submitted requires the matching target.
  Insert starts intent without target output, verification, receipt or error.
  Identity/input/requested destination is immutable; set output IDs and source
  proof cannot change; submitted/failed rows are wholly immutable. Allowed
  transitions are intent -> running/failed and running -> submitted/failed.
  Referenced model/draft must match scope, creator and expected bytes; draft must
  also match dataset and source digest. Digest/time verification are null
  together, must match the source object, and submitted also requires receipt.

The serving role needs SELECT/INSERT/UPDATE on objects, grants and ingestions,
plus SELECT/INSERT/UPDATE/DELETE on parts. Related identity/Workspace/audit/model/
dataset grants stay with those owners. No serving DDL privilege is requested.
Schema rejection must not be waived by successful LIMIT 0 metadata probes.

## Coordinator integration and verification

1. Integrate and register both supplied fragments with restricted grants.
   Integrate native identity, Workspace, licensing, model and dataset owners.
   NewService still performs their real stored-state/readiness checks.
2. Only after that integration, review `dameng-root.patch` for the composition
   gate. It is a proposed one-condition change, never applied by this lane.
   The coordinator must reconcile current schema manifests and source evidence.
3. Extend `internal/testing/repositoryfixture/fixture.go` with an explicitly
   selected protected native fixture and real native seeding/cleanup. Its
   current New and Statement support only PostgreSQL/MySQL. This batch updates
   all 51 M22 fixture selector/helper callsites with explicit native statements
   and positional/JSON bindings, bypassing its old two-dialect Statement method.
   The real shared native New/seed/cleanup branch still must be integrated.
4. Run focused units first, then serialize actual native schema/constraint,
   multipart, current-authority read, dataset output and uncertain-outcome gates.
   Actual native runs also require the admitted cgo/odbc_manager build profile
   and retained native library inventory; an untagged build is not that profile.
   The existing paired/output wrappers still select PostgreSQL/MySQL only;
   adding native selection to their admission/build/source receipts is a shared
   runner change, not an environment-variable trick.

Focused command for the coordinator, not run in this source-only lane:

```text
go -C services/control test ./internal/modules/artifactstores
```

After native fixture/runner activation, compile and execute the applicable tagged
suites with the protected runner-owned profile and disposable synthetic schema:
`repository_integration,object_storage_integration` for
`TestPairedPrivateObjectStorage`, plus `object_storage_output_integration` for
`TestActualPrivateObjectStorageOutputs`. Exact existing procedures are in
`docs/milestones/m22-object-storage/paired-runbook.md` and `output-runbook.md`.
There is intentionally no claimed runnable native wrapper command before the
shared runner exists. The existing
`license_observation_uses_single_connection` leaf is explicitly PostgreSQL-only
and remains so; select the native-eligible leaves individually. Native
single-connection admission/readback needs the shared driver's real pool gate;
do not count that PostgreSQL leaf as native coverage or silently skip it.

Mandatory native cases: exact multipart/whole-byte digest readback; wrong-scope,
expired/revoked grant and changed actor/recipient authority refusal; immutable
database guards; original-key replay with conflict rollback and fresh-transaction
readback; unknown receipt returning no success and preserving the one actual
dataset target and subsequent edits; sent-create/complete/part reconciliation
without duplicate effects; restore invalidating old worker IDs; row/scan/query
failure stopping all native SQL; non-UTC server timezone with UTC results;
empty/nonempty UUID cursor pages; text JSON and binary fingerprint round trips.
Fault wrappers are not evidence of a real dropped transport acknowledgment.

Retain schema/source/driver identity, exact case and outcome receipts. Clean only
the owned synthetic object/upload/fixture inventory after readback and consumer
shutdown. No bucket-wide purge, shared database deletion or real data upload is
authorized here. Missing schema, fixture or owner integration is an implementation
prerequisite; only actual unavailable target qualification may be target-pending.
