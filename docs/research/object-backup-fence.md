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
inventory returns no inventory. No byte copy or off-box backup is created.

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
