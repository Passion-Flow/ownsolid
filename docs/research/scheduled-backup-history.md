# Scheduled backup history on the installation host (REL-111)

The scheduled installer already runs the real PostgreSQL dump, scratch
restore verification and owned archive retention. Its private
`schedule-owner.json` binds a repository to the database deployment ID,
Compose project and installation root. It cannot say whether a backup was
attempted, failed or interrupted. The operator log has free-form details,
omits early failures and records publication before retention. The
coordinated backup and restore files describe specific recovery intents.
They are removed when reconciled and are unsuitable as history.

[Kubernetes CronJobs](https://kubernetes.io/docs/concepts/workloads/controllers/cron-jobs/#jobs-history-limits)
keep bounded terminal run history independently of Job scheduling.
[Velero](https://velero.io/docs/v1.19/how-velero-works/) tracks backup
operation status separately from archive expiry. Keep those two concepts
separate in the local scheduled job. The existing PostgreSQL
[logical dump](https://www.postgresql.org/docs/18/app-pgdump.html) and
verification remain the publication authority. No new dependency, model
or dataset is added. The installer uses the Go standard library
([BSD-3-Clause](https://go.dev/LICENSE)) and the existing PostgreSQL client
([PostgreSQL License](https://www.postgresql.org/about/licence/)).
Commercial and offline use and redistribution remain subject to those
licences.

Persist a bounded, versioned snapshot in the private installation root,
outside the material copied into backups. Reuse the existing repository
owner identity, which includes the project and installation root and gains
its deployment ID from PostgreSQL when first available. Record an admitted
run before opening Docker or the backup repository so an outage can be
seen. A different live deployment ID fails before writing a backup.
Read-only queries verify the local installation binding and can return
the last verified deployment ID while PostgreSQL is down.

Only the scheduled writer holds the installation directory's advisory lock.
A reader can distinguish a running writer from an interrupted attempt
without interpreting another installer operation's lock as a backup.
[Linux flock](https://man7.org/linux/man-pages/man2/flock.2.html) releases
the lock when the process exits. Persist with the installer's existing
atomic private-file writer and directory sync, following the
[fsync durability contract](https://man7.org/linux/man-pages/man2/fsync.2.html).
A retry carries forward the interrupted attempt and starts a new sequence.
No exit time is inferred after a crash. Publication is confirmed only after
the existing backup call has verified and synced it. Persist that fact
before retention so a later retention failure can still show that the new
recovery point was published. A crash before the publication checkpoint
leaves publication unconfirmed.

Expose only the version, deployment ID when known, attempt sequence,
timestamps, outcome, a bounded error code, verified publication and the
number of archives expired when retention completed. Publication describes
the recorded event. Archive availability is a separate concern. Preserve
the latest 256 attempts even when their backup directories are expired.
Do not expose installation paths, filenames, vendor diagnostics, DB
details, keys or user data. Reject treating raw logs, filesystem mtimes or
the presence of a partial archive as durable success. Do not introduce a
second scheduler or a database service for the local status.

Verify the normal scheduled entry with controlled Docker command responses
and real local filesystem writes. Cover owner lookup and dump failure,
successful retention, an interrupted process and a retry, cross-install
refusal, bounded history, invalid state and output redaction. Keep the
previous real PostgreSQL evidence for the backup mechanism separate from
these history tests. The future API should compare the deployment ID with
its authenticated installation context and consume the projected fields.
Console and alert policy remain open.

## Installation-bound consumer readback

The root-only CLI is the installed read boundary. An operations consumer
needs to supply the expected deployment ID from its own authenticated
installation context. Add an explicit `--expected-deployment-id` to the
same command. Compare against the persisted deployment ID after validating
the root and Compose project. A mismatched ID or history without a verified
deployment ID emits no projection. The local operator command can still
diagnose attempts that failed before the first database binding.

Use the same open directory for the advisory lock and for history lookup
with [openat](https://man7.org/linux/man-pages/man2/openat.2.html). Following
the original pathname after taking the lock can read a different directory
if the path is replaced. Refuse symlinks, hard links, nonregular files,
foreign ownership, exposed permissions and oversized history. Decode the
atomic snapshot through the existing strict versioned decoder. A bounded,
explicit projection keeps installation paths, project names, archive paths
and diagnostic details private. The kernel's
[flock semantics](https://man7.org/linux/man-pages/man2/flock.2.html) still
determine whether an unfinished run is active or interrupted. An interrupted
read projects the state in memory and leaves the on-disk record intact.

Reuse the existing Go standard library and
[`golang.org/x/sys` v0.49.0](https://github.com/golang/sys/blob/v0.49.0/LICENSE).
Their BSD-style terms allow commercial use, offline use and redistribution
with notices. No dependency, model or dataset is added. The consumer should
invoke the command through an authorized installation-side operation with a
fixed site configuration and an independent expected ID. Reading the raw
file, trusting the ID returned by the file as the expected ID, or allowing
an API caller to choose the command path would bypass this boundary.

Verify a matching, mismatched, malformed and unbound deployment through
the Linux command. Exercise copied history, unsafe files, directory
substitution, the 256-run bound and the interrupted record before and
after retry. Compare the file bytes before and after readback. Scope the
handoff to this CLI projection. The authenticated Control/API operation and
its Console and alert policy retain their own acceptance.

On Ubuntu 24.04 amd64, the native bound CLI returned the ordered 256-run
projection for a matching deployment. A malformed ID, different ID,
copied installation root, unbound history and 257 rows each failed with
empty stdout. Killing a real holder of the directory lock changed the
last outcome from running to interrupted in the projection. The file's
SHA-256 remained unchanged and the reader supplied no finish time. The
focused tests also refused a changed directory, links and oversized
history, and preserved a verified publication through retention failure
and a retry. The retention test uses controlled PostgreSQL responses.
The CLI harness saw no Docker invocation and only the three recorder
probes over IPv4, IPv6 and DNS.
