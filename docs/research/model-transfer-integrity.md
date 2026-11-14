# Model transfer integrity

## MOD-109 leased transfer guard

The [OCI image descriptor](https://github.com/opencontainers/image-spec/blob/v1.1.1/descriptor.md)
binds content identity to a digest and size. The
[OCI Distribution specification](https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md)
uses that identity for a blob fetch. [RFC 9530](https://www.rfc-editor.org/rfc/rfc9530.html)
separates a digest of response content from a digest of its complete
representation. ownsolid's existing Agent contract similarly has a
range digest and a catalogue digest for each entire file.

The owner already hashes the requested bytes. When a request covers the
whole file, compare that same SHA-256 with the exact trusted catalogue
entry before handing out the file descriptor. Apply it to approved base
and derived files. The previously separate verification-asset entry
should share the same full-file check. Refuse a same-size change with the
existing `MODEL_FILE_CHANGED` error. The lease and scope fences and
response contract remain in place. A range of a larger file still has its
range digest, and the Agent verifies the assembled file against the
catalogue before use. Hashing the entire file for every bounded range
would multiply work while the lease transaction is open.

Use the actual Admin and node TLS flow to download a pinned Qwen
configuration, replace it with an ordinary file of the same size and
mode, confirm refusal without an artifact response, then restore the
fixture and retry using the same valid lease. The existing cross-scope
and stale-lease checks remain part of that run. Also exercise a
catalogue-bound derived file against PostgreSQL. The code reuses Go's
[BSD-3-Clause standard library](https://go.dev/LICENSE), the
[PostgreSQL License](https://www.postgresql.org/about/licence/) fixtures,
and the current [Apache-2.0 Qwen revision](https://huggingface.co/Qwen/Qwen3-0.6B/blob/c1899de289a04d12100db370d81485cdf75e47ca/LICENSE).
These permit offline and commercial use and redistribution subject to
their license and notice terms. There are no new dependencies or datasets.

This qualifies the existing leased path. Site Registry admission and
Agent pre-warming still require NET-102 and the shared Agent owner.

## Disconnected TLS and PostgreSQL acceptance

On Linux arm64 with no network, `TestRealTLSInferenceAPICSRFSchemaAndLeasedModelTransfer`
passed against PostgreSQL 18.6. An authorized node downloaded the exact
726-byte Qwen configuration. After a one-byte substitution, the same
lease received HTTP 409 with `MODEL_FILE_CHANGED` and no artifact digest
header. Restoring the fixture allowed the same lease to retry and
return the pinned digest. Wrong credentials, unsafe paths and expired
task authority remained refused.

The tagged `TestActualBorrowedModelAccess` cases also passed. They
covered the base file descriptor, a synthetic derived file with a
catalogue-bound digest, and cancellation. A same-size change to the
derived file produced no download descriptor, and restoring it allowed
the same command and range again.

Use the private loopback DSN described in the
[development environment](../development/environment.md#database-integration-tests).
Set `PROJECT_ONE_REPOSITORY_MODEL_FILES_DIRECTORY` to the directory
containing the pinned Qwen files for the tagged case.

```sh
go -C services/control test ./internal/modules/deployments \
  -run '^TestRealTLSInferenceAPICSRFSchemaAndLeasedModelTransfer$' -count=1 -v
go -C services/control test -tags repository_integration ./internal/modules/models \
  -run '^TestActualBorrowedModelAccess$/^(base_range_hash_and_owned_descriptor|derived_metadata_file_mode_link_missing_refusal|cancelled_IO_returns_no_download)$' \
  -count=1 -v
```

## Descriptor and multi-range follow-up

Go's [traversal-resistant root](https://go.dev/blog/osroot) constrains
file traversal. Use the same no-follow open flag as derived storage to
refuse a final-component symlink at the open itself. Check the open
descriptor's regular file, size, mode, owner and link count both before
and after hashing. A stat of the pathname alone leaves a window for a
local replacement to change the opened file. Keep the existing file
identity, modification time and pinned digest checks.

The current agent contract bounds each transfer to 8 MiB. A real
approved Qwen tokenizer is 11,422,654 bytes and exercises a full range
plus a short terminal range without importing weights. Over the
existing TLS lease, verify both range hashes, replay the terminal range,
and assemble the original pinned digest. Refuse an aligned request
beyond the end, a final-component symlink to an in-store file and
a writable file. A valid retry after fixture restoration must return
the pinned bytes. The base file and derived transfer keep their
existing response shape and per-range integrity.

On disconnected Linux arm64 and PostgreSQL 18.6, the existing real TLS
transfer test sent the original 11,422,654-byte tokenizer in two
ranges. The concatenated bytes matched the pinned catalogue digest.
The server rejected an aligned offset beyond the end, a final symlink
and a writable replacement. With the original file restored, the same
lease returned the exact short range again. The base and derived
repository checks also passed. Use
`PROJECT_ONE_REPOSITORY_MODEL_FILES_DIRECTORY` for the pinned tokenizer
when running the TLS test. This check does not import the weights.

### Linux amd64 replay

On 2026-11-12, Linux amd64 binaries from `a3b3747d`, based on
`67293e12`, replayed both actual PostgreSQL paths in a container with
`--network none`. PostgreSQL was 18.6 with pgvector 0.8.7.
`TestRealTLSInferenceAPICSRFSchemaAndLeasedModelTransfer` passed in
6.09 seconds with the original 11,422,654-byte tokenizer. The two
ranges, assembled catalogue digest, terminal replay, changed config,
out-of-range request, symlink, writable-file refusal and restored-file
retry all passed under the real lease and TLS dispatcher.

The three selected `TestActualBorrowedModelAccess` subtests passed in
8.80 seconds. They covered the base descriptor, derived file metadata,
mode and link refusals, missing files, digest substitution and cancelled
IO. The source and binaries matched the measured native gate, which
passed `make check`, `make build` and `make test`. Site Registry and
Agent pre-warming retain their existing owner dependencies.
