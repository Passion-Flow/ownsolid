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

## Encrypt an operator-run backup

Generate an [age X25519 identity](https://github.com/FiloSottile/age/blob/v1.3.2/README.md)
on an offline recovery station. Transfer only the public recipient to the
backup host. Store the private identity on separately controlled recovery
media and keep a verified spare. If the identity is lost, the archive
cannot be decrypted. For a new key, generate another pair, use the new
recipient for new backups, and retain the old identity until its last
archive expires.

```sh
umask 077
age-keygen -o /mnt/recovery/ownsolid-identity.txt
age-keygen -y /mnt/recovery/ownsolid-identity.txt > /path/to/ownsolid-recipient.txt
```

On the backup host, install the recipient as a root-owned mode 0600 file.
Make the archive directory root-owned mode 0700 and keep it outside
`install_root`. Use absolute paths without symlinks and a filesystem
that supports hard links and directory synchronization. Back up to a new
filename each time.

```sh
ownsolid-install backup --config install.yaml \
  --output /srv/ownsolid-encrypted/20261109T080000Z.tar.age \
  --recipient-file /etc/ownsolid-backup/recipient.txt
```

The command checks the PostgreSQL dump by restoring it to a scratch
database, then publishes the complete encrypted archive with mode 0600.
The repository receives ciphertext only. Allow space under `install_root`
for one plaintext staging copy and in the repository for the ciphertext.
A failed run can reuse an unpublished filename. If the archive already
exists, retain it and select a new name for a new recovery point.

For recovery, mount the protected identity outside both the archive
directory and `install_root`. Use a root-owned regular file with mode
0400 or 0600. Keep the archive directory at mode 0700 and the archive at
mode 0400 or 0600.

```sh
ownsolid-install restore --config install.yaml \
  --from /srv/ownsolid-encrypted/20261109T080000Z.tar.age \
  --identity-file /mnt/recovery/ownsolid-identity.txt
```

Restore authenticates the entire archive and checks the manifest and file
digests before changing the database. It then follows the same restore
and recovery procedure below. The installer has no default key and needs
no age executable. Keep the identity out of backups, configuration
values, command arguments and environment variables. Only the file path
is passed as an argument. Repository permissions protect archive
provenance because anyone who has the public recipient can create
ciphertext for it.

A process killed without cleanup can leave plaintext under the private
`<install_root>/.encrypted-backup-work/data` and an unpublished ciphertext
file whose name starts with `.ownsolid-encrypted-partial-`. The next
encrypted operation that reaches staging removes the old staging copy.
Once no backup is running, an operator can remove the exact leftover
partial file. Keep the install root on protected storage.

## Restore

```sh
ownsolid-install restore --config install.yaml --from /srv/ownsolid-backups/ownsolid-backup-20261104T080000Z
```

Restore first checks every file of the backup against its manifest, and refuses a backup that
is incomplete or changed (`INSTALL_BACKUP_INVALID`, `INSTALL_BACKUP_CORRUPT`) before it touches
anything. Then it:

1. Starts PostgreSQL and the telemetry stores, loads the dump into a new database,
   and compares every table with the manifest while the original database can still
   serve. A dump that fails verification leaves serving on the original database.
2. Records both database identities, stops serving, and restores missing or damaged
   model files. Matching files and models absent from the backup stay in the store.
3. Swaps the checked database in under the name `ownsolid` in one PostgreSQL
   transaction. It retains the original as `ownsolid_before_restore_<id>`.
4. Applies this release's migrations, checks the schema and grants, and revokes
   every session, invitation, password reset and node credential the backup held.
5. Records that the candidate can accept writes, starts serving and checks both origins.

If an observed failure requires rollback, the installer puts the original
database back and restarts serving. It keeps the candidate as
`ownsolid_failed_restore_<id>` so any writes it accepted can still be inspected.

### Recover an interrupted restore

If the installer is killed, the host restarts, or a command reports
`INSTALL_RESTORE_PENDING`, run this on the same installation:

```sh
ownsolid-install recover-restore --config install.yaml
```

Use the original site configuration. You can increase `backup_seconds` or
`step_seconds` to give recovery more time. The command needs the existing
installation files and PostgreSQL cluster. The backup directory can be offline.

- `RESTORE_ROLLED_BACK` means the original database is back. This is the
  normal outcome when the exchange never committed or rollback was already
  decided. On a fresh target there is no original application to start.
- `RESTORE_COMPLETE` means the checked replacement is serving. If it had
  already started serving, recovery keeps writes made since the restore.
- `RESTORE_NO_RECOVERY_PENDING` means there is no unfinished recorded exchange.
  An interruption during scratch loading can leave an unreferenced
  `ownsolid_restore_<id>` database, with the original still in service.

If the candidate cannot start, retry after fixing the cause or run
`recover-restore --config install.yaml --rollback` to serve on the original.
The candidate, including any writes it accepted, stays in
`ownsolid_failed_restore_<id>` and is no longer the live database.

If the recorded cluster or OIDs disagree with PostgreSQL, recovery stops
before starting control. Preserve the record at
`<install_root>/restore-intent.json` and the databases for inspection.
While a restore is pending, the installer refuses apply, backup, restore,
upgrade and release rollback. Leave Compose service starts and database
renames to the recovery command until it finishes.

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

Once the restored installation is confirmed, drop the database that restore kept. Use the exact name printed by the
installer. A failed candidate can be removed the same way once its contents
are accounted for:

```sh
docker compose --env-file <install_root>/compose.env -f <install_root>/compose.yaml \
  exec postgres psql -U ownsolid_migrator -d postgres -c 'DROP DATABASE "ownsolid_before_restore_<id>"'
```

## Limits

- Backup and restore are for the release that took the backup or a later one. Restore applies
  the newer release's migrations; it never downgrades a schema.
- `backup_seconds` in `install.yaml` (default 3600) bounds a whole backup or restore. Raise it
  for a large database.
- A backup, restore or `apply` refuses to start while another one runs on the same installation
  (`INSTALL_BUSY`).
- Encryption is available for explicit logical backup and restore. The
  scheduled backup, upgrade checkpoint and physical WAL formats are
  independent. Protect the storage where those are kept.

## Codes

| Code | Meaning |
| --- | --- |
| `INSTALL_ENCRYPTED_BACKUP_EXISTS` | A published archive already has that name. Keep it and choose a new output name. |
| `INSTALL_ENCRYPTED_BACKUP_WRONG_IDENTITY` | Use the protected identity matching the archive's recipient. |
| `INSTALL_ENCRYPTED_BACKUP_INVALID` | Restore from an intact ownsolid encrypted archive. |
| `INSTALL_BACKUP_OUTPUT_INVALID` | `--output` is missing, relative, a symlink, or inside `install_root` |
| `INSTALL_BACKUP_EXISTS` | A backup with this time already exists; run the command again |
| `INSTALL_DATABASE_UNAVAILABLE` | PostgreSQL does not answer; run `ownsolid-install status` |
| `INSTALL_BACKUP_DUMP_FAILED`, `INSTALL_BACKUP_VERIFY_FAILED`, `INSTALL_BACKUP_FILES_FAILED` | The backup failed and was removed; the cause is in the message |
| `INSTALL_BACKUP_INVALID`, `INSTALL_BACKUP_CORRUPT` | The backup is incomplete or changed; nothing was touched |
| `INSTALL_RESTORE_KEYS_DIFFER` | The installation's keys differ from the backup's; nothing was touched |
| `INSTALL_RESTORE_LOAD_FAILED`, `INSTALL_RESTORE_CONTENT_MISMATCH` | The backup did not load as recorded; the current database serves again |
| `INSTALL_RESTORE_SWAP_FAILED` | The databases could not be renamed. If rollback also failed, use `recover-restore` |
| `INSTALL_RESTORE_PENDING` | Run `recover-restore` before other installer maintenance |
| `INSTALL_RESTORE_SITE_CHANGED` | Use the site configuration that started the restore |
| `INSTALL_RESTORE_IDENTITY_CHANGED`, `INSTALL_RESTORE_INTENT_INVALID` | Preserve the recovery record and both databases for inspection |
| `INSTALL_BUSY` | Another apply, backup or restore is running on this installation |

Each backup and restore, and its outcome, is appended to `<install_root>/operator/operations.log`.

## PostgreSQL point-in-time recovery

Enable `pitr` in the single-node [installer configuration](../../deploy/offline/install.example.yaml).
Use a new, empty, root-owned directory with mode 0700 outside `install_root`.
Run `apply` or the equivalent signed apply during a maintenance window to
restart PostgreSQL with WAL archiving and summaries enabled. The installer
binds this repository to the installation. Put it on storage sized for full
and incremental backups and continuous WAL. Monitor free space and
`pg_stat_archiver`. An archive that cannot accept writes can eventually fill
the primary's WAL storage.

Create the first full base, then incremental bases that refer to a verified
parent. A parent must use the same PostgreSQL 18 image, cluster and timeline.
A parent whose WAL summaries have expired requires a new full base.

```sh
ownsolid-install pitr backup --config install.yaml --id full-01
ownsolid-install pitr backup --config install.yaml --id incremental-01 --parent full-01
```

The same ID and parent can be retried. The installer verifies the complete
chain and refuses a different parent under an existing ID. The archive
timeout defaults to 60 seconds and the summary retention defaults to 240
hours. Archived WAL stays until an explicit prune. Incremental creation
uses the server's WAL summaries, independently of archived WAL retention.

Choose a UTC time after the selected base finished and within the retained
archive. Use RFC3339 with a trailing `Z` and at most six fractional digits. Replay runs in a separate PostgreSQL container with no network.
A missing segment or an unreached target leaves the live database serving.
The command publishes a checked logical recovery point under
`<pitr.directory>/recovered/<id>` and prints its path.

```sh
ownsolid-install pitr recover --config install.yaml --id incident-01 \
  --base incremental-01 --target 2026-11-08T12:34:56Z
ownsolid-install restore --config install.yaml \
  --from /srv/ownsolid-pitr/recovered/incident-01
```

Repeating the same recovery ID, base and time returns the existing export.
Use the separate `restore` command when ready to switch the live database.
It checks the source installation, cluster, schema and encryption keys,
then performs the normal checked exchange and revokes restored credentials.
Retrying the same artifact while it is active preserves later writes.
The current model store must still contain the recovered database's ready
models. This procedure restores the database on its existing installation.
Coordinated recovery of external files and objects is REL-103.

Expire older bases and WAL by selecting a newer verified **full** base.

```sh
ownsolid-install pitr prune --config install.yaml --keep-from full-02
```

Prune retains newer bases and every predecessor they need. A newer
incremental that still depends on an older full base keeps that full base
and its WAL. Prune checks the retained WAL range for gaps and refuses
mixed timelines. It removes only eligible backups and ordinary WAL segments
in this repository's current cluster partition. Recovery exports and
unmanaged entries remain available after pruning.
