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

The schema 57 baseline fails `TestEveryCellOwnedTableCarriesScope`
because `audit_chain_head` introduced by 53 lacks the two cell-owned columns
required by [ADR 0006](../decisions/0006-cell-and-site-scope.md). Fresh and restored
databases both have this shape. The [Audit scope migration](audit-chain-head-cell-scope.md) qualifies the
forward fix and verifies retained checkpoints.

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

## Gateway alias description successor

The original [GW-120 SQL](gateway-model-description.md) matches the
reviewed Gateway fixture. Its up checksum is
`158f874037201b0309d2f4dc579dcf988153a8d0d19e438bf92b328b3ca97466`.
Applying those bytes in a disposable schema after 58 created
`model_alias_description_intents` with zero of the two non-null cell
columns with defaults required by [ADR 0006](../decisions/0006-cell-and-site-scope.md).
The existing cell-owned table check therefore rejects this successor.

The Gateway-owned replacement in c9654657 adds both non-null columns
with the local defaults and references the exact alias tenant, workspace,
site and cell through a composite foreign key. Its up checksum is
`399432d5de8be8daad68db91d74706e52456ff3d0560048e33d5a10088ab72ab`.
The down checksum is
`61a6eb6f8dabc011610c5b307ab904c5c038ec7119a967d48512adbd5dd52c86`.
The down successor explicitly selects READ COMMITTED before locking;
a prior stronger-isolation snapshot refuses before DDL, as qualified
in the Gateway research note.
The Gateway fixture measured three successor catalogs after applying
the Audit scope overlay. Keep these as comparison values. Assign the
next contiguous migration only after the Audit migration lands on main.
Recompute the signed lineage checksum, the 119-table expectation and
the catalogs on the committed predecessor. Test fresh install, native
upgrade and the logical restore histories starting at 55 through the
then-current schema, including the exact down SQL and catalog replay.

## Dataset and metering successors

The DAT-101 owner candidate at `fa82645c` adds the six-column scope
key on `dataset_drafts` and requires the exact draft's site and cell
on `dataset_draft_import_candidates`. The up SHA-256 is
`d7e8eb61bd8afa4c1a3a1363d092685d13c55ca35d6965257457247336066868`.
The down SHA-256 is
`8ed40d3afaafa83fb4d119e90a01323fa09967f839831e1fa2159b3f8a6c06fb`.
This is a retained owner revision. The coordinated release restore
also needs both dataset references in `snapshotReferences` and
`referenceFiles`, with the REL-103/NET-101 checkpoint binding.
The isolated owner replay restored the normalized file and supplied
the original separately. See the [dataset restore boundary](dataset-import.md).

The metering handoff at `73a3382f` and `273929ee` adds both scope
defaults to `usage_ledger` and includes site and cell in its reporting
index. It preserves the unique producer and event key across scopes.
The up SHA-256 is
`230b2182ba3a953b6231b76b001f28972362e0a30e8869cf4209413fffdd21b3`.
The down SHA-256 is
`d8e296ead25bd9ba8ed890bde9b178890a102dadf4c9888a88a3c64e5c9d9eb3`.
The down SQL requires its transaction and refuses an accrued ledger.
The owner qualified the candidate from a logical schema-55 restore.
The production Gateway Report call still needs the accepted schema
and its verified projection hook. See [metering admission](usage-ledger.md).

## Shared successor catalog and runtime role inputs

The three unnumbered candidates each add one business table and no
sequence. GW-120 and DAT-101 each add a named composite key to an
existing parent. Register each new table in the PostgreSQL-only
`postgreSQLTables` grant list when its migration is accepted. That
list feeds fresh access preparation, existing-role upgrade and the
grant inspector. The embedded SQL is checked against this list by
`TestCatalogMatchesImmutableMigrations`. Keep the shared MySQL list
at its existing scope. The catalog fingerprint covers table shape,
constraints, indexes and functions. It omits ACLs, so verify those
through the separate runtime admission. See the
[grant catalog](../../services/control/internal/operations/databaseaccess/postgres_catalog.go)
and [PostgreSQL GRANT](https://www.postgresql.org/docs/18/sql-grant.html).

Use the same restricted runtime role throughout an upgrade. Each new
table follows the PostgreSQL SELECT, INSERT, UPDATE and DELETE grant
contract. GW-120's immutability trigger and the metering append-only
trigger enforce mutation refusal within those grants. Verify them with
the runtime role as well as catalog readback. Keep `schema_migrations`
SELECT-only and `audit_chain_head` inaccessible. Verify that missing
grants prevent readiness, the grant-only resume retains the same
principal and ledger, and a replay leaves the accepted state intact.

The production logical restore uses `--no-owner` and `--no-privileges`.
Run the target runtime-access owner after restore and check its
effective privileges separately from the structure. Reuse the SCI
pinned PostgreSQL 18.6 and pgvector 0.8.7 fixture and its retained
55 through 58 archives as predecessors. After each actual numbered
migration, extend the same catalog matrix with the previous native
tail and the current logical restore, including the schema-55
reparse history. Compare the existing accepted data and ledger,
exercise the applicable down SQL on disposable copies and verify
the new table count, release checksum and exact fresh and restored
catalogs. Keep the module-specific Gateway, dataset and metering
acceptance alongside the shared matrix.

## Remaining migration queue

After the Audit scope migration, integrate GW-120, DAT-101 and the
metering ledger in that order, assigning each number from the actual
accepted tail. Each activation carries its release checksum, table
inventory, runtime grants, fresh and restored catalog fingerprints,
and native upgrade and logical restore tests. Repeat the module's
normal scoped HTTP flow on the numbered schema.

Additional owner candidates remain unnumbered. The
[Admin TOTP handoff](local-multifactor-authentication.md) adds three
relations and requires the typed startup policy and protected keyring.
The [GW-105 key-purpose handoff](gateway-key-purpose.md) alters
`api_keys` and requires admission and rotation replay with existing
credentials. The [artifact hold handoff](object-backup-fence.md)
requires durable store bindings, live lease fencing, write permits
and coordinated restore qualification. Their module owners retain
their source and normal-flow verification. Assign numbers only when
each candidate is admitted against the current predecessor.


## Accepted description tail

Schema 59 installs the scoped GW-120 owner up SQL without changing
any of the 58 predecessor entries. The [formal schema-59 acceptance](gateway-model-description.md#formal-schema-59-acceptance)
independently reproduced fresh, native 58-to-59, restored 55 through 58
and restored current catalogues on the pinned PostgreSQL stack. All
seven production migration/replay/check paths passed at 119 tables;
the four historical audit checkpoints were retained byte-for-byte.
The PostgreSQL-only runtime table list includes the new intent table.
Existing-role grant resume and exact serving refusal before grants
passed. MySQL's shared catalog and audit head access remain unchanged.
The complete workstation check, build and test passed on a446ec58.
These are Linux arm64 SQL results, distinct from the earlier native
Linux amd64 Audit admission.

GW-120 is therefore the accepted tail for subsequent ordering. DAT-101
and metering remain separate unnumbered candidates until their owners
supply ready inputs against this exact predecessor. Do not assign a
number to an incomplete handoff or reuse a prior overlay fingerprint.
