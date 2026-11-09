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

### Timer-to-archive verification

A successful direct job and a separate systemd probe leave the command
search path and the timer's actual backup invocation unverified. Run the
shipped service and timer under unique runtime unit names on Linux. Set
the installer executable search path and site configuration in a test
drop-in. Override the daily schedule for the bounded drill. Use
[systemd execution settings](https://github.com/systemd/systemd/blob/v255/man/systemd.exec.xml)
to join the isolated PostgreSQL container's network namespace, with the
egress recorder as the only default route. Use the real
[Compose exec path](https://docs.docker.com/reference/cli/docker/compose/exec/)
and the pinned PostgreSQL image with local sockets and no published ports.

Migrate the disposable database with the real Control binary and keep the
normal installer configuration and generated key files. Require a timer
run to publish a logical dump and a completed history entry. Pause the
fixture's PostgreSQL container for a second trigger and require a failed
history entry and unchanged recovery point. Resume it and require a new
verified backup to expire only the previous owned one. Independently load
the retained dump and compare the deployment identity. Leave the live
database OID and the ordinary backup directory intact. This drill uses
only the existing systemd, Compose, PostgreSQL and ownsolid binaries and
adds no dependency or redistributable data.

On Ubuntu 24.04 amd64, systemd 255 activated the shipped timer and
oneshot service under private runtime names. With no schedule, the
installer returned `INSTALL_BACKUP_SCHEDULE_REQUIRED` before creating
an installation. A test root with group and other access was refused
by the history reader and writer. With a mode-0700 root, a schedule,
and an unavailable fixture Docker socket, two timer activations exited
with `INSTALL_BACKUP_OWNER_UNAVAILABLE`. The real `backup-history`
command retained two finished, failed entries in order. It reported no
deployment ID or verified publication, and the backup directory stayed
empty. The runtime units were removed. Both network defaults led only
to the egress recorder, which saw the IPv4, IPv6 and DNS probes.

The combined timer, PostgreSQL success and retention drill remains open.
It needs the exclusive shared test lock and a passing host resource
preflight before the fixture starts. The earlier manual scheduled job
with real PostgreSQL remains separate evidence.
