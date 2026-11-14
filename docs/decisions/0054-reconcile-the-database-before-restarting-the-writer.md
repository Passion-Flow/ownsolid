# Reconcile the database before restarting the writer

A PostgreSQL restore exchanges the old and incoming database names in one
transaction. The transaction can commit even when the installer never receives
its result. The name `ownsolid` alone cannot say which database it contains.

Persist a private restore record before creating the scratch database. Bind it
to the site configuration, PostgreSQL cluster identifier and original OID.
Serialize creation and recovery with the same PostgreSQL advisory lock. Bind
the created scratch OID before loading the dump. If recovery finds a database
without that binding, preserve it for manual inspection. Verify the load
before admitting the exchange or stopping the writer. An interrupted load can
move only the bound scratch database to the failed name while the original
stays online. Check both OIDs inside the rename transaction.
A reverse rename puts the candidate under a distinct name, so a delayed forward
transaction cannot move it back. Keep both databases for inspection.

On restart, finish adopting a committed exchange or put the original database
back when the exchange did not commit or rollback was already decided. Persist
candidate admission after PostgreSQL flushes adoption, before starting
control. Recovery can then restart a serving candidate without reapplying
the backup or revoking sessions it has issued since promotion. A failed retry
leaves a promoted candidate in place. An operator can explicitly switch to
the original and keep the candidate, including its writes.

Apply, backups, other restores and signed release transitions cannot pass the
installer operation lock while a restore record exists. Read-only status and
support remain available. The recovery command needs the same installation
files and cluster. It does not depend on the backup directory.

See [REL-114 research](../research/interrupted-database-restore.md) and the
[operator procedure](../operations/backup-and-restore.md#recover-an-interrupted-restore).
