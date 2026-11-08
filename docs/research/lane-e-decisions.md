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
