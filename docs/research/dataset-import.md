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
