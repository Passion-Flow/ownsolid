# Offline model import file safety

## MOD-108 approved import path

[Hugging Face Hub pickle scanning](https://huggingface.co/docs/hub/security-pickle)
describes the code execution risk of Python pickle and scans uploads for
indicators. [Hugging Face Text Generation Inference](https://huggingface.co/docs/text-generation-inference/en/basic_tutorials/safety)
uses Safetensors by default and requires explicit permission for unsafe
conversion. The [Safetensors specification](https://github.com/safetensors/safetensors#format)
defines a length-prefixed JSON header and contiguous tensor data. Keep the
current approved Safetensors path. The registry and importer must never run
a loader, execute a model file, unpickle weights, convert unsafe formats, or
fetch anything during review.

The existing import resolves a pinned catalogue and checks every file's
name, size, SHA-256 and supported configuration. That shared verification
currently learns about a late executable or unsupported file only after
writing the preceding approved files into a private stage. Add a read-only
review after all chunks are committed and before extraction. Review the
entire USTAR member layout against the catalogue, refuse executable modes,
links, extended headers, duplicates, unknown members and trailing data,
and check Safetensors using the existing structural parser. Check the
pinned loader and tokenizer configuration digests before extraction.
These are the files that could change loader selection or enable custom
code. The full extractor continues checking all payload hashes, the
committed chunk hashes and the existing configuration semantics. A
recovered nonempty stage or published object still needs the same complete
immutable file verification.

Use [Go archive/tar](https://pkg.go.dev/archive/tar) to interpret one USTAR
header at a time. Read bounded header, configuration and trailer regions
directly from the committed chunk layout. This skips the bulk tensor bytes
during the read-only review and limits parser work to the approved member
count. It also keeps extended records out of the parser's look-ahead.
The file and repository session locks stay held across both passes. The
preflight is only a rejection gate. Publication still requires extraction
and full digest verification under the existing worker. A second full
archive pass adds avoidable disk traffic. Extension-only rules cannot
identify pickle renamed to `model.safetensors`. Generic malware heuristics
and a pickle opcode interpreter would expand scope and still would not
establish safe load semantics.

This adds no runtime dependencies or datasets. The existing Go standard
library is [BSD-3-Clause](https://go.dev/LICENSE). PostgreSQL and the
test extension use the [PostgreSQL License](https://www.postgresql.org/about/licence/)
and [pgvector's license](https://github.com/pgvector/pgvector/blob/v0.8.7/LICENSE).
The [Safetensors project](https://github.com/safetensors/safetensors/blob/main/LICENSE)
is Apache-2.0 and is a format reference here. The existing
[Qwen3-0.6B revision](https://huggingface.co/Qwen/Qwen3-0.6B/blob/c1899de289a04d12100db370d81485cdf75e47ca/LICENSE)
is Apache-2.0. These licenses permit offline and commercial use and
redistribution subject to their applicable license and notice terms.
Adversarial tests use synthetic headers and inert pickle bytes.

Verify ordinary approved member order and alternate order, executable
members and modes, nonregular entries, duplicate and late unknown names,
renamed pickle, altered loader configuration, truncated archives and
nonzero bytes after the terminator. In disconnected PostgreSQL exercise
real partial upload, explicit verification and durable failure, exact
retry, manual cancellation and a new clean import of the pinned real
model. Refused requests must create no model version or ready audit, and
preflight refusals must stage no model payloads. The genuine package must
publish once and retain the same verified manifest. Generic catalogue
admission and a richer operator review remain dependent on MOD-101 and
the shared contract.

## Disconnected PostgreSQL acceptance

On 2026-11-09, `TestActualModelImportFileSafety` passed in 19.46 seconds
on Linux arm64 against PostgreSQL 18.6 and pgvector 0.8.7. The container
had no network. The pinned Qwen USTAR archive was 1,519,216,640 bytes,
with SHA-256
`38a49a5e43d5bf570dd1e8d7198941b881f18e1c2526aa71b8920bb876d6feba`.

The fixture changed the last member into an executable Python member,
then independently put inert pickle bytes into the approved weights
entry. Both variants used the full resumable upload flow. Queuing a
partial upload was refused. Each complete variant was refused twice
with its corresponding durable reason, without a model version, ready
audit, published object or staged payload. After cancellation, the
unchanged Qwen package published exactly once and all nine stored
files passed full verification. The existing publication-audit rollback
and fresh-worker recovery test also passed on the real archive. Its
separate snapshot and retirement subtest was outside this run.

Use the private loopback database fixture described in
[import cleanup acceptance](model-import-cleanup.md#linux-amd64-acceptance).
Set `OWNSOLID_TEST_MODEL_FILE_SAFETY_ARCHIVE` to the pinned local USTAR file.

```sh
go -C services/control test -tags model_integration ./internal/modules/models \
  -run '^TestActualModelImportFileSafety$' -count=1 -v
```

### Linux amd64 replay

On 2026-11-12, fresh Linux amd64 binaries from `a3b3747d`, based on
`67293e12`, passed against PostgreSQL 18.6 and pgvector 0.8.7 in a
container with `--network none`. The original nine Qwen files remained
on the test server. Each file passed its pinned size and SHA-256 check
while a fresh USTAR archive was prepared there. That archive was
1,519,216,640 bytes with SHA-256
`a3c43cd415968956b078c4463b4e112866344c0c20235a2a9049db15f0d0d0e9`.

`TestActualModelImportFileSafety` passed in 53.55 seconds. Its two
unsafe variants each failed verification twice without staging a
payload or publishing a version. Partial queuing was refused and the
unchanged approved files published once. The publication-audit rollback
and fresh-worker recovery root passed separately in 41.09 seconds,
including cleanup rollback and recovery. Its snapshot-and-retirement
subtest remained outside the selected run.

The same code passed the serial native `make check`, `make build` and
`make test` gate. The [shared test prerequisites](lane-m-questions.md#shared-full-test-order)
describe the selected Python, temporary filesystem and preserved
harness warning. Generic catalogue admission and the operator review
contract remain the MOD-101 and shared-owner boundaries above.
