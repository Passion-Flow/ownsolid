# Lane E decisions

## AUD-102 integrity coverage

PostgreSQL starts the audit chain at migration activation. Existing rows remain
readable and are outside the verified interval. Rehashing them during upgrade
would lock and rewrite retained partitions without proving their prior integrity.
If old records require attestation, the consequence is an explicit coverage gap
before activation.

The PostgreSQL operator runs a read-only SQL export across partitions. The
existing console audit list keeps its filters and response contract. A filtered
audit list can skip events and cannot serve as evidence of continuity. The
operator export is privileged because the installation-wide chain spans tenant
scopes. An application export with approval belongs to AUD-107.

The sequence follows the lock on the private tail row. Ordering by occurrence
time could produce forks when transactions commit out of time order. Backdated
events can interleave two monthly partitions, so their archives must be read
together in sequence order to verify that span.

Keep the backup content comparison inclusive of the chain head. When comparing
before and after the restore receipt, substitute its verified predecessor state
for the head. Ignoring the private head entirely would allow restoration of a
database that cannot reliably append the next event.

The offline verifier has no third-party or network dependency. Integrate it into
the Go workspace without changing the control-plane HTTP contract. Independent
audit custody of a checkpoint remains necessary to detect a database owner's
full rewrite.

The migration takes the next version from the published lineage. ARC-103
landed first, so the audit migration follows it at 000053. The prior migration
files and checksum entries are preserved. Fresh and dump-restored catalog
fingerprints were recalculated from PostgreSQL 18.6 after rebasing.

The coordinator owns the roadmap progress index. Ticking the module line alone
makes `make check` fail its progress-table check. Keep AUD-102 open until the
module line, the index and the product status can be updated together.

## Next eligible P1 item

- AUD-101 requires first-class service and product-agent actors. The current
  audit list projects user actors from identity, and the control plane has no
  product-agent mutation owner to exercise. Leave the item open until there is a
  real product-agent write path and coordinated ownership of the audit list.
- AUD-103 requires one trace carried through application, gateway, model
  selection and execution on a GPU. The trace and export need changes at those
  owners' boundaries. A database-only correlation cannot prove the full path.
- AUD-104 depends on the retention and telemetry owners. PostgreSQL's audit
  retention setting defaults to 180 days but permits shorter periods. The
  network-log minimum must be enforced by the relevant log-store and operator
  configuration paths, which are outside lane E.
- Proceed with AUD-105. PostgreSQL can enforce audit immutability for the
  serving principal without changing the public audit API or retention
  behavior. The guard can be added to the parent audit table in a new migration.

## AUD-102 cell-scope correction

The live PostgreSQL catalog showed that migration 000030 adds site and cell
identifiers to audit events. V1 did not hash those columns. Keep the published
migration and existing V1 records intact, and append V2 records with both
identifiers under a new hash domain. Rehashing old records would invalidate
independent checkpoints. Treat old exports as V1 coverage and clearly record
their narrower field scope.
