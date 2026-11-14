# AUD-102 audit chain and offline verification

## Evidence and threat model

[AWS CloudTrail integrity validation](https://docs.aws.amazon.com/awscloudtrail/latest/userguide/cloudtrail-log-file-validation-digest-file-structure.html)
links each digest to its predecessor and authenticates digest files separately.
Its [offline validation design](https://docs.aws.amazon.com/awscloudtrail/latest/userguide/cloudtrail-log-file-custom-validation.html)
preserves verification material independently of the logs. GitLab's
[administrator audit export](https://docs.gitlab.com/administration/compliance/audit_event_reports/)
provides an operator-readable file. A filtered UI export can omit intermediate
records by design, so the integrity export must cover a contiguous sequence.

Use SHA-256 as specified in [FIPS 180-4](https://csrc.nist.gov/pubs/fips/180-4/upd1/final).
PostgreSQL 18 provides [SHA-256 over binary data](https://www.postgresql.org/docs/18/functions-binarystring.html)
without an extension, and Go provides it in the standard library. The verifier
checks record content and predecessor links locally. An auditor must also be able
to supply a separately retained, trusted final hash. A database owner who can
rewrite the rows and recompute every later hash can produce a consistent file.
Comparing with the independent final hash detects this rewrite and tail truncation
within that checkpoint's coverage. The operator must preserve checkpoints outside
the database administrator's control. The chain also cannot prove that an event was
captured in the first place.

[PostgreSQL object ownership](https://www.postgresql.org/docs/18/ddl-priv.html)
lets the owner modify the object regardless of privileges assigned to the serving
role. A checkpoint stored only in PostgreSQL would share that authority. Follow
CloudTrail's separate custody principle using an offline auditor-controlled
store. No signing service, cloud storage or new dependency is required. Capture
the head and export in one [read-only repeatable-read snapshot](https://www.postgresql.org/docs/18/transaction-iso.html)
so a concurrent append cannot produce a checkpoint for a different export.
Backfilling legacy rows was rejected because a digest computed during upgrade
cannot attest to the rows before the upgrade. Preserve those records under
existing retention and document the gap in customer-facing instructions.

Exercise the documented capture against PostgreSQL and check the archived head
against the export. Confirm the export excludes pre-chain rows, an internally
consistent replacement chain can pass without the original checkpoint, and
the independently retained checkpoint rejects a replacement or truncated tail.

## Persistence

PostgreSQL is the first implementation. Many PostgreSQL modules insert directly
into the audit table, and the normal application writers borrow their caller's
transaction. Apply the chain at the partitioned parent. PostgreSQL
[row triggers](https://www.postgresql.org/docs/18/sql-createtrigger.html) are cloned
onto existing and future partitions. A row-level AFTER INSERT trigger sees only
rows actually inserted, including with multi-row inserts and conflict handling.
It records the chain position and hashes on the inserted row in the same
transaction. Any failure must abort the original insert.

Maintain one private tail row with the last committed position and hash. Lock it
with SELECT FOR UPDATE, append and update it in the writer's transaction.
[PostgreSQL row locks](https://www.postgresql.org/docs/18/explicit-locking.html)
hold through commit or rollback. At READ COMMITTED a waiter sees the latest row;
stricter isolation may report a retryable serialization error. The head and event
either both persist or both roll back. Each append touches one tail row and one
event identified by its partition key and primary key. It does not scan the
history. A trigger-owned path lets the runtime role insert audit events without
receiving permission to modify the tail directly.

Use a sequence value allocated from the locked tail, not a PostgreSQL sequence
object, so rollbacks do not leave gaps. Position starts at 1 and the predecessor
of position 1 is 32 zero bytes. Position, not the timestamp, determines chain
order. Event timestamps can be equal, arrive out of order or cross a month
boundary while a transaction is open. Export a contiguous sequence in chain
order, reading across monthly partitions. Adjacent partition files can be
verified as one stream when their position ranges are consecutive. If
backdated rows interleave ranges, export the union in position order. Retention
does not reset the head. A retained suffix can start from a separately saved
predecessor hash. A gap inside the retained range requires the missing archive
to verify the full span.

Start coverage at migration activation. Existing rows retain their original
meaning and remain readable. Backfilling the history under the migration lock
would rewrite every retained partition and would only attest to the old rows'
state at migration time. Integrity export must make coverage visible. Confirm
that the offline backup and restore path preserves the chain and private head
together. The current restore comparison excludes its own newly appended audit
row. For the comparison only, project the private head as that row's predecessor
after checking that the new row is the actual tail. The restored database keeps
the new row and the advanced head.

A process mutex or independent writer would leave other control replicas or
direct SQL writers outside its ordering guarantees. An asynchronous queue would
separate audit persistence from the business transaction. Looking up the latest
event on every write still needs concurrency control and would depend on retained
partitions. A partition-local head loses continuity across partition boundaries.

## Version 1 serialization

JSONL is the transport. Hash a versioned binary projection, independent of
JSON whitespace, escape spelling, object member order and local time zone.
Begin with the literal ASCII bytes of ownsolid.audit.v1 followed by one NUL byte.
Append the decoded 32-byte predecessor hash. Append the following fields in
order as framed UTF-8 strings:

1. Position in unsigned decimal without leading zeroes.
2. Event ID, occurrence time, actor ID, subject ID, tenant ID, workspace ID,
   audience, action, outcome, target type, target ID and request ID.
3. Reason commitment, identifier digest commitment and metadata commitment as
   lowercase SHA-256 hex. A nullable source field remains null when absent.

A frame starts with a signed 32-bit big-endian length in bytes, followed by
those bytes. Length -1 encodes null with no following bytes. Length 0 encodes
an empty string. UUIDs use lowercase hyphenated text. Time is UTC and uses
[RFC 3339](https://www.rfc-editor.org/rfc/rfc3339) with exactly six fractional
digits and a literal Z. The six digits match the database's timestamp precision.
Hash the complete projection with SHA-256 and export the result and predecessor
as 64 lowercase hex digits. Export positions as decimal strings to avoid
loss of precision in JSON tools.

Commit to existing reason text as UTF-8 and to an existing identifier digest as
its raw bytes. Commit to metadata as a recursively typed binary encoding.
Use the ASCII tags n, f and t for null, false and true. Use s or d followed by
one nonnull frame for strings and decimal numbers. Decimal numbers have no
exponent, redundant leading or fractional trailing zeroes or negative zero.
Use a or o followed by an unsigned 32-bit big-endian entry count for arrays and
objects. Array items retain their order. Object entries consist of a framed
UTF-8 key and an encoded value, sorted by unsigned byte order of the key.
Unicode strings are preserved without normalization. PostgreSQL
[stores exact JSONB numbers](https://www.postgresql.org/docs/18/datatype-json.html).
The [JSON Canonicalization Scheme](https://www.rfc-editor.org/rfc/rfc8785) uses
binary64 number serialization, which would round some valid existing JSONB
numbers. PostgreSQL's default JSONB display format is also unsuitable as a
portable canonical format.

The export recomputes commitments from the current stored fields and carries
the original chain hashes. Changes in a committed field before export therefore
break verification. The export does not disclose the underlying reason, raw
identifier digest or metadata. A digest is not encryption and can allow guesses
against low-entropy values. The audit path must continue to accept only
operation metadata and must never be given credentials, tokens, request bodies
or user content. The verifier must report positions and field names without
echoing input values.

## Dependencies and acceptance

The PostgreSQL engine and built-in functions use the
[PostgreSQL License](https://www.postgresql.org/about/licence/). The standalone
verifier uses only the [Go standard library](https://github.com/golang/go/blob/master/LICENSE)
under Go's BSD-style license. Existing integration tests use
[pgx (MIT)](https://github.com/jackc/pgx/blob/master/LICENSE) and
[google/uuid (BSD-3-Clause)](https://github.com/google/uuid/blob/master/LICENSE).
These licenses permit commercial use, redistribution with the required notices
and offline use. No model, data set, runtime download or new third-party
dependency is required.

- Compare SQL-produced hashes with independent Go verification, including
  Unicode, null versus empty, JSON object key permutations, exact decimals,
  time-zone offsets at insert and fixed UTC timestamp precision.
- Verify an unmodified export. Modify a middle event and report its exact
  one-based JSONL position and content hash mismatch. Delete an event and
  report the first surviving record with a sequence or predecessor mismatch.
- Reject malformed, duplicate or unknown fields and empty input. Check that
  an independent final hash detects truncation and that a wrong predecessor
  rejects a retained suffix.
- Use real PostgreSQL to append 1,000 events concurrently across connections,
  including direct inserts. Confirm rollback and conflict handling leave no
  orphan head advance. Append across two physical monthly partitions and
  verify the boundary and a retained suffix.
- Build a standalone executable, export synthetic audit rows from PostgreSQL,
  and run the verifier using only the resulting JSONL. Verify both text and
  JSON output and nonzero exits on failure.

## Version 2 site and cell coverage

The audit table also stores site and cell identifiers from the cell-scope
migration. V1 covers the fields listed above. V2 adds the stored site and
cell identifiers so modifications to placement metadata break verification
as well. V2 changes the domain prefix and transport format to
`ownsolid.audit.v2` and appends nonnull `site_id` and `cell_id` UTF-8 frames,
in that order, after the metadata commitment.

The migration preserves all existing hashes and appends V2 records to the
same tail. The private head records the position of the first V2 append. Old
records and exports remain V1. A verifier accepts one transition from V1 to
V2 and rejects a return to V1. An independent checkpoint still covers the
same prefix across an upgrade. The previous V1 portion cannot attest to
site or cell identifiers that were not hashed when its rows were written.
The upgrade test must create a V1 record before activation, append a V2
record after activation, verify both together and detect a changed V2
placement field.
