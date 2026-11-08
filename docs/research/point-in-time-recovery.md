# PostgreSQL point-in-time recovery (REL-109)

The single-node installer already makes logical, portable recovery points.
Database point-in-time recovery needs a native physical backup and WAL from
the same cluster. The application restores its files and external object
stores through separate recovery paths.

## Proven mechanisms

- PostgreSQL 18 [continuous archiving](https://www.postgresql.org/docs/18/continuous-archiving.html)
  combines a physical base backup with an unbroken WAL sequence. The server
  retries a failed archive command. An archive command may report success
  for a prior file only when its contents match. Missing WAL can prevent
  reaching a requested target.
- [pg_basebackup](https://www.postgresql.org/docs/18/app-pgbasebackup.html)
  coordinates online backups with PostgreSQL over the replication protocol.
  An incremental base takes a prior manifest and needs the corresponding
  WAL summaries on the server. Verify both the cluster identifier and
  the required summary range before accepting a chain.
- [pg_combinebackup](https://www.postgresql.org/docs/18/app-pgcombinebackup.html)
  checks the order and linkage of the full and incremental inputs. It
  does not independently verify each member's files. Use
  [pg_verifybackup](https://www.postgresql.org/docs/18/app-pgverifybackup.html)
  against each member and the combined output and boot a disposable
  target. Use plain directories so the PostgreSQL verifier can parse WAL.
  The replay target must have its own files, because a hard-linked copy
  can modify the retained backup.
- [pgBackRest](https://pgbackrest.org/user-guide.html#restore/point-in-time-recovery)
  distinguishes backup dependency retention from WAL retention and selects
  a recovery target explicitly. Its production repository engine is a
  comparison. The installer already ships the PostgreSQL 18 utilities and
  can prove its first local path using those exact binaries.

The existing PostgreSQL dependency uses the
[PostgreSQL License](https://www.postgresql.org/about/licence/), permitting
commercial use, redistribution with notices, and offline operation.
No model, dataset, new dependency or remote service is required for the
local proof.

## First validation

On an isolated Linux amd64 PostgreSQL 18.6 cluster, take a native full base,
change a row, take a native incremental, then archive two later committed
changes separated by a named restore point. Verify each backup and reconstruct
a disposable target without hard links. Replay to the named target from local
WAL and check that the earlier change exists and the later one does not.
Repeat without a required WAL file and refuse an unreached target. Repeat
with a corrupt backup member and a missing predecessor. Keep the current
logical dump flow and prove the new physical sequence outside production
before adding an installer workflow.

The proof must preserve complete dependency chains, treat restore point
selection as an explicit operator action, and prevent serving if replay
cannot reach that point. Whole-cluster recovery does not restore models,
secrets outside PostgreSQL, or objects at the selected instant. Those
consistency points belong to the cross-store backup path.

## Linux amd64 prototype

PostgreSQL 18.6 from the pinned single-node pgvector image ran in a
temporary container with only loopback and the egress recorder. Its three
test clusters accepted only Unix socket connections. The recorder captured
one isolation probe and no PostgreSQL or backup-tool packets.

The prototype used a full base with one row, an incremental base after a
second row, then a WAL restore point between two later commits.
`pg_verifybackup` accepted the full, incremental and combined plain backups.
`pg_combinebackup` refused the incremental without its full predecessor
and `pg_verifybackup` refused an altered member. Replay restored the three
expected rows and excluded the fourth. With the restore-point WAL segment
withheld, PostgreSQL stopped before admitting a connection.

This qualifies the native PostgreSQL primitives. The operator path still
needs a protected archive location, dependency-aware retention, recovery
target selection and a safe promotion path with the application writer
stopped. Test the installer against the actual archived chain and prove it
does not start control on an unreached target.

## Installer boundary

Opt in at the single-node site configuration. Compose mounts only the
PostgreSQL-writable spool. An installation-specific random owner record
binds the private repository to its install root and Compose project.
Archive WAL into separate system-identifier directories. Publish with
a durable no-replace operation and acknowledge a repeated segment only
when its bytes match. This prevents collisions if a site replaces its
cluster or retries archiving a segment.

Use plain bases with streamed WAL and SHA-256 manifests. Incrementals
reference a named, verified parent from the same cluster, timeline and
PostgreSQL image. Refuse user tablespaces in this path, since plain
pg_basebackup can otherwise use absolute source tablespace paths.
A repeated backup ID must match the original parent and pass verification.
Verify all members before combine. The current server's WAL summaries
are a creation prerequisite, independent of the WAL archive used for
recovery. If the required summaries have expired, take a new full base.

Read a UTC recovery target in a disconnected clone with its own Unix
socket, PostgreSQL configuration and no upstream or archive write access.
Publish a checked logical backup only when PostgreSQL reaches and promotes
at that target. The installer restores that artifact through the existing
database exchange, including schema and authority checks. Bind this artifact
to the source cluster and site and require matching session and mail keys.
The active model store must still contain the recovered database's ready
models. Reject a different live schema. This path restores PostgreSQL data
on its existing installation and leaves cross-store recovery to REL-103.

Use an explicit full-base retention cutoff. Keep newer bases and their
transitive dependencies, including an older full ancestor if needed. Compute
the WAL floor across the retained set on its timeline. Prune only ordinary
closed segments strictly below that floor in the current cluster partition.
Keep history files and refuse WAL expiry when another timeline is present.
The archive is shared by multiple restore targets, so an individual clone's
restart position must never drive expiry. See
[recovery target settings](https://www.postgresql.org/docs/18/runtime-config-wal.html#RUNTIME-CONFIG-WAL-RECOVERY-TARGET)
and [pg_archivecleanup usage](https://www.postgresql.org/docs/18/pgarchivecleanup.html).
