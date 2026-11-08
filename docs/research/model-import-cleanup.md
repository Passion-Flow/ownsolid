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
for both copies until publication. The existing console text about retained disk
space needs a copy update from the web lane.
