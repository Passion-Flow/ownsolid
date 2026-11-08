# Apply after PostgreSQL restore

## REL-112

The first administrator intent binds the request and its generated credential
to physical PostgreSQL catalog identifiers. That is required before a retry
can commit an unfinished bootstrap. A logical restore into a new database
creates new identifiers even when the data is identical.
[PostgreSQL's dump documentation](https://www.postgresql.org/docs/18/backup-dump.html)
describes the logical copy. The
[GitLab restore procedure](https://docs.gitlab.com/administration/backup_restore/restore_gitlab/)
also resumes the existing application and its restored credentials after
validation instead of creating a new administrator.

After the installer has migrated, verified the role and schema, and invalidated
restored authority, record adoption on the active database. Use an
[ALTER DATABASE setting](https://www.postgresql.org/docs/18/sql-alterdatabase.html)
read directly from [the database-level catalog](https://www.postgresql.org/docs/18/catalog-pg-db-role-setting.html).
PostgreSQL accepts [two-part custom settings](https://www.postgresql.org/docs/18/runtime-config-custom.html).
Bind the setting to the cluster system identifier, database OID, and
installation-state table OID. Check the live bootstrap completion bit.
Database defaults require the database owner or superuser. A per-role or
per-session setting must have no effect on the check. The existing restore
creates its own database before loading the dump, so it does not import the
database settings that [pg_restore --create](https://www.postgresql.org/docs/18/app-pgrestore.html)
would apply. The setting stays with the database if the installer renames it
aside, so failed startup and rollback cannot authorize the previous database
using the failed candidate's record.

Keep strict bootstrap reconciliation for an unmarked database. Do not weaken
physical intent binding, modify credential bytes, infer success from the
credential file, or trust a mutable host flag after a database swap. A
database restored by an older installer has no adoption record and still
requires the existing reconciliation. REL-114 tracks process loss across the
restore swap itself.

No dependency, model or dataset is introduced. The existing PostgreSQL
[licence](https://www.postgresql.org/about/licence/) permits commercial use,
offline use and redistribution with its required notices.

Verify fresh install and interrupted bootstrap still reconcile the same
credential. Verify a real logical backup restores over the live installation,
uses a different database OID, leaves the administrator, tenant, workspace
and credential intact, revokes restored sessions, and allows repeat apply.
Test that a forged session or role setting, an incomplete bootstrap and a
mismatched physical identity cannot claim adoption. Verify failure to record
adoption triggers rollback before starting the candidate. Run install,
backup, restore and apply with the Linux amd64 egress recorder as the only
default route and require zero product attempts.
