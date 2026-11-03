# Maintenance runtime projection

This package is a Control runtime dependency, not only a command-line utility.
[maintenance.go](maintenance.go) loads the owning protected scope configuration,
dispatches the existing fixed scope commands and projects their original typed
results. Original private JSON is retained as `Original` with an evidence digest;
it is excluded from the public projection. Configuration, durable effects,
reconciliation and recovery remain with `internal/configuration` and
`internal/scope`; this package creates no alternate execution authority.

[native_postgresql.go](native_postgresql.go) transports the release-contract
backup/restore reference union transparently as the same `native_postgresql` JSON
key. Strict decoding rejects unknown, duplicate, null or mixed objects and clears
stale union state. The stored Result decoder checks the entire outer object before pointer decoding, while the original projection retains native field presence as raw JSON. Explicit native_postgresql:null refuses at both boundaries; original omission is preserved. Omission compatibility is not completed MVP proof. It preserves canonical owner fields without introducing a
discriminator, private path, new proof hash or status. Backup/source projection
binds `profile` and `release_manifest_sha256` to the original native declaration.
The signed source manifest, native candidate and outer inventory manifest are
distinct. Original owner verification must precede producing those references.

The final MVP full-native scope action and readonly stage bridge remain pending.
Projection support alone must not route MVP restore through legacy split calls or
claim registry payload import, SQL completion or readiness. The Control consumer
refuses that path until the original scope owner supplies the complete bridge.

Run the repository-admitted Go toolchain from `tools/deploy/offline` with
`go test ./pkg/maintenance`. The focused tests use canonical synthetic shapes and
actual scope result types; they do not execute native SQL or a registry import.
Source-bound validation and remaining gaps are recorded in
[the M26 checkpoint](../../../../../docs/milestones/m26-middleware/parallel-implementation-20261103.md).
