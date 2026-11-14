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

## Multi-day log recovery

The [VictoriaLogs partition API](https://docs.victoriametrics.com/victorialogs/#backup-and-restore)
takes a snapshot of all attached day partitions when no prefix is supplied.
The [pinned storage implementation](https://raw.githubusercontent.com/VictoriaMetrics/VictoriaLogs/v1.53.0/lib/logstorage/storage.go)
holds references to the selected partitions and snapshots each one in turn.
Preserve each UTC day in the staging and restore trees. The operation is
a set of per-partition snapshots. A concurrent writer or a UTC rollover
can leave no common snapshot instant across stores. Deployment-wide
consistent recovery still requires a write fence and publication by the
reliability owner.

Test two adjacent UTC days with two native VictoriaLogs accounts that
each contain one record per day. Require the snapshot response to account
for both days exactly and reject duplicate or unexpected paths. Copy
both snapshots to staging before releasing the source snapshots, then
restore into a fresh directory and query each account separately. This
catches a current-day-only backup, a missing or duplicated partition, and
cross-account result leakage. The existing licenses and fixture-only
rsync use apply. No new dependency is required.

## Verified path

On Linux amd64 with loopback only, vmbackup copied the live VictoriaMetrics
snapshot locally and vmrestore loaded it into a new directory. The restored
store returned the expected value through the instant query API. A native
VictoriaLogs snapshot without a prefix returned both seeded UTC day
partitions. The fixture staged both before deleting either source snapshot,
restored them into a fresh store, and verified both records in each of two
native accounts with no crossover through LogsQL. The VictoriaLogs 1.53.0
[storage handler](https://github.com/VictoriaMetrics/VictoriaLogs/blob/v1.53.0/app/vlstorage/main.go)
requires POST for snapshot create and delete. All test processes stopped.
