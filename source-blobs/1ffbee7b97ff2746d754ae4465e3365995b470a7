# Schedule verified backups

On a Linux amd64 Compose installation, select a new empty directory owned by
root with mode 0700. Put it outside the installation root on the site's
backup filesystem. Add the policy below to the installation configuration.

```yaml
backup_schedule:
  directory: /private/ownsolid-backups
  retain: 7
```

Run a first backup as root and verify the reported path.

```sh
ownsolid-install scheduled-backup --config /private/site.yaml
```

Install the two unit files into the host's systemd unit directory. Set the
configuration path in the service's local drop-in with
`systemctl edit ownsolid-backup.service`.

```ini
[Service]
Environment=OWNSOLID_INSTALL_CONFIG=/private/site.yaml
```

The service finds `ownsolid-install` on the systemd executable search path.
The timer defaults to daily. To change the calendar, use
`systemctl edit ownsolid-backup.timer` with a site-selected `OnCalendar`.
Reset the inherited calendar with an empty `OnCalendar=` line first. Enable
the timer with `systemctl enable --now ownsolid-backup.timer` and inspect the
last run with `journalctl -u ownsolid-backup.service`.

The job uses PostgreSQL's native dump and verifies it with a scratch restore.
It retires older backups only after publishing a new verified one. The
ownership records keep scheduled expiry scoped to this installation. Ordinary
and incomplete backups stay untouched. The regular `restore` command reads
the scheduled backup directory printed by the job.
## Read scheduled backup history

Run the local query as root with the installation configuration.

```sh
ownsolid-install backup-history --config /private/site.yaml
```

The query returns JSON with `format_version`, the last verified
`deployment_id` when known, and up to 256 `runs` ordered by sequence. It
works while Docker or PostgreSQL is stopped and creates no files. With no
recorded attempts it returns an empty array. The private
`scheduled-backups.json` stays in the installation root across process
restarts and archive expiry.

Each run has a sequence, a UTC start time and an outcome. A finished run
also has a UTC finish time. An interrupted run has no inferred finish time.
Failures and interruptions carry a fixed error code. A concurrent
invocation rejected by the installation lock has no separate run record.

`backup_verified: true` means the job confirmed publication of a verified
backup. The archive may since have expired. A false value leaves
publication unconfirmed. `expired_backups` reports the number removed when
retention finished successfully. If expiry fails, the run reports that
failure and still identifies the confirmed publication.

The output excludes private paths and diagnostic text. A later consumer
must match the last verified deployment ID to its own authenticated
installation context before exposing the record. Console access and alerts
have separate ownership. The local query covers scheduled attempts.

## Run the read-only Agent

The signed Agent inventory carries [ownsolid-agent.service](ownsolid-agent.service).
Use the [offline host installer](../offline/README.md#install-a-read-only-agent-from-signed-media)
to provision the account, verify and place the unit, enroll the Agent, and enable
the service. To inspect service state and logs locally, run:

```sh
systemctl status ownsolid-agent.service
journalctl -u ownsolid-agent.service
```
