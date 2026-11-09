# PostgreSQL upgrades after logical restore (REL-113)

## Upstream and comparable behavior

PostgreSQL [pg_dump](https://www.postgresql.org/docs/18/app-pgdump.html) writes
a consistent logical snapshot. Its custom archive is replayed by
[pg_restore](https://www.postgresql.org/docs/18/app-pgrestore.html), which executes
SQL to reconstruct the schema and restore rows and grants. PostgreSQL describes
[pg_get_constraintdef](https://www.postgresql.org/docs/18/functions-info.html) as a
decompiled reconstruction of a constraint. Its
[deparser](https://github.com/postgres/postgres/blob/REL_18_STABLE/src/backend/utils/adt/ruleutils.c)
works from the stored parse tree. The parse tree can change when the reconstructed
SQL is parsed again. A text difference is evidence about the catalog and does not
by itself establish a difference in enforcement. GitLab's self-managed
[restore procedure](https://docs.gitlab.com/administration/backup_restore/restore_gitlab/)
restores to the original application version first and calls for a full restore
test before production use. That separation lets this fixture distinguish restore
from upgrade.

PostgreSQL server and clients and [pgvector 0.8.7](https://github.com/pgvector/pgvector/blob/v0.8.7/LICENSE)
use the [PostgreSQL License](https://www.postgresql.org/about/licence/). It permits
commercial use, offline use and redistribution with the required notices. The
fixture reuses the staged image, Docker, the standard Go test toolchain and the
in-repository egress recorder. It adds no runtime dependency, model or dataset.
The rows are synthetic and created by the fixture.

## Approach and acceptance

Build the control binary from the last repository commit at schema 55 and from
the commit introducing 000056. Use the candidate control binary for the current
release. On a single disposable PostgreSQL instance, take a logical backup of the
historical schema and restore into empty databases with the shipped installer
dump and load helpers. Retain a native predecessor as the control case. Compare
the migration ledger, the installed constraints in `pg_catalog`, and synthetic
rows before and after the real control migration commands. Run each operation
again to distinguish stable refusal or success from a partial or one-time outcome.
Keep the database roles and original migration bytes. The egress recorder is the
only default route in the PostgreSQL and control namespace.

A copy of PostgreSQL's data directory would preserve the original parse tree and
miss the reconstruction bug. Handcrafted DDL would lose the historical migration
ledger, exact grant setup and the real catalog admission decision. Accepting all
text differences or editing an applied migration could hide real schema drift or
invalidate published checksums. The fixture checks the observed outcome against an
explicit expectation. Release acceptance requires a restored database to pass
the real migration command, reach the same ledger as the native database, retain
its records and pass again. While the failure is under investigation, a
reproduction run must show an actionable boundary, a stable failure code, no
partial migration, and unchanged data. The shared database owner owns production
catalog admission.

## Linux amd64 acceptance

The fixture ran on `ownsolid-cpu` with PostgreSQL 18.6 and pgvector 0.8.7
from the installer-pinned image. Control at `3414559f` produced schema 55,
and the historical `f98a160d` binary was the first schema 56 target. The
schema 57 candidate came from `2aaaeebe`. All databases were in one disposable
instance and used its local Unix socket.

| Path | Result |
| --- | --- |
| Native schema 55 to 56 | Migration and repeat returned `MIGRATION_COMPLETE`, and schema verification returned `SCHEMA_CURRENT`. |
| Schema 55 logical restore to 56 | The predecessor accepted the restored schema. Two migration attempts returned exit 1 and `MIGRATION_FAILED`. Both left the schema 55 ledger, catalog and 116 table fingerprints unchanged. |
| Fresh schema 57 and native 56 to 57 | Both reached the same ordered migration checksums. The native upgrade retained the seeded tenant, workspace, user and node enrollment row. |
| Schema 55 logical restore to 57 | Two attempts returned exit 1 and `MIGRATION_FAILED`. Both left the schema at 55 and retained the original ledger, catalog and data. |

Before migration, 168 of 825 check constraint rows differed in their deparsed
definitions or expressions. After executing the checksum-verified 000056 SQL
on the disposable diagnostic copy, 165 of 835 differed. For example,
`tenants_status_check` deparses a cast of the array to `text[]` on the native
path and casts the individual array elements to `text` on the restored path.
The historical release's read-only `check-schema` reported
`SCHEMA_CATALOG_INCOMPATIBLE` on that diagnostic copy after its ledger
contained the published 000056 checksum.

The container had no published ports and its IPv4 and IPv6 default routes
pointed only to the egress recorder. The recorder observed the IPv4, IPv6 and
DNS probes before PostgreSQL started and no further packets through
shutdown. See the [reproduction instructions](../../deploy/offline/README.md#compare-upgrades-before-and-after-a-logical-restore).
