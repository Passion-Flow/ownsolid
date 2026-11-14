# Coordinated local backup (REL-103)

The delivered single-node Compose topology runs PostgreSQL, control and two
telemetry stores. Control serves model uploads and performs background model
publication into its local read-write bind mount. Its configuration bind mount
is read-only. The installer and the privileged operator are separate writers of
configuration, key files and PostgreSQL. Other installer operations share
`operation.lock`. PostgreSQL's maintenance login and Docker/root access belong
to the host operator. The two telemetry stores have independent durability
and their files are not part of this logical recovery point.

Object ingress is a separate opt-in [SeaweedFS S3 profile](s3-artifact-contract.md).
Control handles multipart creation, parts, completion, deletion, abort and
reconciliation. Its ingress staging and its model and dataset publication roots
are distinct. The provider also has independently authenticated S3 clients,
lifecycle actions and management endpoints. A stopped control process does not
fence those writers. Reject the object configuration and existing object
records, including when the object configuration has since been removed.
Uncovered artifact configuration and existing dataset, training or preparation
records also fail admission. Start with the shipped Compose definition,
generated control and Compose settings, local PostgreSQL, local published
models, and installation keys.

## Approach

[PostgreSQL logical dumps](https://www.postgresql.org/docs/18/backup-dump.html)
are database-consistent, and concurrent writers can still commit. GitLab also
[separates external object and key custody](https://docs.gitlab.com/administration/backup_restore/backup_gitlab/).
Use an explicit maintenance backup for the supported local profile. Keep the
existing online backup and PITR procedures available for their declared scope.

1. Under the installer lock, persist the database cluster identifier, database
   OID, original connection limit, site and exact control container identity and
   its prior running state. [Docker stop](https://docs.docker.com/reference/cli/docker/compose/stop/)
   allows the writer to shut down. Reuse the same existing container at recovery.
2. Set [the PostgreSQL database connection limit](https://www.postgresql.org/docs/18/sql-alterdatabase.html)
   to zero and [terminate pre-existing application sessions](https://www.postgresql.org/docs/18/functions-admin.html).
   The database still admits the installer maintenance superuser. PostgreSQL
   also exempts background workers, so refuse subscriptions and custom
   preloaded workers. The stock health check is a read-only maintenance client.
   Host root, Docker administrators and PostgreSQL administrators are trusted
   operators and must exclude their own maintenance writes. Compare the live
   table fingerprints with the round-tripped dump before publication.
3. Snapshot the inventory and native change times of directories and regular
   files under the backed-up roots after fencing. Read back the completed
   staged copy and compare inventory, identities, permissions and native change
   times immediately before publishing. Use the existing logical
   `pg_dump`/scratch `pg_restore` format. The gate detects file additions,
   removals and ordinary equal-size rewrites even when mtime is restored.
   This is an application write barrier plus detection, with trusted local
   administrators. It does not provide storage transactions against a
   privileged adversary or an unqualified remote filesystem.
4. Restore the original database limit and original container state using a
   bounded independent context on failure or cancellation. A durable maintenance
   record blocks conflicting installer actions if killed. Explicit recovery can
   restore service without the backup output directory and can be retried.

A database snapshot by itself, pre/post timestamps without excluding control,
a provider listing or copying a live PostgreSQL directory cannot establish
this local boundary. Locking only SQL tables leaves other mutation paths,
including sequence changes and new sessions. No new dependency or model is
introduced. Existing PostgreSQL uses the
[PostgreSQL License](https://www.postgresql.org/about/licence/), Compose and the
[approved model](signed-release-recovery.md) use Apache-2.0, and the existing
[x/sys](https://cs.opensource.google/go/x/sys/+/refs/tags/v0.49.0:LICENSE)
native filesystem bindings use BSD-3-Clause. These allow commercial use,
redistribution and offline operation subject to their terms.

## Acceptance and provider handoff

On isolated Linux amd64, use real PostgreSQL and the existing pinned model.
The [native recovery fixture](../../services/control/internal/testing/recovery/models_test.go)
loads approved files and seeds their exact published catalogue identity in a
disposable PostgreSQL database. The coordinated drill follows that pattern
with the shared model archive verifier and checks the source through Admin.
Inject a normal PostgreSQL connection attempt after the fence, modify a
protected file during backup, terminate the installer during its dump, recover
in a fresh process, retry, and restore the resulting dump and file bytes.
Use the existing object protocol fixture and an object metadata row to prove
that external object state remains an independent prerequisite. The test does
not qualify an object provider.

Before enabling an object profile, its owner needs a bounded, installation-
and bucket-scoped fence or immutable version checkpoint. It must cover API
admission, in-flight multipart and ingestion/cleanup workers, provider
credentials, lifecycle deletion and other clients. The provider must export
or retain exact object versions with digests and verify readback against the
fenced SQL inventory, including pending or abandoned intents. The installer
needs idempotent acquire, verify and release/recover semantics, with a token
bound to the same database and artifact roots. Provider credentials remain
file references under the storage owner's custody. The end-to-end item remains
open until a real provider passes failure, interruption and restore tests.

## Linux amd64 acceptance

`TestCoordinatedBackupIsolatedDocker` passed in 364.78 seconds on
`ownsolid-cpu` running Ubuntu 24.04 amd64, Docker 29.1.3, Compose 2.40.3
and PostgreSQL 18.6 at schema 57. The stock local profile was installed in
a private Docker daemon in an isolated mount, network and PID namespace.
Both default routes went to the egress recorder. The capture contained
exactly the host TCP probe to `192.0.2.1:9` and the control TCP probe to
`192.0.2.2:9`. The HTTPS listener on 8443 was inside that namespace.

An object-enabled site configuration and a valid retained `creating`
object record each refused before stopping control. PostgreSQL retained
the seeded record. A file rewritten during backup was rejected even
after the fixture restored the original bytes and mtime. No backup was
published, and both entrances and the original control container and
database limit recovered.

Killing the installer after the fence and before `pg_dump` kept the
writer stopped. PostgreSQL rejected a direct login as the application
role and the installer refused a conflicting backup. A fresh
`recover-backup` process restored the exact original container and
connection limit of 23. A second recovery reported no pending work.

The successful retry compared the live database to the verified logical
dump and published 118 tables, 19 installation files and nine model files.
A separate empty site restored the original tenant and administrator.
Both HTTPS origins accepted the recovered administrator. The restored
HMAC key, mail keyring, PostgreSQL password file and administrator
credential matched the backup manifest. Admin returned the same ready
Qwen3-0.6B revision `c1899de289a04d12100db370d81485cdf75e47ca`.
The nine model files matched the pinned hashes before and after
recovery (1,519,207,673 bytes).

The existing S3 conditional-read and multipart SDK fixtures passed
separately in Linux network namespaces with loopback only. They cover
protocol-level readback, changed ETag and truncated responses, exact
multipart scope and refusal of embedded completion errors. They do
not qualify a SeaweedFS provider or coordinate one with PostgreSQL.
