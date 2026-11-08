# Interrupted single-node database restore (REL-114)

The installer loads a logical backup into a scratch database, checks every table,
and swaps two names in a PostgreSQL transaction. A killed installer or a lost
client response can leave that transaction committed even though the caller never
observed success. Starting control based on the last printed step can therefore
select the wrong database.

## Upstream behavior

- PostgreSQL 18 [ALTER DATABASE](https://www.postgresql.org/docs/18/sql-alterdatabase.html)
  permits renaming databases from a separate maintenance database. The
  [18.6 implementation](https://github.com/postgres/postgres/blob/REL_18_6/src/backend/commands/dbcommands.c)
  checks for connected backends and updates the catalog row in place.
  [pg_database](https://www.postgresql.org/docs/18/catalog-pg-database.html) is a
  cluster-wide catalog, so a database name and its OID can be read together from
  the maintenance database. [pg_control_system](https://www.postgresql.org/docs/18/functions-info.html#FUNCTIONS-CONTROLDATA)
  supplies the cluster identifier. Force local WAL flush for the rename
  transaction even if the installation defaults to asynchronous commits.
- PostgreSQL [advisory transaction locks](https://www.postgresql.org/docs/18/explicit-locking.html#ADVISORY-LOCKS)
  are released at transaction end. Serialize guarded rename statements under
  the same lock and check the recorded OIDs inside the transaction.
- [pgBackRest recovery](https://pgbackrest.org/user-guide.html#restore/delta-option)
  keeps PostgreSQL stopped and removes the control file until its physical
  restore is complete. The equivalent admission boundary here is the
  application writer. PostgreSQL itself stays up to load, check and identify
  logical databases.
- Linux [fsync(2)](https://man7.org/linux/man-pages/man2/fsync.2.html) requires
  syncing the containing directory to make a file rename durable. Reuse the
  installer's atomic private record writer, which syncs both.

No new dependencies, models or datasets. The existing PostgreSQL tools use
the [PostgreSQL License](https://www.postgresql.org/about/licence/) and the Go
standard library uses the [Go BSD license](https://go.dev/LICENSE). Both permit
commercial use, redistribution with their notices, and offline use.
pgBackRest is a comparison only.

## Decision

While the original database is still online, load and fingerprint the scratch
database. Before stopping the writer or changing live database names, persist
an installation-local record with the cluster identifier, original and scratch
database OIDs, a unique name suffix, and the installed Compose identity. The
record stores no key material. Hold the existing installer operation lock and
make other mutating installer commands refuse pending recovery.

Run recovery without the backup directory. If the forward rename did not
commit, put the original database back into service and rename the scratch
database away from its forward-rename name. This fences even a delayed forward
request. If the rename committed, finish adoption on the verified database.
Persist permission to start the writer after schema and revocation checks.
After this point, an interrupted start is retried on the same database without
replaying revocation. For an observed restore failure, persist the rollback
decision first, restore the original by OID, and keep the candidate under a
distinct diagnostic name. It may contain writes if control had already started.
A failure after an uncertain command response is resolved from the catalog.
Unknown identities or placements block recovery and preserve both databases.
No branch may serve an unadopted candidate.

Names alone can be reused and a host-side phase alone cannot resolve a lost
transaction response. A backup file cannot provide these live identities.
Reapplying the dump after the writer may have started would discard intervening
writes. Physical file copies and raw data directory swaps are outside this
logical backup path.

## Verification

Run installer subprocesses and terminate them at the load/stop boundary, before
rename, after PostgreSQL commits without delivering its response, during
rollback, and after control starts. On Linux amd64 with real PostgreSQL, check
the cluster identifier, exact OIDs, original and restored data, readiness of
both entrances, and durable writes after restart. Also test a lost rollback
response, a catalog identity mismatch, and competing installer commands.
Exercise backup with pg_dump and pg_restore in the egress-recorder network
namespace. A verified candidate and its original must remain separately
identifiable throughout recovery. Test with signed and unsigned installation
entry-point guards as applicable.
