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

## Verify on an offline machine

```sh
audit-verify --file exported-audit.jsonl
audit-verify --file exported-audit.jsonl --output json
audit-verify --file exported-audit.jsonl --expected-last-hash "$trusted_checkpoint"
```

The trusted checkpoint is the final hash for the exported range, stored
separately under the auditor's control before the suspected change. To capture
a new checkpoint, read the position and hash from `audit_chain_head` as a
database operator and preserve them independently. A checkpoint copied from
the same potentially modified export provides no independent evidence.

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
