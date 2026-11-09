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

## Verify a disclosed assistant actor

The standard export keeps the metadata redacted. To disclose the built-in
assistant's actor kind and ID, add this statement immediately after the
`BEGIN` in the checkpoint capture above.

```sql
SET LOCAL ownsolid.audit_disclose_assistant = on;
```

The setting applies only to that transaction. The query adds `actor_kind` and
`actor_id` to eligible V2 events. Each disclosed event already carries its
user `subject_user_id`, request ID and workspace in the chain. Use the
updated verifier and the checkpoint retrieved from the auditor's records.

```sh
audit-verify --file exported-audit.jsonl --require-assistant-actors \
  --expected-last-hash "$trusted_checkpoint"
```

The verifier checks the actor values against the metadata commitment and the
subject against the record hash. The strict flag also rejects a missing pair
on eligible records. Eligibility requires exactly the built-in assistant
metadata object, a user subject and no human actor. Metadata with additional
keys remains redacted. The export preserves all record positions and hashes,
so an existing checkpoint for the same range still applies. Use the standard
redacted export with older verifier binaries. The updated binary can verify
older exports without the strict flag.

See [assistant actor disclosure](../../docs/research/audit-assistant-disclosure.md)
and [AUD-102 serialization and threat model](../../docs/research/audit-chain.md).

## Forward a verified window to site-local syslog

Use `forward-syslog` on Linux or macOS with an operator-approved export and
the independently held checkpoints at both ends of its contiguous window.
Each window may contain up to 4096 records. The command verifies the window
before opening a connection. It checks V1 and V2 chain records using the same
verifier as the offline audit command.

Keep the grant and export files private to the operator. Create a dedicated
cursor directory with mode `0700` on a local persistent volume. Use an explicit
private or loopback IP address, TLS server certificate name, and SHA-256 of
the receiver's DER leaf certificate. The receiver must accept TLS 1.3,
authorize the supplied client certificate, and use RFC 5425 octet framing.
The CA, client certificate and client key are file references. Keep the
client key private to the operator.

Example grant.

```json
{
  "version": 1,
  "tenant_id": "00000000-0000-4000-8000-000000000010",
  "endpoint": "10.20.30.40:6514",
  "server_name": "siem.site.example",
  "server_certificate_sha256": "<receiver certificate SHA-256>",
  "ca_file": "/path/to/site-ca.pem",
  "client_certificate_file": "/path/to/siem-client.pem",
  "client_key_file": "/path/to/siem-client-key.pem",
  "cursor_directory": "/path/to/private-cursor"
}
```

```sh
audit-verify forward-syslog --file exported-window.jsonl --grant siem-grant.json \
  --previous-position "$previous_position" --previous-hash "$previous_hash" \
  --last-position "$last_position" --expected-last-hash "$last_hash"
```

For the first position use predecessor position zero and the 64-digit
all-zero hash. To resume, supply an export that includes the saved position
and hash or starts immediately after them. Repeat the same verified input
to retry a blocked position. Recover a missing window from retained exports.
A different tenant or receiver identity requires a separate grant directory
and an independently authorized starting checkpoint.

Syslog carries a versioned projection with the event and granted tenant
UUIDs, UTC timestamp, action and outcome. Scope identifiers, actor and
subject details, request and target data, and integrity digests stay in the
operator export. Event and tenant UUIDs provide a receiver deduplication key.
The cursor covers global positions while the destination receives the
granted tenant's events.

The JSON result reports the saved cursor position, committed local writes in
this run as `sent`, cumulative local writes as `total_sent`, and `dropped`.
The forwarder stops at the first known failed event and keeps `dropped` at
zero. A certificate rejection returns `AUDIT_SIEM_TLS_PEER_REJECTED`. Other
transport failures retry within `--attempts` and `--timeout` and return
`AUDIT_SIEM_TLS_WRITE_FAILED` if the budget is exhausted. Syslog over TLS
provides no application acknowledgment. A completed local write may be
replayed after interruption and provides no receiver durability receipt.

See [the delivery boundary and acceptance](../../docs/research/audit-siem-delivery.md).
