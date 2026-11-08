# Offline telemetry backup and restore (OBS-113)

[vmbackup](https://docs.victoriametrics.com/victoriametrics/vmbackup/)
copies an online VictoriaMetrics snapshot to a local filesystem.
[vmrestore](https://docs.victoriametrics.com/victoriametrics/vmrestore/)
restores into a stopped store. [VictoriaLogs partition snapshots](https://docs.victoriametrics.com/victorialogs/#backup-and-restore)
provide consistent immutable files for copying into a backup directory. Restore
the copy into a stopped logs store under the original UTC day partition.
Use the native snapshot paths. An ordinary live copy of the data directory
can race part merges. Prefer fresh staging and restore paths so partial copies
or failed validation cannot overwrite the running store.

The v1.155.0 metrics binaries and v1.53.0 logs binary are Apache-2.0
([metrics license](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/LICENSE),
[logs license](https://github.com/VictoriaMetrics/VictoriaLogs/blob/v1.53.0/LICENSE)).
Commercial use, redistribution with notices and offline operation are
permitted. [rsync](https://github.com/RsyncProject/rsync/blob/master/COPYING)
is GPL-3.0-or-later and is used as a separately installed test command. No
rsync binary or linked code is added to the product. The fixture uses only
local synthetic telemetry and needs no bucket, cloud metadata or credentials.

The acceptance fixture must create snapshots against the actual running stores,
restore into new directories, start both restored stores and read the sample
and log back through their APIs. Existing installation backup/restore does not
include these stores. A full product flow additionally needs scheduled and
audited ownership, a verified staged publication and failure recovery under the
offline installer and reliability owners.

## Verified path

On Linux amd64 with loopback only, vmbackup copied the live VictoriaMetrics
snapshot locally and vmrestore loaded it into a new directory. The restored
store returned the expected value through the instant query API. A native
VictoriaLogs day-partition snapshot was copied locally, deleted by the API
and restored into a fresh store. The restored store returned the expected
record through LogsQL. The VictoriaLogs 1.53.0
[storage handler](https://github.com/VictoriaMetrics/VictoriaLogs/blob/v1.53.0/app/vlstorage/main.go)
requires POST for snapshot create and delete. All test processes stopped.
