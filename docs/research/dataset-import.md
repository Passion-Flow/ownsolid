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
