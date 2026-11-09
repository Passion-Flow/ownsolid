# Artifact object backup fence (REL-103 and NET-101)

The [coordinated local backup](coordinated-local-backup.md) refuses external
objects before stopping its local writer. Artifactstores owns SQL upload
intents, direct part upload, reconciliation, deletion, expiry, staging
cleanup and ingestion handoff. The worker supervisor waits between
iterations. The provider has independently authenticated writers.

The [PostgreSQL isolation contract](https://www.postgresql.org/docs/18/transaction-iso.html)
gives each Read Committed statement a committed view. Use a local admission
barrier across the artifactstores instance. Reject new operations, let
admitted ones settle and pause worker iterations. Compare the SQL inventory
before and after checking the provider. Reject live uploads and pending
effects instead of aborting uploads or replaying unknown effects.

The admitted S3 profile refuses bucket versioning and object lock.
[S3 conditional reads](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html)
bind a request to an ETag, and [multipart ETags](https://docs.aws.amazon.com/AmazonS3/latest/userguide/mpuoverview.html)
need not be full content digests. Bind each ready object to its scoped SQL
revision, opaque ETag, length and full SHA-256. Confirm terminal tombstones
are absent and no upload remains for the known keys. Include database OID
and schema, installation tenant, staging namespace and backend identity
in the inventory digest. The installer still owns the full dump fingerprint
and database cluster identity.

Acquire and verify both require a caller deadline. Acquisition cancellation
reopens admission. Release is idempotent for
the exact acquisition and a stale token cannot release a successor. Service
shutdown cancels verification before closing the transport. The durable
installer record must hold the writer stopped across restarts. A SQL snapshot
or local flag cannot fence another replica, direct bucket writers, lifecycle
expiry or provider administration. The provider owner must supply and
qualify that exclusion. A future version-aware adapter needs HEAD and GET
version IDs and protection against version deletion. The current profile
needs a verified byte copy under both fences before backup publication.

No product dependency, model or dataset is added. [SeaweedFS 4.53](https://github.com/seaweedfs/seaweedfs/blob/4.53/LICENSE)
and the existing [AWS SDK](https://github.com/aws/aws-sdk-go-v2/blob/v1.47.3/LICENSE.txt)
use Apache-2.0. [Go](https://go.dev/LICENSE) uses BSD-3-Clause and
[PostgreSQL](https://www.postgresql.org/about/licence/) uses the PostgreSQL
License. These permit commercial use, redistribution subject to notices
and offline operation.

## Coordinator and provider boundaries

- [Control composition](../../services/control/cmd/server/artifactstores.go)
  owns the only service instance. An installer-facing authenticated local
  operation must hold the exact fence in that instance, audit acquire and
  release, and coordinate every replica and other artifact owner. The
  fence blocks reads that can write audit or stage state as well as
  ordinary mutations. Pausing new calls leaves existing unsettled work
  recoverable through the current APIs after release.
- [S3 admission and transport](../../services/control/internal/platform/objectstore/s3.go)
  own the configured bucket, exact-key multipart listing and conditional
  read. Inventory verification covers known SQL keys. It cannot discover
  uploads or objects outside that inventory, freeze direct S3 or
  filer/gRPC writers, or pin a read to a historical version. Before a
  backup can be published, the provider owner must prevent those writers
  and expiry, then retain a separate immutable copy and verify its bytes
  under that exclusion. A versioned profile needs VersionID in the
  transport and a policy that excludes version deletion.
- [Coordinated backup](../../deploy/offline/internal/install/coordinated_backup.go)
  currently refuses the external profile in `checkCoordinatedProfile`
  or `checkCoordinatedData`. Any future admission must bind the verified
  inventory digest and verified copy to the SQL dump and cluster identity
  in the durable operation. Recovery needs to preserve exclusion through
  restart or interruption and release it idempotently only for that
  operation. Restore must verify the staged copy against the manifest
  before reopening writers.

The inventory verifies existing objects sequentially. It performs one or
more multipart-list requests and up to four read or privacy checks per
ready object and reads the sum of ready object bytes. Terminal tombstones
require multipart and absence checks. The row cap includes tombstones.
A deadline, scope mismatch, unresolved effect, wrong bytes or changed SQL
inventory returns no inventory. VerifyInventory discards the bytes.
CopyInventory can stage a verified archive, while publication remains
with the installer.

## Validation

The [isolated runner](../../services/control/internal/modules/artifactstores/testdata/run-backup-fence.sh)
exercises real PostgreSQL 18.6 with pgvector 0.8.7 and SeaweedFS 4.53
with one transfer slot. It requires a locally available PostgreSQL image
pinned by image ID and a locally installed SeaweedFS binary. pgvector
uses the [PostgreSQL License](https://github.com/pgvector/pgvector/blob/v0.8.7/LICENSE).
The fixture starts both in a network namespace with loopback only. The
S3 requests are signed and pass through a TLS frontend. The fixture
covers the data plane and does not qualify provider management mTLS or
the installer. REL-103 and NET-101 stay open.

On Linux amd64, `TestActualObjectBackupFence` passed with those versions
in the loopback-only namespace. It held a real part during acquisition,
canceled one attempt, retried after the part settled, and rejected
unsettled multipart and ingestion state. It checked a ready object's
bytes and an aborted object's absence, refused a foreign tenant before
provider I/O, rejected an SQL revision change during readback, and
verified that a stopped instance canceled its read and a stale release
left the successor fence held.

## Verified copy under the artifactstores fence

The existing verification reads every object but discards the body. Use the
same conditional S3 read and privacy checks to write a portable
[Go archive/tar](https://pkg.go.dev/archive/tar) stream into a new private
regular file opened exclusively by the trusted backup coordinator. Use PAX
for objects larger than the legacy tar size field. Store each ready object's
bytes under its canonical workspace and object UUIDs. Record the verified
inventory, including tombstones, as inventory.json. A tombstone has no byte
entry. Keep the existing read-only inventory operation for callers that
need verification without a copy.

Use bounded sequential streaming and the same selected SQL inventory before
and after copying. Preflight the caller's object and content-byte limits
before provider I/O. Each ready object's body must match its recorded length
and full SHA-256, with an unchanged ETag and private access. Close the
provider body before subsequent provider checks on the one-slot S3 profile.
[Go File.Sync](https://pkg.go.dev/os#File.Sync) and the
[Linux fsync contract](https://man7.org/linux/man-pages/man2/fsync.2.html)
describe the file writeback boundary. After closing the tar stream, sync and
hash a complete readback of the file descriptor and compare the length and
SHA-256 with the bytes produced. Return the inventory and exact copy identity
only after both readback and the final SQL comparison succeed.

Keep the file private and staged on any failure. The coordinator owns
exclusive creation, disposal, directory sync, publication, separate backup
storage, and the durable record binding the copy to the SQL dump and
provider exclusion. Returning the copy identity alone cannot fence direct
provider writers or authorize restore. An S3-side copy would still require
provider-specific admission and verified readback. Copying the running
provider's data directories would bind the backup to its filesystem layout
and recovery internals. Neither is required to produce this portable copy.

No new product dependency, model or data set is needed. The Go archive and
file APIs use the existing [BSD-3-Clause Go license](https://go.dev/LICENSE),
which permits commercial use, offline operation and redistribution with
notices. The provider and fixture licenses are recorded above.

Extend the real SeaweedFS and PostgreSQL loopback fixture. It must extract
the ready object from a copied archive, check its SHA-256 and bytes against
SQL, confirm that the tombstone has no content entry, and validate the full
archive identity. Cover budget refusal before provider I/O, nonempty or
nonprivate destinations, a changed SQL revision, retry with a fresh file,
and provider-side abort or shutdown with no publishable copy result.

### Verified staged archive

On Linux amd64, TestActualObjectBackupFence passed with SeaweedFS 4.53,
PostgreSQL 18.6 and pgvector 0.8.7. The isolated namespace used only
loopback. The signed S3 read used the existing single-transfer TLS fixture.
The test independently parsed the PAX archive, compared the ready object's
exact bytes and SHA-256 to SQL, verified the complete archive digest and
checked that the aborted object's tombstone appeared only in the inventory.
The byte-budget refusal performed no S3 request and wrote zero bytes.
Nonprivate and nonempty destinations were refused. A changed SQL revision
invalidated an in-progress copy, a fresh-file retry bound the new revision,
and service shutdown canceled a held S3 read without returning a copy
identity. The fixture stopped SeaweedFS and PostgreSQL.

Run the [isolated runner](../../services/control/internal/modules/artifactstores/testdata/run-backup-fence.sh)
with the verified local SeaweedFS executable and the preloaded PostgreSQL
image ID. The installer and provider owners still own exclusion,
separate backup storage, publication with the SQL dump, and restore.
## Installer checkpoint boundary

Use the existing PostgreSQL custom-format dump, table fingerprints and
protected-file inventory inside a distinct object checkpoint manifest.
[PostgreSQL custom archives](https://www.postgresql.org/docs/18/app-pgdump.html)
carry a logical database export. Their contents cannot establish which
external object bytes or source cluster they belong to. Bind the verified
dump SHA-256, source cluster and database OID, site, operation, tenant,
store and backend to the Control inventory digest. Match a trusted copy
confirmation for the same scope and inventory. The coordinator supplies
an identifier for the durable fence and the independently verified dump
digest. These values are not recovered from the package itself.

Read the exact private PAX copy produced by `BackupFence.CopyInventory`
in `e03bbbab`. Bind the producer-confirmed archive size and SHA-256
as well as the copy identifier. Require the canonical workspace and
object paths in SQL order, followed by the sole `inventory.json`. The
version 1 inventory digest uses Go JSON with `inventory_sha256` empty.
Compare it with the trusted operation and the verified copy. Stream
and verify every size and checksum. Refuse missing or unlisted objects,
tombstone content, duplicate entries, unsafe headers and trailing bytes.
Keep the tenant, workspace, object ID, SQL revision and ETag in the
inventory. The digest is a byte identity. An
[S3 ETag](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html)
cannot substitute for the full SHA-256.

Publish the object checkpoint manifest last with exclusive atomic
placement and directory sync. The input directory must be private and
already contain the durable dump and copy. Never place a version 1
manifest in the object staging directory. The standard local and age
paths refuse the object format. The independent restore check requires
the exact sealed manifest SHA-256 and operation binding from a trusted
durable record, then rechecks the package files. After an interruption,
an absent, changed or unconfirmed manifest leaves recovery held. An
exact retry may finish the same seal after rechecking all input. It
cannot replace a different manifest. Directory sync follows the
[Linux fsync contract](https://man7.org/linux/man-pages/man2/fsync.2.html).
Do not add a production backup or restore route for this checkpoint
while the independently trusted provider copy and durable fence are
unavailable.

The control owner supplies the authenticated scoped inventory and the
verified portable copy under admission and worker exclusion. The provider
owner supplies independently enforced writer and expiry exclusion. The
coordinator retains the verified copy in durable immutable storage and
binds the producer's exact scope and archive readback to the operation. The installer coordinator must persist that
authority and the fence before capture, then confirm the round-tripped
dump under the same operation. Restore additionally needs durable
target exclusion, target-scope authorization and readback of every
restored object before writer admission. The restored provider must
preserve the SQL-bound ETag or have a separately authorized and
qualified reconciliation of that identity. Tombstones must remain absent. Only that coordinator can reconcile or
release a fence. Neither successful seal nor local readback grants
permission to start a service.

Reuse the existing Go, PostgreSQL and object protocol components and
licenses described above. No dependency, model or dataset is added.
Test missing files, changed dumps and object bytes, unconfirmed copies,
wrong operation or scope, path substitutions, repeated sealing and
interrupted publication. On disconnected Linux amd64, create a real
`pg_dump -Fc` archive and load it into a disposable PostgreSQL database
before binding its bytes. The lightweight run does not restore a
deployment or qualify the provider.

### Linux amd64 checkpoint validation

On `ownsolid-cpu`, one cached PostgreSQL 18.6 container used network none,
Unix sockets and a private tmpfs database. The fixture created one scoped
artifact row, exported it with `pg_dump -Fc` and loaded it with
`pg_restore --exit-on-error` into a scratch database. The original and
scratch table fingerprints and object SHA-256 matched. The logical dump
SHA-256 was `648b35595d80f12ffef8e3029d78c6d58c823464ae591151bc842356a5b32d12`.

The native installer test bound those dump bytes to the actual source
cluster and database OID, the fixture's exact PAX object copy, and a
separate operation record. The fixture independently wrote the archive
using Control's inventory types and PAX header writer at `e03bbbab`.
The archive SHA-256 was
`004aa1c438c5164d56311f2579b0fb3bdae3b8054a09615697c224cff302ac46`. A subprocess exited after the checkpoint was durable
and before its digest was recorded. Recovery held until the trusted
operation retried the exact seal. Legacy recovery preserved an unknown
fence record and issued no Docker command. Missing keys and objects,
foreign scope and altered input were refused. Only the IPv4, IPv6 and DNS
probes reached the recorder. No deployment or provider was restored, and
the copy confirmation and fence in this run were controlled fixture inputs.

## Restart authority required for production

The selected PostgreSQL owner holds an installation advisory lock for
serving. [PostgreSQL releases that lock when the session ends](https://www.postgresql.org/docs/18/explicit-locking.html#ADVISORY-LOCKS).
A new process can acquire a new installation lock while a prior object
checkpoint is still incomplete. The current artifactstores constructor
checks tenant, backend, schema and provider profile. It loads no backup
hold. The local fence is tied to one lifecycle pointer. Closing that
lifecycle cancels copy I/O and invalidates the pointer. Its scoped
inventory and SHA-256 are byte evidence for a staging file.

The object tables retain per-object operations and their authorization.
ProcessNext selects prepared work, rechecks its actor and commits sent
before provider I/O. Reconcile may claim an expired or failed intent.
CleanupStaging can remove local content and Expire can schedule abort
or deletion. None reads a durable backup operation. Restored-state
fencing revokes grants and marks interrupted operations for reconciliation.
It supplies no permission to reopen writers. The installer's a43f2a45
checkpoint requires a trusted fence digest and copy confirmation. It
has no production coordinator issuing either.

Use the following contract for the selected PostgreSQL owner and installer.
It reuses the previously documented PostgreSQL, Go and S3 components and
licenses. A new dependency is unnecessary.

- Add an installation-scoped hold row with a unique key over the
  source cluster system identifier, database OID, tenant UUID and
  store UUID. Pin the site SHA-256 and backend SHA-256 in that row.
  Preserve a monotonic positive generation across release and reuse.
  The row needs an operation UUID, coordinator owner UUID, bounded
  lease deadline, hold state, and nullable verified copy identity
  fields for inventory SHA-256, copy UUID, archive SHA-256 and size.
  Require all copy fields together and bind confirmation to operation
  and generation. Keep the independent fence digest distinct from
  any secret or release token.
- Serialize hold transitions and writer admission on the same row.
  Persist an admission for each operation that can outlive its
  initial SQL transaction, including direct multipart transfer,
  cleanup, reconciliation and the worker. Bind permits to scope,
  generation, operation UUID, owner and admitted/sent state. Reuse
  an existing durable intent only when it can prove the same
  boundaries. Commit the drain state before excluding new admissions.
  Require provider exclusion and settle all admits before copy.
  A lost acknowledgement or an expired sent operation stays unresolved
  until the current owner reconciles provider and SQL state.
- Read and validate the current scope and generation before installing
  the artifactstores service or starting its worker. During an active
  or unresolved hold, stop all mutation and cleanup across replicas.
  Unavailable, missing or inconsistent authority in this profile
  blocks startup and recovery release. A missing database row after
  restore cannot override a still-held coordinator operation. The
  installer must compare its independently durable operation before
  enabling the restored target.
- Lease expiry permits a privileged takeover of the same hold with
  a higher generation. Expiry leaves writers fenced. Copy verification
  and release recheck scope, operation, generation and current
  coordinator authority in the owning transaction. A stale confirmation
  or release returns no success and changes no successor state.
  Reconcile restart ambiguity before allowing a new confirmation.
- The provider owner must exclude direct S3 and filer writers, lifecycle
  expiry and administrative writes throughout capture. The installer
  pins that exclusion independently and binds a durable archive readback
  to the logical dump and checkpoint. For restore it must also verify
  the target and every restored object's content and ETag before
  releasing the target hold.

Use the existing SeaweedFS and PostgreSQL runner to first confirm that
a process-local handle cannot return a copy after shutdown or release a
successor in the same service. Qualification of the new authority needs
two service processes, one shared database and a fenced provider.
Kill the first process before copy, after a sent provider request and
after writing a verified file. Require the next process to refuse
admissions and cleanup until the current coordinator resolves the hold.
Exercise lease takeover, stale confirmation, stale release, unknown
commit, an expired prepared intent and an expired sent intent. Check
provider requests and SQL revisions to detect effects from a denied call.

The shared schema and startup owners must supply that authority before
mounting production backup, cleanup or restore release for a checkpoint
hold. The existing private stage-copy path remains available for its
process-local fixture. NET-101 and REL-103 remain open.

## PostgreSQL hold candidate

Use an unnumbered schema candidate under the artifactstores test fixture.
Provision one released row explicitly for the selected database, tenant,
site and store. Opening the candidate will neither insert a missing row
nor infer release from its absence. Observe the current database OID,
cluster system identifier and installed tenant on each transition. Use
the existing PostgreSQL transaction owner and take the scope row with
`SELECT FOR UPDATE` before evaluating the lease. The
[PostgreSQL row lock](https://www.postgresql.org/docs/18/explicit-locking.html#LOCKING-ROWS)
serializes decisions, and
[clock_timestamp](https://www.postgresql.org/docs/18/functions-datetime.html)
samples server time after the lock wait. Enable transaction-local
[synchronous_commit](https://www.postgresql.org/docs/18/runtime-config-wal.html#GUC-SYNCHRONOUS-COMMIT)
and refuse a server configured with fsync off. The
[control identity functions](https://www.postgresql.org/docs/18/functions-info.html#FUNCTIONS-CONTROLDATA)
supply the cluster pin. ARC owns least-privilege grants and validation
of the selected SQL owner. The fixture uses its disposable operator.

A hold increments the generation and expires only for takeover. Takeover
keeps the operation UUID, replaces the owner and clears any copy identity
from the prior generation. A current owner can confirm only the result
of a new exact local archive readback scoped to that hold. Confirmation
never reopens the write gate. Release requires the current unexpired
token and the exact persisted copy identity. An ambiguous commit yields
no token, receipt or permission. The coordinator can inspect current
state or retry the same identity. A release retry is accepted only
while the row still identifies that exact released hold and copy. Reject
SQL table-lock-only and process-memory designs because neither persists
ownership after the coordinator process exits.

The test gate represents a startup/cleanup decision with the local writer
already independently stopped. It supplies no durable writer permits.
The candidate is unmounted. Prove the closed gate across a coordinator
process kill, expiry, takeover, stale confirmation and stale release,
then prove exact archive confirmation and explicit release on the
isolated PostgreSQL/SeaweedFS fixture. Test that a new generation clears
an earlier verified copy and that a missing or mismatched scope remains
closed. [S3 access controls](https://docs.aws.amazon.com/AmazonS3/latest/userguide/access-management.html)
describe the object data plane. They cannot prove exclusion for SeaweedFS
filer, lifecycle and administrative routes. Provider write exclusion,
durable business admission and the installer's independent restore
authority remain production prerequisites. No dependency changes.

### Verified PostgreSQL coordinator takeover

On Linux amd64, `TestActualObjectBackupFence/durable_hold` applied
the candidate DDL to its disposable PostgreSQL 18.6 schema with
pgvector 0.8.7. SeaweedFS 4.53 held the already verified multipart
object. The network namespace had only loopback.

A child coordinator committed the hold and was killed. A fresh SQL
pool saw the gate closed before and after forced lease expiry.
Two successor claims yielded one owner. The old owner could neither
start a new copy nor release the hold. Takeover cleared an earlier
confirmed copy, and a blocked S3 readback that finished after another
takeover could not record its result. The real local staging cleanup
returned busy and did not reach S3. The retained file could be removed
after the current owner confirmed a new archive and explicitly
released with that exact identity. The fixture also refused missing
DDL, an unprovisioned scope and mismatched site and tenant pins.

The data-plane service was separately paused by its existing local
fence throughout the child-process test. This does not qualify a
control-plane restart with external provider requests still pending,
or provider write exclusion. ARC owns migration numbering, SQL grants
and durable write admission at startup. The provider and installer
owners still own the exclusions and independent target release checks
described above. The PostgreSQL and SeaweedFS fixtures stopped.
