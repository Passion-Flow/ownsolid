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

## Recover an interrupted logical load

A single-database PostgreSQL [custom archive restore](https://www.postgresql.org/docs/18/app-pgrestore.html)
can commit individual commands before a later command fails. Even when a
restore uses a single SQL transaction, that transaction cannot cover
[CREATE DATABASE](https://www.postgresql.org/docs/18/sql-createdatabase.html).
Changing the archive transaction mode cannot tell the installer which
database it created or whether its tables passed fingerprint verification.

Persist a loading phase in the existing recovery record immediately after
PostgreSQL supplies the scratch database OID and before opening the archive.
The original can keep serving while the scratch database is loaded and
checked. Only a successfully verified load can advance the record to pending
exchange. Loading recovery validates the cluster and exact original and
candidate placement, terminates only the scratch connections, and moves the
candidate to the existing failed name in a guarded transaction. It leaves the
original writer running. A repeat after the guarded rename can clear the
record. A completed cleanup that left the exact original and neither
candidate name can also clear it. An unknown placement refuses without
changing either database.

Preserve immediate cleanup for a completed load command that reports failure
while the installer still owns its operation lock. Check the exact scratch
identity first. If cleanup is uncertain, retain the recovery record. An
interrupted or unacknowledged load keeps its candidate for inspection. The
short interval before a successful CREATE result has been tied to an OID is
still outside automatic recovery. An unbound name is insufficient authority
to remove a database. PostgreSQL's optional explicit OID is intended for
upgrade tooling, so the installer leaves OID allocation to PostgreSQL.

Acceptance includes a real pg_restore followed by loss of the installer
process before it observes load completion, while the existing writer remains
ready. Recover without the archive, verify the original OID and live writes,
and confirm that the candidate OID is preserved under the failed name. Also
test a lost response to the recovery rename, a substituted name, and ordinary
invalid archives. Reuse the PostgreSQL and Go dependencies listed above.

### Disconnected load recovery

On 2026-11-09, a targeted run on `ownsolid-cpu` drove the installer Backup,
Restore and RecoverRestore entry points against PostgreSQL 18.6. The same
one-container adapter described in the [DEL-103 acceptance](first-run-bootstrap.md#linux-amd64-acceptance)
ran native Control and forwarded the actual pg_dump and pg_restore streams.
It used no published ports or telemetry services. The installer verified the
logical backup across 118 tables.

The adapter sent SIGKILL to the Restore process immediately after real
pg_restore completed, before the installer verified the candidate or stopped
Control. Both HTTPS entrances still returned ready. The recovery record
identified the original and scratch OIDs. With the backup directory moved
away, RecoverRestore kept the original OID, its later tenant write, and the
same Control PID. It moved the scratch OID and the original backup data to
the reported failed name. Both origins still returned ready. A second
recovery reported `RESTORE_NO_RECOVERY_PENDING`.

The recorder captured the IPv4, IPv6 and DNS probes and zero product
attempts. The disposable container and namespace were removed. The focused
tests additionally simulated a lost recovery rename response, a substituted
OID, an already-removed scratch database, and an unexpected promotion. The
full Compose regression adds the same process-loss checkpoint to
`TestPreflightIsolatedDocker`.
