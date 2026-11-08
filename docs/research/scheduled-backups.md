# Scheduled single-node backups

## REL-108

[GitLab Self-Managed](https://docs.gitlab.com/administration/backup_restore/backup_gitlab/#configuring-cron-to-make-daily-backups)
runs its existing application backup command on a host schedule.
[pgBackRest](https://pgbackrest.org/user-guide.html#retention) expires
backups only after a successful replacement is complete.
[systemd timers](https://github.com/systemd/systemd/blob/v255/man/systemd.timer.xml)
support calendar schedules, serialized activation of a oneshot service and a
catch-up invocation after a missed interval.

Ship an opt-in timer and service for Linux amd64. The service runs the
existing installer backup under the same installation lock, uses the native
PostgreSQL dump and scratch restore verification, and prunes only after a
verified backup has been published. Take the calendar from the site timer
configuration and the absolute backup directory and keep count from typed
installer configuration. The operator can run the same job on demand.

Require a private, dedicated directory outside the installation. Bind its
owner record to the site project, installation path and database deployment
ID. Refuse to claim a nonempty directory. A separate generated archive name
prefix limits expiration to archives the scheduler creates. Ordinary backups,
partial archives and unexpected entries are never expired. A second installation
that selects the same directory is refused.

Reuse the host's systemd. The [upstream licence inventory](https://github.com/systemd/systemd/blob/v255/LICENSES/README.md)
describes the LGPL-2.1-or-later default and component exceptions. No systemd
binary is redistributed by this change. The Go installer and PostgreSQL
utilities retain their existing licences. No new application dependency,
model or dataset is required. Reject an always-on scheduler in control because
it would require a privileged path from the application to the host's backup
and Docker authority. Reject time-based deletion by arbitrary filename or
filesystem mtime.

Verify protected directory and ownership checks, failed backup preserving
the last recovery point, successful expiry, collisions with a second
installation and unrelated files remaining untouched. Exercise the scheduled
job against real PostgreSQL on the isolated Compose installation. Verify a
Linux systemd timer starts the oneshot command and records its result.
