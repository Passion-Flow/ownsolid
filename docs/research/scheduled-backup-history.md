# Scheduled backup history on the installation host (REL-111)

The scheduled installer already runs the real PostgreSQL dump, scratch
restore verification and owned archive retention. Its private
`schedule-owner.json` binds a repository to the database deployment ID,
Compose project and installation root. It cannot say whether a backup was
attempted, failed or interrupted. The operator log has free-form details,
omits early failures and records publication before retention. The
coordinated backup and restore files describe specific recovery intents.
They are removed when reconciled and are unsuitable as history.

[Kubernetes CronJobs](https://kubernetes.io/docs/concepts/workloads/controllers/cron-jobs/#jobs-history-limits)
keep bounded terminal run history independently of Job scheduling.
[Velero](https://velero.io/docs/v1.19/how-velero-works/) tracks backup
operation status separately from archive expiry. Keep those two concepts
separate in the local scheduled job. The existing PostgreSQL
[logical dump](https://www.postgresql.org/docs/18/app-pgdump.html) and
verification remain the publication authority. No new dependency, model
or dataset is added. The installer uses the Go standard library
([BSD-3-Clause](https://go.dev/LICENSE)) and the existing PostgreSQL client
([PostgreSQL License](https://www.postgresql.org/about/licence/)).
Commercial and offline use and redistribution remain subject to those
licences.

Persist a bounded, versioned snapshot in the private installation root,
outside the material copied into backups. Reuse the existing repository
owner identity, which includes the project and installation root and gains
its deployment ID from PostgreSQL when first available. Record an admitted
run before opening Docker or the backup repository so an outage can be
seen. A different live deployment ID fails before writing a backup.
Read-only queries verify the local installation binding and can return
the last verified deployment ID while PostgreSQL is down.

Only the scheduled writer holds the installation directory's advisory lock.
A reader can distinguish a running writer from an interrupted attempt
without interpreting another installer operation's lock as a backup.
[Linux flock](https://man7.org/linux/man-pages/man2/flock.2.html) releases
the lock when the process exits. Persist with the installer's existing
atomic private-file writer and directory sync, following the
[fsync durability contract](https://man7.org/linux/man-pages/man2/fsync.2.html).
A retry carries forward the interrupted attempt and starts a new sequence.
No exit time is inferred after a crash. Publication is confirmed only after
the existing backup call has verified and synced it. Persist that fact
before retention so a later retention failure can still show that the new
recovery point was published. A crash before the publication checkpoint
leaves publication unconfirmed.

Expose only the version, deployment ID when known, attempt sequence,
timestamps, outcome, a bounded error code, verified publication and the
number of archives expired when retention completed. Publication describes
the recorded event. Archive availability is a separate concern. Preserve
the latest 256 attempts even when their backup directories are expired.
Do not expose installation paths, filenames, vendor diagnostics, DB
details, keys or user data. Reject treating raw logs, filesystem mtimes or
the presence of a partial archive as durable success. Do not introduce a
second scheduler or a database service for the local status.

Verify the normal scheduled entry with controlled Docker command responses
and real local filesystem writes. Cover owner lookup and dump failure,
successful retention, an interrupted process and a retry, cross-install
refusal, bounded history, invalid state and output redaction. Keep the
previous real PostgreSQL evidence for the backup mechanism separate from
these history tests. The future API should compare the deployment ID with
its authenticated installation context and consume the projected fields.
Console and alert policy remain open.
