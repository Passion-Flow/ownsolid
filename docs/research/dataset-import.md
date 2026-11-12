# Dataset source import (DAT-101)

## Product and source review

Reviewed 2026-11-08. DAT-101 owns the intake boundary. The data roadmap already
separates training schemas, cleaning previews, deduplication, leakage-safe splits,
version lineage, tokenizer previews, sensitive data, quality gates, and database
sources. Keep intake independent of those policies. [Hugging Face Datasets
loading](https://huggingface.co/docs/datasets/en/loading) accepts local files in
these formats and [streams datasets](https://huggingface.co/docs/datasets/main/stream)
without materializing them. [SageMaker training input channels](https://docs.aws.amazon.com/en_en/sagemaker/latest/dg/model-train-storage-env-var-summary.html)
bind training inputs to selected objects. Use the existing scoped object-store
transport and the [S3 conditional GetObject contract](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html)
to fetch one authorized, immutable object identity. Never derive a bucket, host,
credentials, or authorization from an upload request.

The inherited processor reads JSONL training records with exactly three fields
and accumulates parsed rows for a strict result contract. Its UI, SQL draft
materialization, and generated limits cap sources at 64 MiB and 10,000 rows. Use
a separate Go intake boundary that commits private JSONL without accumulating
rows. The legacy draft is a consumer of smaller training-shaped data, and its
wire schema and durable behavior remain compatible. Publishing a 50 GB generic
dataset via that draft or from the Console requires changes outside Lane F.

## Formats and recovery

- [JSON Lines](https://jsonlines.org/) provides physical record boundaries.
  Validate a bounded line and report its 1-based position. Discard the remainder
  of an oversized line in bounded chunks.
- A [JSON array](https://www.rfc-editor.org/rfc/rfc8259) streams one element at a
  time with a per-element read budget. Records are objects with unique member
  names. Malformed array framing is fatal. An independently decoded non-object
  element can be skipped.
- [RFC 4180](https://www.rfc-editor.org/rfc/rfc4180) and [Go's CSV
  reader](https://pkg.go.dev/encoding/csv) define quoting, escaped quotes and
  multiline fields. Require a unique, non-empty header. Preserve textual values
  such as leading zeroes. Empty cells are null. Accept UTF-8 with or without its
  BOM. Accept explicitly selected GBK using the [WHATWG decoder mapping](https://encoding.spec.whatwg.org/#gbk)
  and reject replacement characters produced by invalid legacy byte sequences.
  An absent encoding declaration selects strict UTF-8. Two byte sequences can
  be valid in both encodings with different meanings, so guessing GBK risks
  silent data corruption. An invalid encoding, an invalid header, a record that
  exceeds the CSV read budget, or broken quoting is fatal. A complete record
  with a mismatched field count may be skipped. CSV errors use physical lines.
- [Parquet](https://parquet.apache.org/docs/file-format/) has a footer and
  independently encoded row groups. Stage the source once in a private disk
  file so a non-seekable upload or object can be read with random access.
  [Arrow's ParquetFile](https://arrow.apache.org/docs/python/generated/pyarrow.parquet.ParquetFile.html)
  iterates bounded batches without memory mapping or prefetch. Bound the footer
  and declared uncompressed row-group size, and run the native decoder in a
  separate process with an address-space limit on Linux. Arrow reports logical
  types. Normalized JSON carries binary as base64, decimal as exact text and
  temporal values as exact integer counts in the reported Arrow unit. A corrupt page/row group aborts
  the file and reports the first row of the unread batch.
- Reject on the first invalid row by default. Fine-tuning inputs are hard to
  reconstruct if data is dropped implicitly. Opt-in skip records the total
  rejected count and a bounded list of positions and codes. A source with no
  accepted row still fails. Fatal transport, framing, encoding, and resource
  errors abort either policy. Reports include raw bytes, accepted and rejected
  rows, top-level field count, types, and per-field null totals including absent
  JSON keys. The staged output becomes visible only after complete validation
  and a successful exclusive commit.

A whole-file JSON/Parquet decoder or in-memory spool fails the memory bound.
A bespoke Parquet decoder has too much codec and malformed-input surface.
[parquet-go](https://github.com/parquet-go/parquet-go) is Apache-2.0 and offers
incremental row reads, but would add a shared Go dependency outside the lane and
would run native-format parsing inside control. Use the existing Python runtime
as the location for a separately configured Arrow worker.

## Dependency and redistribution review

- [Go standard library](https://go.dev/LICENSE) and [golang.org/x/text v0.43.0](https://github.com/golang/text/blob/v0.43.0/LICENSE)
  use BSD-3-Clause. Keep copyright, licence and disclaimer in distributions.
- Existing [AWS SDK for Go v2](https://github.com/aws/aws-sdk-go-v2/blob/main/LICENSE.txt)
  and [Smithy Go](https://github.com/aws/smithy-go/blob/main/LICENSE) use Apache-2.0.
  The configured object transport already owns these dependencies.
- [CPython 3.12](https://docs.python.org/3.12/license.html) uses the PSF licence
  and ships third-party notices. [PyArrow 26.0.1](https://pypi.org/project/pyarrow/26.0.1/)
  uses [Apache-2.0](https://github.com/apache/arrow/blob/apache-arrow-26.0.1/LICENSE.txt).
  Use pinned binary wheels and retain the wheel's licence and bundled library
  notices, including applicable NOTICE and source-offer terms. These licences
  permit commercial distribution and offline operation. The worker uses a
  private local descriptor and makes no download at runtime. The release image
  owner must admit and package the target wheels before enabling it.
- Tests generate synthetic examples and large fixtures locally. No third-party
  training data or model is shipped.

## Verification

Exercise all four formats through the intake boundary, including multiline
CSV with a UTF-8 BOM, explicit GBK and unsupported encoding, bad JSONL rows
under both policies, malformed array/CSV framing, and bounded diagnostics and
fields. Check output contents, failed-attempt cleanup, cancellation, source
identity and no overwrite. Use the S3 transport against a TLS protocol fixture
with conditional GET and truncated/mismatched object cases. Measure fresh
process peak RSS at two actual file sizes above and below 100 MiB. Keep
production route, UI, persistence and offline-image qualification separate from
the intake test evidence.

The disconnected Linux arm64 run used a two-CPU container and PyArrow 26.0.1
installed from a local wheel. The JSONL regression imported actual files of
16,777,216 bytes (4,096 rows) and 134,217,728 bytes (32,768 rows). The two fresh
processes peaked at 25,411,584 and 27,693,056 resident bytes. The regression
fixes Go CPU parallelism and uses the default GC pacing with no heap cap so
host-wide concurrency does not obscure the size comparison.

## Offline decoder package for Linux amd64

The control image uses CPython 3.12 on Debian Bookworm. The
[PyArrow 26.0.1 release inventory](https://pypi.org/project/pyarrow/26.0.1/#files)
lists a CPython 3.12 x86-64 wheel with a glibc 2.28 minimum and no required
Python packages. The [manylinux platform contract](https://peps.python.org/pep-0600/)
defines the glibc compatibility rule. The runtime lock admits one exact
wheel by SHA-256. The connected release builder stages it and the offline
image build installs from that directory with the index disabled.
[pip download](https://pip.pypa.io/en/stable/cli/pip_download/) can select
the release target from a different build host, and
[pip hash-checking mode](https://pip.pypa.io/en/stable/topics/secure-installs/)
checks the artifact during staging and installation. Keep wheel licence and
notice files in the installed package.

Use the binary wheel so the offline build needs no C++ toolchain, source
resolver, or external Arrow libraries. The previous Alpine arm64 fixture
proves the decoder on that development target. Its musl wheel is incompatible
with the Debian amd64 release base. Qualification for the release wheel
requires an index-disabled install from the lock, a rejected modified wheel,
and the bounded decoder test under the control image's non-root UID.

## Bind the normalized artifact

The source digest identifies the original bytes. A durable registration must
also identify the normalized JSONL bytes so integrity checks and backup can
verify the stored artifact. Use Go's [MultiWriter](https://pkg.go.dev/io#MultiWriter)
to compute the normalized SHA-256 as the existing file buffer flushes. The
report gives the output digest after the exclusive destination link exists.
On an uncertain directory sync, the caller can reconcile the linked file
against that digest. A second pass would add a full read of a potentially
50 GB output, while returning a digest before the exclusive link could
describe a file the caller has no authority to adopt. Tests compare the
reported digest with the published bytes and require empty output digests on
parse failure and destination conflict.

The Linux amd64 control base image passed the import suite on the native CPU
test host with networking disabled and UID 10001. The lock installed
PyArrow 26.0.1, `pip check` reported no dependency problems, and the wheel's
LICENSE and NOTICE were present. A one-byte modification to the staged wheel
was refused by the offline hash check. For the 16 MiB and 128 MiB JSONL files,
both import processes peaked at 26,738,688 resident bytes. The release
control image still needs to consume the lock and staged wheelhouse.

## Registration and product handoff

The existing dataset row can remain the workspace authority. Reuse the
[dataset service transaction](../../services/control/internal/modules/datasets/service.go)
to resolve and recheck the workspace and actor. Use a separately named import
resource for the generic artifact. Existing drafts cap inputs at 64 MiB and
their worker interprets a fixed prompt/completion schema. Existing versions
require a draft, a publication job and attempt, and a legacy split manifest.
An S3 ingestion receipt requires a draft foreign key on submission and limits
its JSON receipt to 16 KiB. None of these authorities can hold the generic
report and artifact without changing their current contracts.

1. **Registry and backup.** Add a scoped import reference with a composite
   tenant, workspace, and dataset foreign key, a scoped idempotency key and
   fingerprint, exact source and output byte counts and SHA-256, a bounded
   report and the existing store identity. Reserve an attempt, run
   `SourceImporter` outside the SQL transaction, then recheck the actor,
   membership lifetime, dataset status and attempt before committing the
   report and audit event together. Read the report using the same workspace
   authority and reject references outside that scope. Reconcile unknown
   commit outcomes by key and digest. Keep the existing
   [store and integrity checks](../../services/control/internal/modules/datasets/integrity.go)
   and [snapshot inventory](../../services/control/internal/modules/datasets/snapshot_inventory.go)
   as the owners of file verification. Extend them with the new reference
   and test snapshot/restore with an artifact over 64 MiB. The
   [PostgreSQL constraints](https://www.postgresql.org/docs/current/ddl-constraints.html)
   and [transaction contract](https://www.postgresql.org/docs/current/tutorial-transactions.html)
   support the scoped foreign key and atomic report/audit commit. Assign
   the next contiguous migration from current main at integration time.
   The [release lineage descriptor](../../api/release/v1/mvp-postgresql-cpu-v1-lineage.json),
   [release schema constants](../../api/release/v1/postgres_application.go)
   and backup/restore inventory must advance with that migration.
2. **API and S3 authority.** Add an import/upload and report resource under
   the existing dataset scopes in the [dataset handler](../../services/control/internal/modules/datasets/http/handler.go)
   and [OpenAPI contract](../../api/openapi/v1/datasets/openapi.json).
   Update the upload deadline and error mapping used by the authenticated
   [control HTTP dispatcher](../../services/control/internal/modules/identity/http/handler.go).
   For S3, use the current grant and exact ETag, byte count and SHA-256 from
   the [artifact owner](../../services/control/internal/modules/artifactstores/ingestion.go).
   This preserves the current legacy ingestion path. Configure the importer
   and its independent source and output budgets through the
   [control startup owner](../../services/control/cmd/server/runtime_lifecycle.go)
   and [typed dataset configuration](../../services/control/internal/platform/config/datasets.go).
3. **Offline image and UI.** Pass the [locked wheelhouse](../../runtimes/data-processing/README.md#parquet-source-intake)
   to the [control image build](../../deploy/images/control/Dockerfile) as
   a staged build input, install before switching to UID 10001, and retain
   licence files. Wire the S-503 screen in the
   [product plan](../product/plan/README.md#stage-5--run-it) to the authorized
   report and import lifecycle. Send no data to the browser before scope
   admission. Regenerate the client after the OpenAPI change with `make generate`.

From the workspace root, rerun the component tests with the locked wheel
installed into a protected Python 3.12 runtime:

```sh
TMPDIR="$(cd "${TMPDIR:-/tmp}" && pwd -P)/" \
  OWNSOLID_TEST_PARQUET_PYTHON="$dataset_python" \
  go test ./services/control/internal/modules/datasets/... -count=1 -v
```

With the local PostgreSQL and Python settings in
[development environment](../development/environment.md), also rerun:

```sh
go test -p 1 -tags dataset_processor_integration \
  ./services/control/internal/modules/datasets/... -count=1
make check
```

These existing commands verify components and the inherited workflow. The
new route still needs an offline installation test that imports from upload
and S3, reloads the report after restart, rejects another workspace,
reconciles retries, and restores the registered artifact and report together.

## Interrupted transfers

The [Go Reader contract](https://pkg.go.dev/io#Reader) permits data and a
non-EOF error in the same read. Buffered parsers can consume the bytes before
surfacing the error and use different syntax errors for the same interrupted
body. A conditional [GetObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html)
matches the selected object at request time. A successful response still
requires reading the entire body and checking its receipt.

Record a non-EOF source error at the input boundary. Preserve the timeout and
resource limit classifications. Otherwise surface the stable source-read code,
keep any existing exact line diagnostic, and discard the private candidate.
Keep parser errors for complete inputs so users can distinguish correcting
the file from retrying the transfer. Do not automatically retry an arbitrary
reader or stage a second copy of a large text source. The change uses the Go
standard library and synthetic local fixtures only.

Exercise interruptions before and after a valid prefix with skip mode enabled.
Assert that the importer withholds previews and output, then accepts a fresh
complete transfer to the same destination.

The Linux amd64 text and S3 intake tests passed in a network namespace with
loopback only. The 16 MiB and 128 MiB cleaning runs both peaked at 28,000,256
resident bytes. The fixtures and their normalized outputs were removed when
the tests exited.

## NET-101 snapshot handoff

The portable copy from NET-101 commit `e03bbbab` covers only the
[artifact-store inventory](../../services/control/internal/modules/artifactstores/backup_fence.go).
It binds the SQL database, tenant, store namespace and backend fingerprint to
settled `artifact_objects` rows. A ready row contributes
`objects/<workspace_id>/<artifact_object_id>` to the archive. The copy
verifies its revision, ETag, byte count and SHA-256, and returns the
inventory digest plus the archive size and SHA-256 after readback.

The dataset draft in candidate `8d7a6363` retains two separate files in
the private datasets store. Its `dataset_draft_import_candidates` row
joins `dataset_drafts` by tenant, workspace, dataset and draft IDs. Use
the draft's `source_object_id` for both relative paths.

| Dataset file | Expected size and SHA-256 |
| --- | --- |
| `objects/<source_object_id>/source.parquet` | Candidate report `source_bytes` and `source_sha256` |
| `objects/<source_object_id>/source.jsonl` | Candidate report `output_bytes` and `output_sha256`, equal to the draft's `expected_bytes` and `source_sha256` |

The candidate also checks `source_format = parquet`. Neither private
path is an `artifact_objects` entry. The S3 upload used at ingress may
have its own NET-101 row, but copying it does not prove custody of the
draft's private original or normalized bytes. The existing dataset file
snapshot already references the normalized bytes and omits the original.
Keep the NET-101 scope checks intact.

The REL-103 [object checkpoint](../decisions/0068-bind-external-objects-to-the-logical-backup.md)
already binds the verified PAX copy to the logical backup and its protected
file inventory. Add the two dataset references on that logical side. Keep
the same checkpoint format and trusted operation. The coordinator can
publish both owner manifests with one PostgreSQL dump under a common
writer-exclusion window. A
[shared PostgreSQL snapshot](https://www.postgresql.org/docs/18/functions-admin.html#FUNCTIONS-SNAPSHOT-SYNCHRONIZATION)
can synchronize SQL readers. The coordinator must still exclude writes
to both object stores until their verified inventories and the dump
are bound together. After the ARC audit and GW-120 migrations, admit
the candidate table and the original in datasets `snapshotReferences`
and `referenceFiles`. Bind the SQL dump digest, dataset file snapshot
digest, NET-101 `inventory_sha256` and portable archive size and SHA-256
before publication. On restore, verify both candidate files and the
scoped row before ready. OBS can use these verified manifests for
acceptance without rescanning a general-purpose bucket.

The offline fixture stays on candidate `8d7a6363`. Install its pinned
decoder and run the existing PostgreSQL and signed HTTPS replay:

```sh
bash services/control/internal/modules/datasets/testdata/replay_draft_import.sh \
  "^TestActualParquetDraftAfterMetadataAndObjectRestore$"
```

This fixture has exercised restored PostgreSQL, the normalized snapshot,
a signed fetch of the original, scope denial and corrupt or missing
files. Run the same assertions against the coordinated restore once
its trusted datasets inventory admits both paths. The current
NET-101 copy cannot be used for that replay.

## Scoped CSV draft candidate

The released draft stores one source object and one receipt. The existing
inspection worker interprets that object as JSONL. The released schema has
no dataset-owned slot for a second immutable source and a generic import
report. The isolated PostgreSQL candidate creates a real editable draft and
its existing inspection job, with an additional scoped report table supplied
only by the module test fixture. It uses the existing store and writes UTF-8
CSV into a private UUID directory as the parser consumes it. The existing
`SourceImporter` writes the corresponding JSONL into the same directory.
Both files must be synced before the database can publish a reference.

The [Go streaming reader contract](https://pkg.go.dev/io#TeeReader) supports
writing exact input bytes without reading the entire file into memory.
[PostgreSQL transactions](https://www.postgresql.org/docs/18/tutorial-transactions.html)
make the draft, its job, the import report, and the audit visible together.
A [composite foreign key](https://www.postgresql.org/docs/18/ddl-constraints.html#DDL-CONSTRAINTS-FK)
binds the report to the same tenant, workspace, and dataset as the draft.
The candidate uses the [Go standard library](https://go.dev/LICENSE),
[pgx](https://github.com/jackc/pgx/blob/master/LICENSE), and
[PostgreSQL](https://www.postgresql.org/about/licence/) already in the
product. Their licenses allow commercial use, offline use, and redistribution
with their respective notices. The fixture has synthetic rows and adds no
dependency, dataset, or model.

The first flow accepts the legacy `source_id,prompt,completion` record shape
and uses the configured draft byte, line, and row limits. It streams and
validates records with a bounded working set. Authentication, dataset scope,
and delegated authority are checked before the request body is consumed and
again before committing. A completed idempotency key verifies both files and returns the same draft
and report. A changed source declaration conflicts. A competing attempt for
the same key is checked again under the existing dataset lock before any
insert. An interrupted stream, schema error, or known database rollback
removes that attempt's private files and exposes no report or preview.
If PostgreSQL cannot determine the commit outcome, retain that attempt's
sealed files and resolve the result by repeating the same key.

A JSON sidecar alone cannot give atomic publication with the draft, and
adding report data to a job policy or audit event would change their
contracts. The candidate adds no deployment migration, public API contract,
or default handler registration. The release/schema owner must adopt the
scoped table and both files into backup, restore and garbage collection before
enabling a production route. The shared API and S-503 owners must wire the
user flow. The legacy byte and row ceilings remain in force for this path.


The HTTP fixture registers the candidate explicitly and installs its table
after verifying the released catalog. With the isolated PostgreSQL and
CPython settings in [development environment](../development/environment.md),
run the actual TLS, upload, and draft inspection flow:

```sh
go -C services/control test -tags dataset_processor_integration -count=1 \
  ./internal/modules/datasets \
  -run '^TestActualDatasetTLSContractsLargeUnicodeRowsCSRFExportAndReadiness$'
```

The Linux amd64 fixture ran with loopback only. It checked CSRF, a real
second workspace, a failed stream with a valid prefix, rollback after a
report constraint failure, two competing requests for one key, original
bytes and digest, rejection of altered custody, and editable rows from the
existing CPython inspection worker.

## Scoped Parquet draft candidate

The Apache [file format](https://parquet.apache.org/docs/file-format/)
places metadata at the end of the file. The existing protected
[Arrow batch decoder](https://arrow.apache.org/docs/python/generated/pyarrow.parquet.ParquetFile.html#pyarrow.parquet.ParquetFile.iter_batches)
therefore needs a complete seekable file before emitting rows. Reuse the
same private original file for bounded staging, decoding and eventual
custody. This avoids a second full-size Parquet copy. Keep the locked
CPython 3.12 PyArrow 26.0.1 wheel from commit `166151b6` and the existing
Apache-2.0, PSF, and Go licence terms. The fixture uses two synthetic rows
and adds no dependency. The explicit route uses the registered
[Parquet media type](https://www.iana.org/assignments/media-types/application/vnd.apache.parquet).
CSV keeps its earlier UTF-8 contract.

The optional fixture-only `dataset_draft_import_candidates` table binds
`source_format` and the JSON report to the composite tenant, workspace,
dataset and draft key. Each report identifies exact original and output
byte counts and SHA-256 digests. The source UUID belongs to
`dataset_drafts.source_object_id`. Its directory contains
`objects/<source_object_id>/source.parquet` or `source.csv`, and the same
directory's `source.jsonl` remains the existing inspection input. Allow
independent requests with the same key and original receipt in distinct
authorized workspaces. Reject a valid dataset ID from the wrong workspace,
a changed declaration in the same scope, and any replay whose original or
normalized artifact fails its bound digest. A partial transfer, invalid
decoder input, or report insert failure must leave neither a draft nor a
report or private candidate. An uncertain commit retains the original
files for reconciliation. These paths retain the configured legacy draft
byte, row, and record limits.

The migration owner must register the receipt in the next authorized
schema and release lineage. Snapshot and restore must extend
`snapshotReferences` and `referenceFiles` in
[the dataset inventory](../../services/control/internal/modules/datasets/snapshot_inventory.go)
to enumerate both file roles from the same scoped receipt, and bind the
catalog and copied inventory in the restore checks in
[snapshot_import.go](../../services/control/internal/modules/datasets/snapshot_import.go).
The integrity owner must include both roles in
[VerifyStored](../../services/control/internal/modules/datasets/integrity.go).
Tests must restore the SQL receipt and both files together, then refuse
an omitted, altered, or cross-scope original before readiness. The fixture
migration is not selected by the production dispatcher or release catalog.
The image build still owns wheelhouse admission, and the shared API and
S-503 own the public upload and report flow.

### Reproduce the isolated handoff

Install the hash-locked wheel using the offline decoder instructions above.
Set `OWNSOLID_TEST_PARQUET_PYTHON` to that protected CPython 3.12 executable
and `PROJECT_ONE_DATA_PYTHON` to the protected draft processor interpreter.
Stage the documented pgvector image and the Go module cache locally. On the
Linux fixture host run:

```sh
bash services/control/internal/modules/datasets/testdata/replay_draft_import.sh
```

The script starts a disposable PostgreSQL 18 and pgvector instance with
loopback only. It creates the production base schema through the identity
fixture and adds [the draft receipt table](../../services/control/internal/modules/datasets/testdata/draft_import_candidate.up.sql)
only after the released readiness assertions. The TLS test generates a real
two-row Parquet file and verifies the stored originals, the bound inspection
results, rollback and replay. The script stops the container and removes its
connection file on exit. Use `.` as its first argument to run the full
tagged dataset suite.

The Linux amd64 PostgreSQL and HTTPS fixture passed for CSV and the
hash-locked PyArrow Parquet source with loopback only. Both cases left
independently scoped drafts and originals when the same request ran in a
second authorized workspace. Both replayed the report after inspection
and refused a modified original. An injected receipt constraint failure
rolled back the draft, job and audit rows and removed the private source.

## Restore acceptance for the isolated Parquet receipt

Use the existing owned PostgreSQL fixture's
[custom dump](https://www.postgresql.org/docs/18/app-pgdump.html) and
[restore](https://www.postgresql.org/docs/18/app-pgrestore.html) in a new
database. The test script gives its network-none container a unique cluster
name and passes its exact identity to the replay test. Reuse the dataset
file snapshot for the normalized input and the signed S3 TLS fixture for
the supplemental original. It provides the exact tenant, workspace, object
and ETag expected by the object transport. Both streams and the SQL archive
come from synthetic fixture data. PostgreSQL, the Go libraries, and the
existing Apache-2.0 PyArrow and S3 libraries retain the licenses above.
This test adds no runtime dependency or new dataset.

A restored receipt is read by draft ID using the normal workspace and draft
authorization. Verify the row's source format, both byte counts and SHA-256
digests, and the existing draft's normalized receipt before returning any
report. Share this verification with idempotent replay. Recover into a new
store with the original namespace marker, invalidate the restored account
sessions, and sign in again as the same person. Compare both the restored
draft and report with the original and refuse a real second workspace.
Require failure when either file or the report is missing or changed.
The ordinary file snapshot omits Parquet originals, so test read refusal
before the supplemental object has been restored as well.

This remains a module acceptance fixture. It does not change the
production backup manifest or apply restore operators for other domains.
The restore owner must join `dataset_draft_import_candidates` to
`dataset_drafts` by tenant, workspace, dataset and draft in the shared
migration order, include both object paths in `snapshotReferences` and
`referenceFiles`, and verify the pair with the receipt before marking the
restored installation ready. Repeat the same missing and corrupt cases at
the coordinated restore entry point, then bind the public S-503 report
read to the same authorization and custody checks.

On Linux amd64 with only loopback, the restored PostgreSQL 18.6 database
and a fresh login returned the original editing draft and identical
report. The fixture fetched the exact original through a signed S3 TLS
GET after importing the normalized snapshot. The old session, another
workspace, missing and same-length altered files, and missing and changed
report rows were refused. To run only this acceptance:

```sh
bash services/control/internal/modules/datasets/testdata/replay_draft_import.sh \
  '^TestActualParquetDraftAfterMetadataAndObjectRestore$'
```

## Site and cell scope for the candidate receipt

[ADR 0006](../decisions/0006-cell-and-site-scope.md) puts drafts and
their receipt in the cell. Workspaces and memberships remain global.
The request router first selects the cell. Within the candidate's
PostgreSQL transaction, use the authenticated tenant and workspace and
the database's `local_site_id()` and `local_cell_id()`. Lock the local
dataset before reading an import body. Select the receipt scope from
the exact locked local draft. Apply the same local restriction to reads
and idempotent retries.

Follow the [alias description candidate](gateway-model-description.md#site-and-cell-scope-for-edit-intents)
for nullable-field refusal and local defaults. PostgreSQL requires a
unique target for the
[full foreign key](https://www.postgresql.org/docs/18/ddl-constraints.html#DDL-CONSTRAINTS-FK).
Add a named unique key to `dataset_drafts` over tenant, workspace,
dataset, draft, site and cell, and reference all six values. Keep the
existing primary keys and ownership keys. A separate foreign key to the
cell registry would admit a valid foreign cell without binding the
receipt to the draft and would lock a shared row on every insert.

The up SQL starts with an empty receipt table. Older drafts already
carry their scope from 000030, and keep their metadata and normalized
files without an invented original or report. The down SQL removes the
candidate table and its parent key. ARC owns catalog and release
registration after 000058 and the GW-120 candidate on the actual tail.
The disposable replay can overlay those two SQL inputs on the published
predecessor. Restore the populated predecessor first, apply the
candidate, exercise the down SQL and compare the old draft. Then restore
the populated candidate into another database and recheck both the
foreign key and the authorized read.

Test a registered second cell at the same site and a different site,
explicit nulls, refusal before any foreign-dataset body read, and a
same-workspace foreign draft with a matching receipt. Inspect the
private dataset file inventory as well as the recovered bytes. The
normalized digest must match the receipt, while the Parquet original
remains a supplemental object in this replay. The coordinated dataset
inventory and NET-101 archive boundary described above remain separate.
No new dependency, license or dataset is introduced.

The isolated PostgreSQL 18.6 and PyArrow 26.0.1 replay on the predecessor
at `f83a9fde` ran the full tagged datasets suite. The restore test
applied the audit scope candidate from `4380fb94` and the alias
description candidate from `c9654657` as an overlay before the dataset
SQL. The concatenated overlay had SHA-256
`b8df9e9c7a6f90225f3ffdd8fd6ed7980834974e3451d159656783014ae34c58`.
On both the source and restored databases PostgreSQL rejected mismatched
site and cell with the named foreign key, and rejected explicit null scope.
The authorized restored read matched the original Parquet bytes, the
normalized JSONL and their separate report digests. The private snapshot
preserved the legacy JSONL and included the candidate's normalized
source path, size and digest. The test restored the Parquet original
as an explicit supplemental object and refused both missing and
same-length corrupted files.

For the full replay, set `OWNSOLID_TEST_DRAFT_RESTORE_PREDECESSOR_SQL`
to the absolute path to the trusted overlay assembled from those two
up SQL files. Set `OWNSOLID_TEST_PARQUET_PYTHON` and
`PROJECT_ONE_DATA_PYTHON` to the protected interpreter executables.
From the repository root run
`bash services/control/internal/modules/datasets/testdata/replay_draft_import.sh .`.
The runner creates its own network-isolated database. The replay does
not register a release migration or alter the coordinated backup inventory.

The focused replay on the `1881ae27` base also verified the restored
legacy JSONL byte for byte. It exercised an existing idempotency key on
a foreign draft while its dataset was still local, then tried a new key
against the foreign dataset. Both returned `DATASET_NOT_FOUND` before
reading the upload body.

## Production lineage integration

The immutable owner handoff is fa82645ca49811521872df90450dad858816b71f.
Its up script adds one receipt table and a six-column unique parent key,
without inventing receipts for existing drafts. Preserve those up bytes and
the existing draft keys. Select the next migration from the actual published
tail; schema 59 at 4f76102c is the current predecessor. Add the receipt to
the PostgreSQL application grants without changing the MySQL table list or
any preceding migration or release-lineage entry.

The fixture down script drops retained receipts. The formal down must first
select READ COMMITTED, then lock dataset_drafts and the receipt table in
ACCESS EXCLUSIVE mode. Refuse a nonempty receipt table with a stable restore
requirement before either DROP. A prior RR or SERIALIZABLE snapshot must fail
before destructive DDL. PostgreSQL's
[transaction isolation](https://www.postgresql.org/docs/18/transaction-iso.html)
and [SET TRANSACTION rule](https://www.postgresql.org/docs/18/sql-set-transaction.html)
require this ordering: an exclusive table lock does not refresh an old
snapshot. The already reproduced GW-120 rollback failure and qualified guard
provide the technical basis. Verify pristine rollback, retained receipts,
waiting committed writers, and both prior-snapshot isolation levels against
the actual numbered scripts. Preserve the original draft and receipt after
every refused downgrade.

Measure fresh, native predecessor upgrade and restored histories through the
new production migration twice, recording their actual catalog fingerprints.
Retain original dumps and audit checkpoints byte for byte. Verify same-role
grant resume, current scoped receipt DML and foreign-key refusal, and denial
of schema, private-ledger and audit-head writes. Runtime and schema acceptance
must use the same immutable source input. The PostgreSQL, Go and pgx licenses
above still apply; no dependency, model or dataset is added.

Schema registration alone does not enable the upload route. The source owner
still must admit both original and normalized roles into the production dataset
snapshot and integrity inventory. The existing snapshot omits the original
and the isolated restore supplies it separately. Keep production constructor
registration and the public contract/UI flow open until coordinated backup
and restore verify both files with their scoped receipt. The formal integration
must also adapt isolated test setup so the actual registered table is exercised
without applying the fixture DDL a second time; no scope or custody assertion
may be removed.


The independent PostgreSQL 18.6 prototype on a Linux arm64 network-none
fixture reproduced the original down deleting a retained receipt; its outer
transaction rolled back that reproduction. The guarded script passed ten
cases covering empty rollback, both retained formats, prior RR/SERIALIZABLE
snapshots, all four fresh isolation levels and a committed writer waiting at
the parent-table lock. The original and guarded log SHA-256 values are
6e06105a12c961462f1b79b71667dc948c39c4e0b361fe28cda1290ad3f478f1
and 949a473bda021d85c5fa42764e3d256779aa30db036145c48b52f3372407a8cb.
This prototype used a minimal parent schema, so full predecessor and runtime
qualification remain separate.

A second disconnected Linux arm64 fixture reused the accepted schema-59
migrator, original schema-55 through schema-59 dumps and audit verifier. It
measured the receipt DDL on full predecessor schemas, including restoration
of a receipt-bearing catalog. The exact prospective catalog fingerprints are:

| History | SHA-256 |
| --- | --- |
| Fresh, or native 58 migrated through 59 | 4999ec1b3e7ccad081e2290270860c362d23d56ba088441dee7869f734f608d7 |
| Restored 55, then migrated through 59 | f0178764888dc1f0ce36b96a98d4105af4a1b7d8c2c99fbec359f20c4ef7b813 |
| Restored 56, 57, 58 or 59; restored receipt catalog | 93b8967dbcc4b90c1692dcf67076192a8b97ead256335cf2a4fbccb6dcdf87b4 |

Each projection had 120 tables and the still-accepted 59-entry ledger.
Pristine down restored each exact predecessor catalog, and reapplying the
same up produced the same prospective fingerprint. All five retained dumps
were unchanged, and the four original audit exports remained byte-identical
and passed the standalone verifier. The projection log SHA-256 is
50e6e9a585a6286fbfd0ccf574bad2db0bfb54e55fdf6640b5d8a1667097f660.
These measured values supply formal schema-60 registration; the projection
alone does not establish production-migrator or application acceptance.
The up and guarded down SHA-256 values are respectively
d7e8eb61bd8afa4c1a3a1363d092685d13c55ca35d6965257457247336066868
and 30a30989f237d89b56af6c5bef12115b3242089463ea608db445b1eb9df5d7df.

## Formal schema-60 acceptance

The qualified input is f110757995e081544b836ec240474a700176571e,
tree d5adf0a8a3a70cd0482ac1039d842b8fa355a6b4. All 59 predecessor
migration bytes and release-lineage entries remain unchanged. The imported
owner implementation remains byte-identical; only the two isolated test
adapters select the registered table and actual numbered scripts.

On PostgreSQL 18.6 and pgvector 0.8.7, a Linux arm64 Docker fixture on the
Darwin arm64 workstation ran with network none, two CPUs and no published
ports. Six numbered-script test roots passed: pristine rollback, retained
CSV/Parquet receipts, prior RR/SERIALIZABLE snapshots, fresh transactions
at all four isolation levels, a committed writer observed waiting at the
exclusive lock, and registered foreign/null scope refusals. Nine runtime
access roots passed, including both description and receipt grant resume.
They preserved the existing principal, password verifier and predecessor
ledger, refused premature serving, and retained private-ledger, audit-head,
DDL and TRUNCATE denial after resumed grants. These database cases had no
skips. Their log SHA-256 values are respectively
549f4cd1568a15caeb4fda9b442936d00796d932fcebdbb268fe157530e2be6e
and a30b606c809498466d830ea16cd6e8c388cd4a1a6ea0860fbdbea5b86c1b792a.

The actual production migrator ran twice and normal catalog admission
passed on eight histories: fresh, native 59, restored 55, 56, 57, 58, 59
and current 60. Each reached 120 tables and a 60-entry ledger with the
three measured fingerprints above. Original schema-55 through schema-59
dumps remained unchanged. All four original audit exports were byte-identical
and passed the standalone verifier. Existing lineage and cell-scope
regressions passed. The restore-matrix and lineage log SHA-256 values are
b71193e58a4f17b24ef6816f26d8f6be5b75c6aba162a1ee1792c1d9f64cf525
and 5c88bcc5fd54d2e46baac828f6828635a934cc48b0b083c8d4965e6109c35fed.

Full `make check`, `make build` and `make test` passed on that input,
using offline dependency resolution and a private APFS temporary volume.
Console passed 1,075 browser cases and Portal passed 389, with their 37
existing live-fixture skips and no added retries. The focused affected
packages also passed race tests. The full-gate log SHA-256 values are:

| Gate | SHA-256 |
| --- | --- |
| check | 197c06c775d1a4509d7d8089b0e1b39f6eb4e3801b2c7eaebeb53f852ceeae6f |
| build | d73107de9eeaf089d8b5892cc7f4f2042f319f865d59646c3d250bdb62f78e1b |
| test | 43f0bad0f36ed3c60c4ff98767d3268a5d00ed2b7eaf9607e018f49d290cd62c |

The initial static grant-catalog check correctly failed because its explicit
migration-input list omitted 60. Adding that real migration and changing
the exact table count from 22 to 23 preserved the full table-name comparison.
No permission, retention, concurrency or scope assertion was relaxed.
The later rebase added only three existing training/status documents;
all executable and test inputs remained identical to the qualified input.

The tagged dataset suite compiled for Linux arm64 and amd64. Its current
native amd64 CSV/Parquet HTTPS and object-restore replay has not run.
The native handoff of f1107579 is receiver-verified at 108,333 bytes with
SHA-256 b3aa444d51f8fb8d030ea9b5abc8685414cdb945e627bb60ceb9aeabea15290d.
A source handoff is not runtime acceptance; the prior owner replay remains
separately scoped above. To replay against
the formal lineage, leave `OWNSOLID_TEST_DRAFT_RESTORE_PREDECESSOR_SQL` unset
and run `bash services/control/internal/modules/datasets/testdata/replay_draft_import.sh`
with the protected interpreters required by that runner. The frozen testdata
up/down files preserve the original owner artifact; current tests execute
the numbered migration, including the retained-data downgrade guard.

DAT-101 stays open. Original and normalized artifacts still need the same
production backup and integrity inventory before route registration,
public-contract composition, release-image and S-503 UI acceptance.

## Original source files in the dataset snapshot

Schema 60 now registers the receipt. Its next owner-side requirement is to
include both the retained CSV/Parquet original and normalized JSONL in the
same production file inventory. PostgreSQL's [logical dump](https://www.postgresql.org/docs/18/backup-dump.html)
reconstructs database state, so application files must remain paired with its
references under the existing stopped-runtime/installation-lock contract.
[DVC metadata](https://doc.dvc.org/user-guide/project-structure/dvc-files)
records artifact paths, sizes and digests separately from large cached bytes;
this supports the existing metadata-bound copy pattern, not a DVC dependency.
Go's [bounded reader](https://pkg.go.dev/io#LimitReader) and the existing
snapshotio copier keep hashing/copying independent of source size.

Use the existing SnapshotSource inventory with an optional original-format
role restricted to CSV or Parquet. Derive that role from the registered
receipt joined to its exact six-field draft scope, never from a filename or
client path. Validate the report's normalized size/hash against the draft
before adding its original size/hash. Require one normalized and at most one
original role per object. Both roles count toward existing file/byte limits.
The operator inventory remains installation/tenant scoped, as its stopped
store and logical backup already are; user report reads retain current
workspace and local-cell authorization. No second file authority is added.

A snapshot containing an original uses file-manifest version 2. Legacy
JSONL-only exports retain version 1, and the new reader accepts them unchanged.
The store namespace marker stays version 1. The strict older reader will
refuse the new manifest instead of restoring only half the receipt. Reject
version-1 manifests with originals, unknown roles, duplicate/orphan roles and
any original whose paired normalized source is missing. Import must compare
the entire reference inventory with the restored database before writing to
the destination. Startup integrity must independently verify both file roles.

This reuses the current Go BSD-3-Clause, pgx MIT, PostgreSQL License and
Apache-2.0 PyArrow components and the generated CC0 fixtures already reviewed
above. No dependency, model, dataset, service, migration or runtime download
is added. Commercial, redistribution and offline obligations remain unchanged.
DVC is a primary-source comparison only and is not installed or redistributed.

Reject copying originals as an unreferenced supplemental archive: it would
leave ordinary backup/readiness incomplete. Do not infer receipt identity
from file extensions, adopt unknown orphan files, fabricate reports for old
drafts, or change the store namespace to version the transfer manifest.
See [ADR 0071](../decisions/0071-version-dataset-snapshots-with-original-sources.md).

Acceptance must cover actual CSV and locked-PyArrow Parquet import, ordinary
snapshot export, logical database restore, original and normalized file
restore, re-login and identical authorized report read. Preserve legacy JSONL
and its version-1 codec. Missing or same-size altered originals/normalized
files must refuse readiness/export/import and leave an unverified restore
destination untouched. Changed/missing receipt metadata must refuse import
of the old bound snapshot. Pure controls cover role/version validation,
counts/limits, symlinks, and metadata-to-file hash binding. All hashing and
copying remain streamed; no source body is materialized by the product.
The existing small synthetic fixture may read its own bytes for assertions.
Production routes, release image and S-503 UI stay open until their owners
complete the installed path; this increment must not tick DAT-101 alone.


## Native complete-source snapshot acceptance

The component source `0d8d09b60bcffdcda1a79a72f55e9003bb57ede9`, based on
main `be62f58e846dcc654dafee65cba6cc7480890570`, passed all five maintained
native dataset groups together on disconnected Linux amd64 PostgreSQL 18.6
and pgvector 0.8.7 in 19.733 seconds. The container had network none and no
published ports; the test process entered its loopback-only namespace. This
run includes the intervening identity audit and scheduler implementation.
It supplements the earlier formal migration qualification rather than
repeating or replacing its eight-history matrix.

The actual CSV import and the locked-PyArrow Parquet import both restore the
original and normalized bytes through ordinary ExportFiles/ImportFiles.
Parquet additionally restores the logical database into a fresh database,
invalidates old sessions, logs in again and reads the identical authorized
report. A legacy JSONL source still exports a version-1 manifest. One actual
Parquet receipt plus 64 synthetic operator fixture receipts crosses the
64-record keyset page boundary: the complete snapshot contains 131 source
roles and 132 files including its namespace marker. Those clones test
inventory paging, not 65 independent user import flows.

The tests refuse missing and same-size altered original/normalized files
at readiness, export and import, with no unverified destination mutation.
Exact file repair restores the original report and readiness. A changed or
removed receipt refuses the old snapshot before copying. Existing HTTPS,
CSRF, workspace-denial, source custody and file-snapshot controls also pass.
The maintained `testdata/replay_draft_import.sh` default selects all five
groups, including the new CSV and complete-file restore cases.

A first CSV fixture replay at `1d292db9` failed when OpenConfiguredStore
correctly refused an incomplete processor configuration. The fixture now
copies the source processor configuration before selecting the restored
artifact root; product validation is unchanged. The focused corrected CSV
case passed at `9e524484`, followed by the complete five-group run above.
The original failed run and the focused repair remain preserved.

| Retained raw evidence | SHA-256 |
| --- | --- |
| `/root/lane-trn-dat-schema60-current.pyqqwi5i/native.log` | `959e84a4bf53a34df1b35f6e7908b2a392fe794e829e74ffabef68acb8225b1f` |
| `/root/lane-trn-dat-schema60-current.pyqqwi5i/postgres.log` | `804502fb3fdec4e2dbb5095a905a06a943aee1f301bd46df3b9eefca18453ab4` |
| `/root/lane-trn-dat-inventory.67f3fjmt/native.log` (original CSV failure, four other groups passed) | `b26c77dbdadd6bac18c01dac70e81ff023745eaaf6883a58a20b8b5678bc0c5e` |
| `/root/lane-trn-dat-csv.qwenb8u0/native.log` (focused repaired CSV case) | `740653846de611a6139d2b174ae073ab5f397a82dc8028cd3d3f579d5b065687` |

The isolated data interpreter contains exactly PyArrow 26.0.1 on Python
3.12.3. The 55,043,763-byte CPython-3.12 manylinux wheel matches the existing
lock SHA-256 `c718bf97a92a6dea97fd1062c3b9f9e2e83cb9cc926e752289fa4ffff41738f9`.
A network-none, no-index, hash-required installation, actual import and
pip check passed, with the original LICENSE and NOTICE retained. This fixture
does not change the separate retained 37-package training environment or
admit a production release image.

All new hashing and copying remain bounded streams; receipt metadata is
paged before file I/O and the manifest retains its configured inventory
ceilings. The draft source path still enforces its existing 64 MiB and
10,000-row contract. These tiny synthetic restore fixtures do not constitute
a 50 GB product import or an installed generic-registration/UI acceptance.
DAT-101 remains open for route/report composition, the offline release image
and S-503 UI. Fresh repository gates for the rebased branch are recorded
separately; this native component result alone does not complete them.


## Current source and repository-gate boundary

At source f61bea6b995a3b9980b65be93f74047b736ea45c, based on main
dfaf5e8868de489ed12a78c8289069576789fa8a, all five maintained dataset
groups passed together again in 20.914 seconds on disconnected Linux amd64
PostgreSQL 18.6. This includes the updated shared HTTP-fixture lifecycle.
The original/normalized restore, 64+1 receipt pages, exact repair and
dual-entrance source-custody assertions remain unchanged.

The preserved native log is
/root/lane-trn-dat-dfaf.505n49t1/native.log with SHA-256
af665f19f3a8fcdec0b91de2b9ec8d528b581af037a26195c6405c39cb2e3b48.
Its privately captured postgres.log SHA-256 is
fed4ebf113ae8087fb4d09561dc0cc94c8bc9984682a2e8d38eca85eeed4f1ad.
The disposable container and test-owned metadata/files were cleaned up.
No external route or GPU was used.

The same Go inputs passed all ten training PostgreSQL/HTTPS roots; see the
[current training acceptance](training-job-lifecycle.md#current-native-owner-and-https-acceptance).
The expanded maintained training selector and subsequent research/evidence
documents change no Go, Python, SQL or API input. Fresh ordinary owner-package,
build and check gates remain pending: the following resource admission refused
high I/O before starting modules, and later bounded lock requests expired
before executing the gate helper. These are not test/build results and do
not qualify a full repository gate or installed product path.
