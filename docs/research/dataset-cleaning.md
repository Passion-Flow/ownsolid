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
