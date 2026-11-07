# Backup and restore

`ownsolid-install backup` copies everything a single-node installation needs to come back:
the control-plane database and the files that unlock it. `ownsolid-install restore` puts a
backup back, either over a running installation or onto a host that lost its installation.
Both run offline, as root, with the same `install.yaml` that `apply` uses.

## Take a backup

```sh
mkdir -p /srv/ownsolid-backups
ownsolid-install backup --config install.yaml --output /srv/ownsolid-backups
```

Serving keeps running. The command prints `BACKUP_COMPLETE path=<directory>` when it is
done. Each backup is a new directory, `ownsolid-backup-<UTC time>`, and an existing one is
never overwritten.

`--output` must be an existing directory outside `install_root`, so losing the installation
does not lose its backups. Copy backups to other storage as well: a backup on the same disk
does not survive that disk.

### What a backup holds

| File | Content |
| --- | --- |
| `database.dump` | The `ownsolid` database, from one consistent snapshot (`pg_dump` custom format) |
| `files/config`, `files/operator`, `files/secrets` | Session and mail keys, database identities, TLS material and the administrator credential |
| `files/data/models/objects` | Every published model, because serving will not start on a database that names a model it cannot find |
| `manifest.json` | Hashes and sizes of every file, the schema version, and a row count and checksum of every table |

A backup is not reported complete until its dump has been restored into a scratch database
and read back. The checksums in the manifest come from that restored copy, so a dump that
cannot be restored never produces a backup. A failed backup removes everything it wrote.

The backup holds the secrets of the installation, including the database passwords and the
key that encrypts queued mail. Keep it as carefully as `install_root` itself: mode 0700, on
storage only operators can read.

A backup copies the whole model store each time, so `--output` needs room for it.

Not in a backup: the telemetry stores (`data/metrics`, `data/logs`; OBS-113), the operator log of
past backups and restores, and uploads still in progress (`data/models/imports`). An import
interrupted by a restore fails verification and is discarded from the Model library.

## Restore

```sh
ownsolid-install restore --config install.yaml --from /srv/ownsolid-backups/ownsolid-backup-20261104T080000Z
```

Restore first checks every file of the backup against its manifest, and refuses a backup that
is incomplete or changed (`INSTALL_BACKUP_INVALID`, `INSTALL_BACKUP_CORRUPT`) before it touches
anything. Then it:

1. Stops serving, starts PostgreSQL and the telemetry stores, and puts back every model that is
   missing or damaged. A model that matches the backup is left alone, and models published since
   the backup stay in the store unused.
2. Loads the dump into a new database and compares every table with the manifest. If anything
   differs, the current database is left as it was and serving starts again.
3. Swaps the loaded database in under the name `ownsolid`. The database it replaces is kept as
   `ownsolid_before_restore_<time>`.
4. Applies this release's migrations, checks the schema and grants, and revokes every session,
   invitation, password reset and node credential the backup contained.
5. Starts serving and checks both origins.

If a step after the swap fails, restore swaps the previous database back and starts serving
on it. The message names the step that failed.

After a restore, everyone signs in again and node agents join again. Work recorded after the
backup was taken is gone.

### Restoring over a running installation

The installation keeps its own files. Its session and mail keys must be the ones in the
backup, because the database holds data encrypted with them: otherwise restore stops with
`INSTALL_RESTORE_KEYS_DIFFER` and changes nothing.

### Rebuilding a lost installation

On a host with Docker, the images loaded and an empty or absent `install_root`, restore takes
the files from the backup, creates the database roles and then loads the database. The
administrator credential and every key come back from the backup. Use the `install.yaml` of the
lost installation; its `install_root` may differ.

### Removing the previous database

Once the restored installation is confirmed, drop the database that restore kept:

```sh
docker compose --env-file <install_root>/compose.env -f <install_root>/compose.yaml \
  exec postgres psql -U ownsolid_migrator -d postgres -c 'DROP DATABASE "ownsolid_before_restore_<time>"'
```

## Limits

- Backup and restore are for the release that took the backup or a later one. Restore applies
  the newer release's migrations; it never downgrades a schema.
- `backup_seconds` in `install.yaml` (default 3600) bounds a whole backup or restore. Raise it
  for a large database.
- A backup, restore or `apply` refuses to start while another one runs on the same installation
  (`INSTALL_BUSY`).
- Scheduling, retention, encryption and point-in-time recovery are separate roadmap items
  (REL-108 to REL-110).

## Codes

| Code | Meaning |
| --- | --- |
| `INSTALL_BACKUP_OUTPUT_INVALID` | `--output` is missing, relative, a symlink, or inside `install_root` |
| `INSTALL_BACKUP_EXISTS` | A backup with this time already exists; run the command again |
| `INSTALL_DATABASE_UNAVAILABLE` | PostgreSQL does not answer; run `ownsolid-install status` |
| `INSTALL_BACKUP_DUMP_FAILED`, `INSTALL_BACKUP_VERIFY_FAILED`, `INSTALL_BACKUP_FILES_FAILED` | The backup failed and was removed; the cause is in the message |
| `INSTALL_BACKUP_INVALID`, `INSTALL_BACKUP_CORRUPT` | The backup is incomplete or changed; nothing was touched |
| `INSTALL_RESTORE_KEYS_DIFFER` | The installation's keys differ from the backup's; nothing was touched |
| `INSTALL_RESTORE_LOAD_FAILED`, `INSTALL_RESTORE_CONTENT_MISMATCH` | The backup did not load as recorded; the current database serves again |
| `INSTALL_RESTORE_SWAP_FAILED` | The databases could not be renamed; the current database serves again |
| `INSTALL_BUSY` | Another apply, backup or restore is running on this installation |

Each backup and restore, and its outcome, is appended to `<install_root>/operator/operations.log`.
