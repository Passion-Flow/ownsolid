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
