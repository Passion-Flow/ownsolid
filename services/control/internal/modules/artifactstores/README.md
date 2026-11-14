# Scoped object storage and ingestion

This module owns object metadata, grants, upload/download admission and ingestion into dataset/model owners. [platform/objectstore](../../platform/objectstore) is its S3-compatible transport; that adapter receives server-resolved tenant, Workspace and object identity, not independent user authorization.

[service.go](service.go), [authorization.go](authorization.go) and [multipart.go](multipart.go) admit object actions. [download.go](download.go) bounds download access. [ingestion.go](ingestion.go), [worker.go](worker.go) and the recovery/maintenance files bind external bytes to the destination domain and preserve incomplete work. Configuration choices and current license checks remain in [configuration.go](configuration.go) and [license.go](license.go).

Migration 000023 owns object, part, grant and ingestion records. Expected length/digest, part size, revision and expiry are persistent state, not hints supplied to an unchecked transport. Private staging, source snapshots, external upload uncertainty and destination publication need their own reconciliation; successful S3 transfer alone does not publish a model or dataset. Whole readback closes its GET body and releases the transfer reservation before the final HEAD/private-access observations, including on a single-transfer profile. A body-close failure returns no verified receipt. The worker's lifecycle is joined by the control composition root.

The repository selects explicit PostgreSQL, MySQL or Dameng statements. Native Dameng uses the existing transaction owner, quoted identifiers, text JSON bindings and UTC microsecond timestamps; it does not rewrite another dialect. [dameng.go](dameng.go) owns positional bindings and [dameng_restore.go](dameng_restore.go) fences restored operations. The [native integration contract](dameng-integration.md) lists the exact schema and shared activation prerequisites. This source path is not yet an activated or verified Dameng deployment.

Adjust deployment settings through control ObjectStorage configuration and [object-storage examples](../../../../../config/examples/object-storage). Synchronize [object-storage contracts](../../../../../contracts/object-storage), datasets/models, [frontend](../../../../../web/README.md), [quality harnesses](../../../../../tools/quality/object-storage) and [M22](../../../../../docs/milestones/m22-object-storage). Storage paths must remain disjoint from other artifact owners.

Focused unit check: `go -C services/control test ./internal/modules/artifactstores`. Actual multipart, isolation, license, ingestion and recovery checks require the declared selected database and S3 fixture; unit transport doubles do not qualify a production provider.

A privileged coordinator can use [the object backup fence](backup_fence.go)
to pause this instance, wait for admitted calls, and verify a bounded
PostgreSQL inventory against the S3 data plane. With external writers
excluded, pass CopyInventory an exclusively opened, empty regular file
with mode 0600 in the coordinator's private staging directory, and
supply the deadline, inventory row cap and content byte budget. Each
ready object is written under objects/<workspace UUID>/<object UUID>.
The inventory.json entry contains the SQL inventory and tombstones.
The returned length and SHA-256 cover the entire PAX archive after sync
and readback. A failure yields no copy identity. Use a fresh private
file for a retry and dispose of the failed staging file.

The installation coordinator owns durable provider exclusion across
replicas, publication with the database dump, directory sync and recovery.
Release the fence after the coordinated operation finishes so ordinary
reconciliation can proceed. See the
[backup research note](../../../../../docs/research/object-backup-fence.md#verified-copy-under-the-artifactstores-fence)
for the provider and installer boundary and the Linux reproduction.

The [PostgreSQL hold candidate](backup_hold_candidate.go) and
[unnumbered up](testdata/backup-hold.up.sql) and [down](testdata/backup-hold.down.sql) scripts qualify cell scope and coordinator takeover
in the isolated backup fence fixture. Run the existing
`testdata/run-backup-fence.sh` with the verified SeaweedFS binary and
PostgreSQL image ID. The candidate is private to this module. Both tables are cell-owned. Bind the admitted staging UUID before
serving the candidate. Downgrade refuses any used hold generation.
Production activation requires a numbered migration, least-privilege database
composition, durable writer permits and qualified provider exclusion.
See [the restart boundary](../../../../../docs/research/object-backup-fence.md#postgresql-hold-candidate).
