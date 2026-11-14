# Read-only MongoDB dataset source

The fixed [native quality gate](../../../../../tools/quality/mongodb/README.md)
uses an owned real Community TLS/SCRAM replica set and restricted find-only user.
The reader remains unchanged; private test infrastructure lives under
`internal/testing/mongodbfixture`. Source presence is not actual qualification.

This package is the configured MongoDB collection reader used by [datasets](../datasets/README.md). It does not own dataset publication, a standalone HTTP surface or MongoDB writes.

[reader.go](reader.go) validates the configured source, snapshot-session read and bounded field projection. Rows are ordered by `_id`; prompt/completion mappings use the admitted string or extended-JSON conversion. The URI option allowlist, single-connection pool, primary selection, explicit timeouts and disabled driver retries constrain the source operation. A failed read returns no partial successful import.

The datasets owner's mongodb path retains import state and source binding; migration 000021 stores MongoDB dataset import records. The source adapter must not choose tenant/Workspace scope or bypass current dataset authority. Publication stays in the dataset transaction after the complete source result is validated.

Change source behavior through control MongoDB configuration and [connector examples](../../../../../config/examples/mongodb), with protected credential references. Synchronize [MongoDB contracts](../../../../../contracts/mongodb), [M21](../../../../../docs/milestones/m21-mongodb), datasets ingestion/recovery, the selected SQL repositories and [reader tests](reader_test.go). Adding source fields or URI options requires explicit validation and a failure/permission case; unrestricted connection strings are not a compatibility feature.

Focused native check: `go -C services/control test ./internal/modules/mongodatasets ./internal/modules/datasets`. Real snapshot behavior, credentials, collection grants and interruption cases require the declared MongoDB fixture; transport doubles do not qualify an external database.

The PostgreSQL import owner now uses `CaptureSource` to pin URI/CA bytes before
reading and rechecks the fingerprint through the versioned receipt grant. The
[Linux native reader test](reader_native_integration_test.go) reuses the existing
TLS fixture; actual server mutation and getMore faults produce real driver
responses, while command monitoring supplies only the response boundary. Run
with `-tags=mongodb_integration` and the protected fixture input after admission.
The remaining M08 transaction seam and separate acceptance scopes are described
in the [current M21 increment](../../../../../docs/milestones/m21-mongodb/parallel-20261103.md).

`reader_refusals_native_integration_test.go` adds a separate actual-source root
for malformed last-row fields, mixed identifier types, output row/byte budgets
and explicit canonical int64 conversion. It reuses the same fixture and never
requires replaying the accepted snapshot mutation/getMore root. Every refusal
must return zero rows, no bytes and no watermark; restored source bytes must
match the original eight-row read. Test source is not an executed receipt.
