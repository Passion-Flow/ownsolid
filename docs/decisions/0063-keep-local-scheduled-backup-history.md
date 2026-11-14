# Keep local scheduled backup history

Use the existing scheduled backup job and its PostgreSQL, filesystem and
retention outcomes. Persist a bounded private snapshot at the installation
root using the same owner identity as the backup repository and the
installer's atomic private-file writer. Bind the first known database
deployment ID before publishing an archive and refuse a changed identity.

Hold an advisory lock on the installation directory for the scheduled
writer. A read-only query can observe an active run and detect a stale
start after the lock holder exits. The next admitted run preserves the
interruption and starts a new sequence. Retain the publication fact
independently of archive retention, and omit uncertain timestamps.

Expose only a versioned local JSON projection. Keep paths, raw diagnostics,
keys and contents behind the installer boundary. The Control API, Console
and failure alert policy are separate consumers.

See [REL-111 research](../research/scheduled-backup-history.md) and
[operator instructions](../../deploy/systemd/README.md#read-scheduled-backup-history).
