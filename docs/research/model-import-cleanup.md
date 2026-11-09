# Reclaim published model import staging

MOD-120 follows the [S3 multipart lifecycle](https://docs.aws.amazon.com/us_en/AmazonS3/latest/userguide/mpuoverview.html):
a completed object and the temporary parts that created it have separate lifetimes.
ownsolid's temporary parts live on disk while the publication is recorded in
PostgreSQL. [PostgreSQL transaction visibility](https://www.postgresql.org/docs/current/tutorial-transactions.html)
lets a fresh worker distinguish a committed publication from one whose commit
outcome was unknown. [Directory fsync](https://man7.org/linux/man-pages/man2/fsync.2.html)
is needed to make the removal durable before acknowledging it in SQL.

Use the existing import worker, per-import file and SQL session locks, durable
publication check, and bounded staging cleanup used by cancellation. A ready
import with chunk records is eligible. The worker checks the committed version,
scope and audit event, verifies the immutable model directory, then removes the
upload and failed stages. It syncs the imports directory before deleting the
temporary chunk records. The import's history, byte counts and publication audit
remain available. A retired model's files are retained and can still be verified
before reclaiming its staging.

An interrupted filesystem cleanup can resume from the remaining owned files.
An interruption after the files disappear still leaves the SQL work marker.
If a path contains foreign data, or the published copy fails verification,
retain the upload and log a stable error with the import ID. Move the scan cursor
past a blocked import so that other work still progresses, and retry it on the
next pass. A worker that loses either lock cannot claim concurrent cleanup.

Rejected approaches:

- Delete staging before the SQL publication commits. A failed or unknown commit
  could leave the only recoverable source gone.
- Keep a separate cleanup table. Existing chunk records describe the pending
  work and already exist on installations upgraded from earlier releases.
- Sweep arbitrary directories. Only an import whose metadata and immutable
  publication agree is eligible. Unexpected files require operator inspection.

The implementation uses existing ownsolid code and the
[Go standard library](https://go.dev/LICENSE) (BSD-3-Clause). The existing
[UUID package](https://github.com/google/uuid/blob/master/LICENSE) is BSD-3-Clause.
The PostgreSQL backend uses the [PostgreSQL License](https://www.postgresql.org/about/licence/)
and existing [pgx](https://github.com/jackc/pgx/blob/master/LICENSE) is MIT. These
permit commercial use, redistribution under their notice terms and offline use.
No new runtime dependency or dataset is required. The real-model test uses the
existing [Qwen3-0.6B fixture](https://huggingface.co/Qwen/Qwen3-0.6B/blob/main/LICENSE)
under Apache-2.0, which permits commercial and offline use and redistribution
subject to its license and notice terms. The model is test media and is kept
outside Git.

Acceptance: publish the approved model through its existing resumable import.
Confirm cleanup only after ready, the model still verifies byte for byte, and
history and file transfer stay intact. Exercise a restarted worker, missing or
damaged publication, unexpected staging files, concurrent ownership and a retry
where the files are gone but the SQL cleanup has not committed. Run against real
PostgreSQL with the model already on local media. Disk sizing still needs room
for both copies until publication. The [console flow](model-library.md) reports
publication separately from background cleanup.

The real-model integration test separates the cleanup assertions from backup,
restore and retirement. This lets a shared Linux host run only the acceptance
path with one worker, an isolated PostgreSQL instance and bounded temporary
storage. The acceptance path verifies the model bytes again after the temporary
files and chunk records have been removed. Stage logs identify whether a
timeout occurred during upload, publication, cleanup or the final readback.

## Linux amd64 acceptance

On 2026-11-09, the real-model test passed in 43.17 seconds on `ownsolid-cpu`
with PostgreSQL 18.6, pgvector 0.8.7 and Docker `--network none`. The
container had one CPU and a 4 GiB memory limit. PostgreSQL data and test staging
were on bounded tmpfs mounts. The 1,519,216,640-byte USTAR archive used the
approved Qwen3-0.6B revision already on the host. The test uploaded every chunk,
forced a publication audit rollback, confirmed the upload remained, then
published the same verified revision. An injected SQL cleanup failure left
chunk records after the upload directory and failed stage disappeared. A fresh
worker removed the records, preserved the ready import and byte count, and
verified the published files again.

With the original archive bind-mounted from the shared disk, the same test
timed out at 150 seconds in `io.ReadFull` on the source archive before
publication. Its last logged committed offset was 268,435,456 bytes and a
thread sample showed `folio_wait_bit_common`. Packing the same existing files
into a tmpfs archive removed the cold source-disk read from the verification
run. The archive and test instance were removed afterward.

Cross-compile the `model_integration` tests for Linux amd64. In the isolated
PostgreSQL container, provide `PROJECT_ONE_STATE_ROOT` with the approved
archive and `PROJECT_ONE_TEST_DSN_FILE` pointing to a private loopback test
database DSN. Run the selected test with the directory for `TMPDIR` on bounded
tmpfs.

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go -C services/control test \
  -tags model_integration -c -o "$fixture_dir/models.test" ./internal/modules/models

GOMAXPROCS=1 "$fixture_dir/models.test" \
  -test.run '^TestActualModelImportPublicationAuditRollbackAndFreshWorkerRecovery$' \
  -test.skip '/snapshot-and-retirement$' -test.v -test.timeout 150s
```

The console evidence is commit `bbf09930`, which keeps the published model
accessible after reload and presents disk usage as the import estimate.
Browser tests use mocked responses for publication and retries. The Linux
test above supplies the actual filesystem and PostgreSQL evidence.

With the acceptance assertions in this change on baseline `1bbdca7e`,
`make check`, `make build` and `make test` passed in sequence.
Chromium passed 1,039 console and 387 portal tests.

## MOD-121 abandoned upload retention

The [tus expiration extension](https://tus.io/protocols/resumable-upload#expiration)
makes an expiration visible to a resuming client. [S3 incomplete multipart
lifecycle](https://docs.aws.amazon.com/AmazonS3/latest/userguide/mpu-abort-incomplete-mpu-lifecycle-config.html)
separates unfinished parts from completed objects. Keep ownsolid's existing
protocol and apply the same lifecycle distinction. These are design
references. This work adds no dependency, model or data.

The deployment configuration needs an opt-in inactivity interval in
`config.ModelsPolicy` and its validated runtime policy. Use the last
committed progress timestamp under the import's file and repository session
locks. A replay of an already recorded chunk leaves that timestamp alone.
The model worker should select and recheck one candidate at a time. Active
writers and jobs already queued for verification keep their current paths.
Committed publications stay in the existing post-publication cleanup path.

First record an expiry intent and a system audit transaction. Recover the
exact import staging directory through the existing bounded discard logic.
Confirm that cleanup before releasing the one-active-import constraint.
Use the existing cancelled terminal state with a distinct expiry reason if
its contract permits it. Keep idempotent history readable. The shared
problem registry, Models contract and Console must carry the expiry reason
and show when a fresh import is needed. A missing, conflicting or uncertain
expiry commit must never authorize deletion. Verify lost replies, process
restart, partial cleanup, fresh progress that races the candidate, and a
completed model in the same workspace before enabling the interval.

### Worker candidate

The first executable candidate takes a PostgreSQL policy scoped to one
workspace in the current installation. It accepts an explicit inactivity
interval of one hour through 365 days, in whole seconds. A nil policy
leaves the worker disabled. The lower bound allows ordinary upload
pauses, and the upper bound catches accidental units while allowing a
full year of retention. The age comparison uses PostgreSQL's clock
after the scoped row lock. An incomplete import must still be uploading,
have no model or verification attempt, and have no error. The
expiry marker and system audit commit in one transaction. A worker can
resume from that marker even after normal audit retention removes the
first event. Both events use the import UUID as the operation correlation.

[PostgreSQL row and advisory locks](https://www.postgresql.org/docs/18/explicit-locking.html)
support rechecking the workspace and import after selection, while the
existing file lock excludes a writer across the filesystem and both
transactions. A bulk delete by timestamp would bypass those fences. A
filesystem mtime would also count retries or temporary writes as progress.
Reuse the immutable-publication check and refuse disposal if the import
already has a published object. Reuse the bounded staging discard from
MOD-120 and keep the historic byte count when removing chunk records.
The terminal reason is `MODEL_IMPORT_EXPIRED`. A second attempt
after a lost commit acknowledgement, a partially removed directory, or
a failed terminal audit must converge without a duplicate expiry event.

The existing [Go standard library](https://go.dev/LICENSE) and
[UUID library](https://github.com/google/uuid/blob/v1.6.0/LICENSE) are BSD-3-Clause,
[pgx](https://github.com/jackc/pgx/blob/master/LICENSE) is MIT, and
[PostgreSQL](https://www.postgresql.org/about/licence/) uses the PostgreSQL
License. Each permits commercial and offline use and redistribution with
its notices. The candidate tests use synthetic chunk bytes and the
existing approved catalogue metadata. No model weights are needed for
the expiry tests.

### PostgreSQL expiry acceptance

The three `TestActualAbandonedImportExpiry*` tests ran against PostgreSQL
18.6 in a Linux arm64 container with no network on November 9, 2026. They
exercise the normal worker loop, a real chunk writer racing expiry, a
fresh commit after candidate selection, an exact chunk replay, intent
and terminal audit rollback, synthetic lost acknowledgements after real
commits, partial staging removal and fresh-worker recovery, scoped
terminal reads and idempotent replay. They also check that expiry is
disabled by default and preserves queued, verifying, failed, complete
and published imports, as well as rows and files in another workspace
and tenant.

Run with an isolated PostgreSQL test database prepared by the
[identity fixture](../../services/control/internal/testing/identityfixture/README.md).

```sh
go -C services/control test ./internal/modules/models -run '^TestActualAbandonedImportExpiry' -count=1 -v
```

### Startup configuration

Use a single optional `runtime.models.import_expiry` mapping in the
protected Control configuration. Require both `workspace_id`, a canonical
nonzero UUID, and `inactive_seconds`, a whole number from 3600 to
31536000 inclusive. Omission keeps all previously installed snapshots
and worker defaults. Explicit null, partial, duplicate, aliased or
mistyped values fail before environment overlays. This follows the
existing strict Models configuration and the
[YAML mapping and scalar rules](https://yaml.org/spec/1.2.2/).
The new group is YAML-only. Operators select the workspace and its
interval together in one protected restart snapshot.

Opt-in requires PostgreSQL and a private model artifact root. At
serving startup, use the selected database to confirm that the workspace
is active and belongs to the active, bootstrapped installation tenant.
A bounded [PostgreSQL existence check](https://www.postgresql.org/docs/18/functions-subquery.html#FUNCTIONS-SUBQUERY-EXISTS)
uses the same scope predicates as the existing worker. Reject a missing,
inactive or foreign workspace before listeners and the worker start.
Attach the policy to the existing model owner before registering its
worker. The worker retains its own per-import authority recheck and
filesystem fences. Accepting any existing workspace or widening scope
to all workspaces would allow a configuration mistake to authorize
cleanup beyond the selected installation. The full Control server owns
the expiry worker lifecycle.

Use the already pinned [YAML decoder](https://github.com/yaml/go-yaml/blob/v3.0.5/LICENSE)
(MIT and Apache-2.0 notices), Go, UUID, pgx and PostgreSQL under the
terms above. This slice introduces no dependency, model weights or data.

Test existing protected YAML, both interval boundaries, invalid shapes
and IDs, incompatible database and missing storage. Start the production
serving composition against real offline PostgreSQL and verify the
default leaves an old upload alone. With the selected workspace
configured, the normal worker must settle the abandoned import and
write its audit entries. A second workspace's upload must survive.
Refuse startup for missing, disabled and foreign workspaces.

The serving regression passed on Linux arm64 with networking disabled,
PostgreSQL 18.6 and pgvector 0.8.7. It loaded the actual protected YAML,
verified loopback HTTPS in the old and opt-in configurations, and ran
the existing worker under the restricted PostgreSQL runtime role. The
legacy startup retained the stale upload. The opt-in startup removed
only the selected upload's staging and wrote both expiry audit events.
A second active workspace retained its staged upload. Missing, disabled
and foreign workspaces failed before HTTPS startup.

With the [protected repository fixture](../../services/control/internal/testing/repositoryfixture/README.md),
run the same serving path using:

```sh
go -C services/control test -p 1 -tags repository_integration ./cmd/server \
  -run '^TestActualModelImportExpiryStartup$' -count=1 -v
```
