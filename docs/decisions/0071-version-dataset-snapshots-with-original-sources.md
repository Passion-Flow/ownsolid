# Version dataset snapshots with original sources

Status: accepted design for the DAT-101 schema-60 file-owner increment;
implementation and native acceptance remain pending.

## Context

The dataset snapshot currently carries normalized JSONL and published versions.
A schema-60 CSV/Parquet receipt binds an additional retained original. Restoring
only JSONL cannot satisfy that receipt, even if the logical database is intact.
The existing namespace marker identifies the store, not its transfer codec.

## Decision

Extend the existing source inventory with an optional CSV/Parquet original role.
Resolve roles, sizes and SHA-256 values from the server's exact draft/receipt
join. Require the corresponding normalized role and include both in ordinary
streamed snapshot copying, restored-reference equality and startup integrity.
Retain the existing installation lock, stopped owner and private-root checks.

Emit file-manifest version 2 when an original is present. Continue emitting and
reading version 1 for legacy sources. Keep the store namespace marker at its
existing version. Reject role/version mismatches and unknown, duplicate or
unpaired originals before destination mutation. This extends the same backup
authority described in [ADR 0068](0068-bind-external-objects-to-the-logical-backup.md).

## Consequences

Old JSONL snapshots remain usable. Older software explicitly refuses the new
manifest; it cannot silently restore an incomplete CSV/Parquet receipt. No
new database table, signing format or sidecar registry is needed. File counts
and byte budgets include both roles. The public import route remains disabled
until ordinary production backup/readiness acceptance, release image and UI
composition are complete. The native acceptance requirements and license
scope are in [the import research](../research/dataset-import.md#original-source-files-in-the-dataset-snapshot).
