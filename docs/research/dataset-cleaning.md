# Dataset cleaning and filtering (DAT-103)

[Hugging Face Datasets](https://huggingface.co/docs/datasets/process) separates
row mapping from row filtering. Its [streaming guide](https://huggingface.co/docs/datasets/main/stream)
applies transformations as records are consumed. Use the same separation in
ownsolid's bounded importer so operators can see exactly how many rows were
changed, filtered, and rejected by validation. Reuse the Go standard library
under [BSD-3-Clause](https://go.dev/LICENSE), including the Unicode whitespace
definition in [bytes.TrimSpace](https://pkg.go.dev/bytes#TrimSpace). The
implementation ships no new dependency, training data, or model.

## Initial rules and preview

- Callers explicitly select top-level fields to trim. Only string values are
  trimmed. Missing, null, and non-string values stay unchanged.
- Callers may keep only records with a value in every selected field.
  Missing values, nulls, and whitespace-only strings are considered blank.
  Other values, including false and zero, are retained.
- Parse the record first, apply the blank filter, trim retained values, then
  validate the selected training format and write the normalized record.
  Keep filtered and rejected counts separate. Malformed source framing and
  resource failures keep the source import's existing fatal behavior.
- Count a changed row only after the transformed row is accepted. Collect
  complete paired samples for changed rows and before samples for filtered
  rows. Use original source positions and retain the first eligible samples
  within explicit count and byte budgets. Skip an oversized sample and
  report truncation. The report keeps samples out of default JSON output.
  Return the samples to the Go caller only after source identity and the
  private artifact commit succeed. The caller must keep them out of logs
  and publish them only to an authorized preview response.

The caller stages the candidate in a private directory and decides whether
to register it. That path can produce the candidate and samples in one scan.
Implicit type conversion or automatic text normalization could corrupt
labels, identifiers or intentional whitespace. Arbitrary executable rules
would add a new execution and offline dependency boundary. Sample selection
by complete records preserves the row's original structure without returning
partial JSON or requiring storage proportional to the dataset.

## Verification

Use the normal file and S3 intake paths. Check that source bytes and digest
are preserved, candidate bytes and output digest reflect the selected rules,
and sample positions and counts agree with the output. Exercise Unicode
whitespace, null/missing/blank values, false and zero, malformed rows,
validation after trimming, malformed rules, both preview budgets, source
identity failure, and the no-valid-row case. Confirm serialized statistics
exclude sample contents. The persisted policy and the authorized S-503
preview and apply flow still depend on generic workspace registration.

From the workspace root, run the focused tests, including the actual 128 MiB
file with trimming, filtering, and preview enabled:

```sh
go test ./services/control/internal/modules/datasets/... \
  -run '^TestSource(Cleaning|ImportS3|ImportMemory)' -count=1 -v
```

The disconnected Linux arm64 import suite passed as UID 10001, including
S3 loopback, Parquet, and the opt-in cleaning path. The 16 MiB and 128 MiB
JSONL files cleaned 4,096 and 32,768 rows and peaked at 24,064,000 and
25,833,472 resident bytes. Both retained eight samples and reported
preview truncation.

## Linux peak memory measurement

The full integration process can have a much larger resident set than the
isolated import child. Linux preserves resource usage across `execve`, and
the [kernel exec path](https://github.com/torvalds/linux/blob/master/fs/exec.c)
carries the replaced address space's RSS peak into resource usage. The parent
can therefore read the same pre-exec high-water value from `wait4` for both
inputs. Measure `VmHWM` from the importer child's
[`/proc/self/status`](https://docs.kernel.org/filesystems/proc.html) after
importing. It measures the child's address space and captures spikes between
samples. Periodic RSS polling could miss those spikes. Keep the existing
macOS process usage measurement.

The test uses the [Go standard library](https://go.dev/LICENSE) and
synthetic fixture data. It adds no dependency or runtime requirement.
The standard library permits commercial use, redistribution with the
license notice, and offline use. Verify the 16 MiB and 128 MiB cases both
in isolation and after the database integration tests. Retain the 64 MiB
absolute ceiling and the 24 MiB allowed growth. Remove each input and output
once its measurement is complete.

## Dry run against a sealed source

An operator must be able to inspect a filter that removes every row before
applying it. The publishing path still rejects an empty dataset. A separate
dry run uses the same decoder, cleaning rules, schema checks, and limits
to count retained and removed rows. Like
[Hugging Face streaming transforms](https://huggingface.co/docs/datasets/stream#filter),
the policy is evaluated per record. Use Go's
[`io.Discard`](https://pkg.go.dev/io#Discard) for normalized rows during
preview. The output byte count is an estimate of the would-be artifact and
no output digest or persistent artifact is issued. Avoid writing a second
normalized copy and keep Parquet's bounded private decoder scratch.

Require a source length and digest, verify the entire transfer, and only
then release complete samples within the existing budgets. An incomplete,
oversized, altered, or invalid source yields no sample or output reference.
Keep samples outside the serialized report. The caller must authorize the
immutable source and the recipient before exposing a preview. This is the
intake primitive for the authorized workspace policy flow in S-503.

Verify identical counts and samples between a dry run and a successful
application, preview an all-filtered result with no artifact, and reject
both digest mismatches and a valid prefix followed by a transport error.
The implementation adds only standard library code and synthetic fixtures,
with the same offline and commercial-use license terms documented above.

The Linux amd64 loopback-only run verified dry-run parity for JSONL and the
filtered row in a real PyArrow 26.0.1 Parquet fixture. The pinned wheel was
installed without an index and passed its lockfile digest check. The
dry-run scratch directory was empty on success and failure.

## Dry run for an authorized S3 object

A caller that has already resolved a tenant and workspace grant can pass
its immutable receipt directly to the same cleaning scan used by uploads.
Reuse the existing S3 reader and
[conditional GetObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html#API_GetObject_RequestSyntax)
with the receipt's exact key and ETag. Reject inconsistent response
metadata, then verify the full byte count and SHA-256 before returning any
sample. An ETag controls object selection. The receipt's SHA-256 controls
byte identity. Closing the body and sharing one operation deadline covers
both the GET and the scan.

A caller-supplied URL, HEAD followed by an unconditional GET, and using
the ETag alone would remove source binding. Applying the policy solely to
a prefix would conceal later invalid data and undercount filtered records.
Reuse the existing Go standard library and the repository's S3 SDK under
the [Apache-2.0 licence](https://github.com/aws/aws-sdk-go-v2/blob/main/LICENSE).
Both allow offline use, commercial use and redistribution with the
documented notices. No new dependency, dataset, or model is required.

Use the existing synthetic TLS S3 fixture to verify signed conditional
reads, source identity, all-filtered preview, response metadata mismatch,
interruption, incorrect digest and refusal before opening an invalid
request. The preview does not retain normalized data. The caller still
owns current grant resolution, authorized sample presentation and the
separate apply operation in S-503.

The Linux amd64 loopback-only S3 fixture verified all of these responses
through the existing SigV4 client. A good request after both metadata
refusals reused the single transfer slot. Use:

```sh
go -C services/control test -count=1 ./internal/modules/datasets -run '^TestSourceImportS3$'
```
