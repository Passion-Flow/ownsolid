# 0053. Restore adopts the verified database

- Status: accepted
- Date: 2026-11-09

The initial administrator intent authorizes a bootstrap retry on one physical
PostgreSQL target. A logical restore creates a new target and recovers the
administrator from the dump. Reusing the old intent to authorize first-run
mutation would weaken that boundary.

After schema and grant checks and revocation of restored authority, restore
sets an installer-owned PostgreSQL database default. Its value binds the
adoption to the PostgreSQL system identifier, database OID and
installation-state table OID. Apply reads the database-level catalog and
checks that the bootstrap is complete. It can then preserve the existing
administrator without replaying the original intent. A role or session
override cannot provide this evidence.

The record travels with the candidate database when it is renamed. Rolling
back a candidate never writes or transfers its record to the prior database.
The current logical dump and restore procedure uses a new target and does
not restore database defaults. Physical backups and older logical restores
have their own qualification paths.

See [restore apply research](../research/restore-apply.md) for the PostgreSQL
mechanism, compatibility and acceptance checks.
