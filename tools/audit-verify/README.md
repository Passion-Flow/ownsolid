# Offline audit verification

Build a standalone binary from the repository root:

```sh
CGO_ENABLED=0 go build -trimpath -o /path/to/audit-verify ./tools/audit-verify/
```

## Export from PostgreSQL

Run the read-only [export query](export.sql) as an authorized database operator
with the installation schema on the connection's search path. Use a locally
configured libpq service for database access and write the file with restricted
permissions.

```sh
umask 077
psql 'service=ownsolid-audit' --no-psqlrc --tuples-only --no-align \
  --set ON_ERROR_STOP=1 --file tools/audit-verify/export.sql > exported-audit.jsonl
```

The query reads one database snapshot and orders records by chain position
across partitions. Records that predate the migration are outside the chain.
The export includes event identifiers and action metadata. It uses digests in
place of reason text, identifier digests and metadata. V2 events additionally
bind site and cell identifiers. The verifier accepts existing V1 exports and
continuous exports that cross the upgrade boundary.

## Coverage and custody

Hash-chain coverage starts when the migration activates, at position 1.
Audit events already stored at upgrade remain readable and subject to normal
retention. The verifier cannot establish their integrity, and they never
appear in the chain export. An earlier event's occurrence time does not make
it a legacy row if the event was inserted after activation.

The database owner or a PostgreSQL superuser can replace the stored events and
recompute the chain and head. A passing verification without an independently
held checkpoint shows only that the exported range is internally consistent.
To detect a rewrite through a given position, preserve the checkpoint before
a suspected compromise in storage controlled by the auditor, with separate
credentials and write authority from the database operators. Record the
installation identity, position, hash and capture time in the auditor's
records. If the first checkpoint is taken after a compromise, it cannot
establish the earlier state. Events that were never recorded cannot be
verified.

### Capture an export and its checkpoint together

Run from the repository root. The transaction uses one snapshot for both
reads, even if events are appended concurrently. Each capture writes to a
new private directory.

```sh
umask 077
audit_query="$(pwd)/tools/audit-verify/export.sql"
capture=$(mktemp -d)
(
  cd "$capture" || exit
  psql 'service=ownsolid-audit' --no-psqlrc --quiet --tuples-only --no-align \
    --set ON_ERROR_STOP=1 --set audit_query="$audit_query" <<'SQL'
BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
\o exported-audit.jsonl
\i :audit_query
\o audit-checkpoint.txt
SELECT chain_position, encode(hash, 'hex')
FROM audit_chain_head WHERE singleton AND chain_position > 0;
\o
COMMIT;
SQL
)
```

The checkpoint file has one line in `position|hash` format once an event
has been chained. An empty file means there is no checkpoint to retain.
Use the files only after `psql` succeeds and verification passes. Transfer the
checkpoint to the auditor's separate custody. Archive the export according to
the installation's retention policy so it remains available for independent
verification after PostgreSQL expires its partitions.

## Verify on an offline machine

```sh
audit-verify --file exported-audit.jsonl
audit-verify --file exported-audit.jsonl --output json
audit-verify --file exported-audit.jsonl --expected-last-hash "$trusted_checkpoint"
```

Set `trusted_checkpoint` to the hash retrieved from the auditor's copy,
and verify an export ending at its recorded position. A newer export must be
limited to that position for this comparison. Events after it require a later
independent checkpoint to check for a rewrite or truncation of that later range.

For a retained range, set `--previous-hash` to the trusted hash of the event
immediately before the first exported record. For separately exported
partitions with consecutive positions, concatenate the files in chain order
and use `--file -` to verify the combined stream. If backdated events interleave
the partitions, export the partitions together in chain order.

On success the tool prints the number of verified records, the earliest and
latest event times and the final hash. On failure it returns exit status 1
and reports the first affected one-based JSONL line and field. Invalid
invocation returns exit status 2. A zero exit without a trusted checkpoint
establishes internal consistency. Comparing against a trusted checkpoint
also detects complete rewrites and truncation through that checkpoint.

See [AUD-102 serialization and threat model](../../docs/research/audit-chain.md).
