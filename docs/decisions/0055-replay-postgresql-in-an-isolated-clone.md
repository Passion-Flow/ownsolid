# Replay PostgreSQL in an isolated clone

A base backup and archived WAL can recover a PostgreSQL cluster to an earlier
time. Starting that physical copy as the installation's database would also
restore cluster roles and settings and could admit the writer before
application checks complete.

The single-node installer archives WAL into its own cluster namespace.
It verifies full and incremental bases with the installed PostgreSQL 18
image and pins their dependency chain. Expiry preserves the complete
dependency closure and checks WAL continuity before deletion.

Replay to an explicit UTC time in a disconnected clone. Export the database
through pg_dump and verify its logical round trip. Publish the export
atomically under an operator-selected ID. Repeated replay requests return the
verified export. The existing restore command performs the separate writer
transition after checking the original site, PostgreSQL cluster, schema and
keys. It keeps the original database available for rollback.

See the [REL-109 research](../research/point-in-time-recovery.md) and the
[operator procedure](../operations/backup-and-restore.md#postgresql-point-in-time-recovery).
