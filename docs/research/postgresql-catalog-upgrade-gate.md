# PostgreSQL catalog admission after a logical restore

[Delivery's restore comparison](restored-schema-upgrade.md) reproduces the first
failure in the released 55-to-56 path. The version 55 binary accepts its native
and restored databases. The version 56 binary accepts its native upgrade, but
rejects the restored upgrade and rolls back. Applying the exact migration bytes
and checksum to a disposable restored copy exposes the failing read-only gate,
`SCHEMA_CATALOG_INCOMPATIBLE`.

## Contract and upstream behavior

The migrator validates the contiguous, checksummed migration prefix under a
transaction advisory lock. It applies the tail and checks the resulting catalog,
cell identity, extension and privileges before commit. The read-only schema and
runtime readiness checks also require the complete ledger and accepted catalog.
The catalog is a hash of the ordered, schema-local relation, column, constraint,
index, trigger and routine descriptions selected by `CatalogSQL`. It excludes
data, OIDs, extension-owned routines and partition children. Admission depends
on the catalog and ledger independently.

[PostgreSQL 18 catalog information functions](https://www.postgresql.org/docs/18/functions-info.html)
reconstruct definitions from catalog expression trees. [pg_dump](https://www.postgresql.org/docs/18/app-pgdump.html)
serializes object definitions and [pg_restore](https://www.postgresql.org/docs/18/app-pgrestore.html)
recreates them from the archive. A dump can spell an array cast as individual
element casts, and the restored expression deparses with that spelling. This
makes a raw fingerprint sensitive to when the dump occurred.

## Reproduction and decision

An isolated PostgreSQL 18.6 and pgvector 0.8.7 run used the embedded SQL and its
original checksums. It applied 1–55, took a real custom-format dump, restored
into an empty database and applied 56 and 57. Separate databases covered a native
upgrade, a fresh install, a restore at 56 followed by 57, and a restore at 57.

| Path | Version 57 catalog SHA-256 |
| --- | --- |
| Fresh or uninterrupted native upgrade | `8454a9e3039dfb669a532aaabd26419661c404d683cd9abfa45eb4ee2b25b4a4` |
| Restore at 55, then apply 56 and 57 | `4ae1c70a752af0056647e275114e2a16e8831bbcf875e6018ad3a7754b11efcc` |
| Restore at 56 and apply 57, or restore at 57 | `c160dfad296f6a93ec88aa88cc63bd232cbfd5dca0518c22d390961b0593e7be` |

The 56 catalog after restoring 55 differs from the native 56 catalog in
127 constraint definitions and 14 index predicates. For example,
`tenants_status_check` places a cast to `text[]` around the original
`varchar[]`, while the restored definition casts each element to `text`.
Relative to a full restore at 57, the mixed catalog differs in the definitions
of `cell_cluster_members_role_check`, `cell_cluster_members_state_check`,
`managed_nodes_execution_profile_check` and
`node_join_tokens_execution_profile_check`, and the predicate of
`cell_cluster_members_open_idx`. All five are the same cast-placement difference
in objects written by 56 and retained by 57. The remaining difference at 56,
`node_tasks_operation_check`, is replaced by 57. The comparison found no
structural differences between these fixture paths.

Separately, the clean main branch fails `TestEveryCellOwnedTableCarriesScope`
because `audit_chain_head` introduced by 53 lacks the two cell-owned columns
required by [ADR 0006](../decisions/0006-cell-scope.md). Fresh and restored
databases both have this shape. Resolving that scope contract requires an
Audit-owned forward migration and retained-checkpoint verification.

Admit the measured mixed version 57 fingerprint in addition to the two existing
complete fingerprints. Keep the immutable migration files and the original
catalog query. Updating 56 in place would invalidate the checksummed ledger on
existing installs. Ignoring expression text or rewriting cast syntax would
introduce ambiguity into the catalog gate. A later schema version must
recalculate its expected catalogs and validate each supported restore/upgrade
path from clean, owned fixtures. Catalog fingerprints taken from a customer
database are unsuitable as baselines.

The historical version 56 binary still rejects the mixed catalog. The verified
path is to run the version 57 migrator directly against a restored version 55
database. This evidence covers the measured restore points on the pinned
database stack.

## Verification

Use the [installer restore fixture](../../deploy/offline/README.md) with the
version 55 and 56 binaries, the immutable 56 SQL and the current candidate, with
the `current` expectation. It requires an empty candidate installation, checks
the original failed upgrade twice for rollback, compares the final ledger and
data with a native upgrade, and repeats candidate migration and schema checks.
The disconnected local Docker run used Linux amd64 Control binaries against
PostgreSQL 18.6 and pgvector 0.8.7 on Linux arm64. The empty install, native
upgrade and restored 55 upgrade reached the same 57-entry lineage. The fixture
verified 116 predecessor table fingerprints, seeded data and both reruns.
The Control PostgreSQL regression changes an existing check in the restored
database, requires two exact catalog refusals with unchanged ledger and catalog,
then repairs the check and requires a successful migration and an unchanged
rerun. It also exercises the restore-at-56 and fully restored version 57 paths. With
the protected owned PostgreSQL test DSN and Docker fixture settings, run:

```sh
go -C services/control test -p 1 ./internal/platform/database -run '^TestCatalogAdmissionAfterLogicalRestore$' -count=1 -v
```

This uses existing dependencies only. [PostgreSQL](https://www.postgresql.org/about/licence/)
uses the PostgreSQL License and [pgvector](https://github.com/pgvector/pgvector/blob/v0.8.7/LICENSE)
uses the PostgreSQL License. Both permit commercial and offline use and
redistribution with their notices. The existing [Go standard library](https://go.dev/LICENSE)
and [pgx](https://github.com/jackc/pgx/blob/v5.11.0/LICENSE) use BSD-3-Clause
and MIT, respectively, and allow those same uses with their required notices.
No models, datasets or new runtime downloads are required.
