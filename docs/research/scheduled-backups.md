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

### Resume the successful timer drill

Resume after the ARC 000058 migration acceptance, from a clean isolated
checkout that contains the accepted migration commit and the bound
`backup-history` reader. Build the Linux amd64 installer, Control operator,
materialization test and egress recorder from that checkout. Stage the
unchanged service and timer and record the source SHA and every input
digest. Use the cached PostgreSQL 18.6 and pgvector 0.8.7 image
`pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47`.
The prepared fixture uses one isolated Compose PostgreSQL service and the
real native Control migration. It requires schema 58 or later and compares
the observed schema and table count to each restored dump. The prior
[encrypted backup and direct restore](encrypted-backups.md#release-qualification)
provide separate acceptance for the age archive and recovery identity.

The continuation wrapper takes the checkout and the full SHA of the
accepted ARC migration commit.

```sh
bash "$rel108_fixture/resume.sh" "$ownsolid_worktree" "$accepted_arc_commit"
```

The wrapper verifies source ancestry, the exact ARC up and down SQL files
and input hashes. It checks disk,
available memory, I/O pressure and ports before acquiring the shared
test lock, and rechecks resources under the lock. It exits 75 without
starting the fixture when the lock or resource preflight refuses it.
All timer invocations stay in the disposable PostgreSQL namespace,
whose IPv4 and IPv6 defaults terminate at the egress recorder.

| Trigger | Archive check | Expected durable history |
| --- | --- | --- |
| First success | Read back the manifest and exact dump digest, then restore into a scratch database and compare the deployment ID | Succeeded with verified publication and zero expired backups |
| Second success with retention set to one | The first owned archive is absent, the new dump can be restored, and the ordinary backup remains | Both success rows remain. The new row records one expired backup |
| Inject a symlink as the owner marker in a fixture-only canonical predecessor directory | The preceding archive stays intact. The new publication can be restored independently | Failed with `INSTALL_BACKUP_RETENTION_FAILED`, verified publication and an omitted expiry count |
| Remove the exact fixture marker and retry | The two older owned archives are absent. The new dump and ordinary backup remain | The retention failure stays visible and the new success records two expired backups |

Require the live PostgreSQL OID to stay constant and capture only the
deliberate IPv4, IPv6 and DNS probes. Preserve the native timer exit
status, archive and manifest checks, bound history, input hashes and
egress transcript. The fixture source passed shell syntax and checksum
verification, and its ARC gate refused an unrelated commit. Its source archive SHA-256 is
`a9f83539a38f5823b9b5d29e4c3fdcbf1cb98cfb3d0364a5e67d6ea717c278f8`.
The combined run remains queued behind ARC acceptance and the shared host
preflight. The earlier failure drill remains separate.

### Recovered timer continuation

The timer, materialization, logical restore and retention failure/retry
sources were recovered from their original command records. The recovered
wrapper holds `/run/lock/ownsolid-heavy.lock`, followed by
`/root/lane-sci-tools/heavy-test.lock`, throughout the Linux run. It
rechecks resources after acquiring both. The scheduled delivery window
follows the accepted Audit and Gateway qualifications. Use a clean
checkout containing the accepted ARC migration and its exact SQL bytes.
The fixture observes that checkout's live schema and table count and
requires every independently restored archive to match them.

Shell parsing and source checksum verification passed. The source
ancestry guard refused the pending Audit candidate before building or
connecting to the host. The recovered source archive SHA-256 is
`a4a64ea37be3fe72bea5489637e6e83ab629e9c40b79f094695b43944450d434`.
The guard transcript SHA-256 is
`1a2931ac119004277e166bd8ae4d78f93d67d0a1dc697fa07355622b16fa5515`.
The combined timer success, restored dump and retention failure/retry
matrix above still requires its complete Linux acceptance transcript.


### Accepted schema continuation

The accepted Audit migration is now on main through `106ca52d`; the SQL
commit is `8c04ab6f`. Build from the selected current checkout and verify
that accepted migration's exact up/down bytes and ancestry. Observe the
actual migrated schema and table count, and run Control's schema check
before scheduling. Do not assume later accepted migrations are absent.

Review of the recovered runner found an unconnected recorder launch and a
wrong history assertion using `complete` instead of `succeeded`. Repair
the fixture before executing it: start the repository recorder in the
PostgreSQL namespace before database initialization, make it both default
routes, prove the three exact host probes, and require the capture to stay
unchanged through migrations and all four timer attempts. All four
publications are verified, including the deliberate retention failure;
only successful expiry has a count. This repair does not establish a run
result. Keep the existing earlier failure evidence and source recovery
separate from the resulting timer-to-restore acceptance.


### Verified timer publication and retention recovery

On 2026-11-12, source `bc6e4b0274d494109c0b726ec545bf90ac2409c5`
passed the combined drill on Ubuntu 24.04 Linux amd64 and systemd 255.
It contains the accepted Audit SQL commit
`8c04ab6fa6d1d8736e9ddc14376e92c1a70f08a2`, admitted through main
`106ca52d`. The native Control migration and schema check reported
schema 58 and 118 tables; those observed values matched every manifest
and independently restored PostgreSQL database.

The four real systemd timer activations produced `succeeded`, `succeeded`,
`failed`, `succeeded`. Their expiry counts were 0, 1, omitted, 2. Every
publication had `backup_verified=true`. The deliberate owner symlink
caused `INSTALL_BACKUP_RETENTION_FAILED` and service exit status 1 after
publishing a readable new dump. Removing only that fixture marker let the
next actual timer invocation succeed and expire both owned predecessors.
All four history rows remained, including the retention failure.

Each archive had a custom-format `PGDMP` header and exact manifest byte
count and SHA-256. Each was loaded again with native `pg_restore` into a
separate scratch database and compared for deployment UUID, schema and
table count. The original live database OID and ordinary-backup sentinel
remained unchanged. This was a database-aware export, not a database
directory copy.

The PostgreSQL service had no network interface beyond loopback and the
recorder. Both defaults terminated at that recorder before database
initialization. It observed and blocked the exact IPv4, IPv6 and DNS
probes, then the complete capture stayed unchanged through migration,
all four jobs and logical readbacks. The fixture used no published port.
Its PostgreSQL container, runtime units, recorder and scratch directories
were removed; the staging tmpfs returned to zero used bytes. Other lane
containers were untouched.

The retained transcript is `20261112T061113Z-linux.log`, SHA-256
`ad353eff7b863e62f9fe5811188247a9b9dbefa987acd7ba0bdbff29cd46b9ed`.
Its evidence package contains the exact binaries, build-source and accepted
SQL identities, input hashes and runner. The tested timer, host wrapper,
materialization test and probe are now kept with the
[component fixture](../../deploy/offline/internal/install/testdata/scheduled-backup-timer).
The launcher keeps new evidence outside the checkout. Earlier refusal and
source-recovery records remain separate. Console configuration and history
acceptance are not established by this drill; REL-108 remains unticked.
