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
