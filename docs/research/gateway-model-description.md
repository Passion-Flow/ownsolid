# Published model descriptions (GW-120)

Reviewed 2026-11-12.

[Hugging Face model cards](https://huggingface.co/docs/hub/model-cards)
record intended uses and limitations with the imported model. The
[LiteLLM model API](https://docs.litellm.ai/api-reference) separates
creating, reading and updating a published endpoint's metadata.
S-304 needs the local administrator's description of a workspace alias.
Two workspaces can publish the same model for different purposes, and an
alias can change its target. Store the description on the alias and keep
the model card and the administrator's audit reason separate.

Use optional plain text on publication and a dedicated administrator edit.
A blank value clears the field. Normalize outer whitespace, allow
multiline text, reject malformed UTF-8 and disallowed control characters,
and limit the result to 500 Unicode characters. Use a constant empty
database default for existing aliases. [PostgreSQL 18](https://www.postgresql.org/docs/18/ddl-alter.html)
can add a constant default without rewriting all the existing rows.
Return the description in the authorized alias detail and list. Render
it as text in the Portal catalogue.

Keep a separate description revision and store a scoped edit intent
with its request fingerprint and applied revision. Reuse the current
workspace authorization and the cutover replay pattern. Store the edit
and its audit event in the same transaction. Replay identical intents
without changing the revision or adding an audit event, and reject an
intent reused with a different payload. Check the expected description
revision before an edit so competing administrators see a conflict.
Publication fingerprinting must continue to accept pre-upgrade retries
with an omitted or empty description. The audit event records the
actor, workspace, alias and edit reason. The description itself is
catalogue content.

A routing revision participates in admissions and the cutover history
and describes the actual serving target. Updating it for a text edit
would change the meaning of retained target versions. Storing the
description in audit reasons would couple display to audit retention and
could expose operator notes. Copying a model card would lose local,
per-alias intent. This design adds no dependency, model or dataset.
[Go](https://go.dev/LICENSE) is BSD-3-Clause,
[pgx](https://github.com/jackc/pgx/blob/master/LICENSE) is MIT, and
[PostgreSQL](https://www.postgresql.org/about/licence/) and
[pgvector](https://github.com/pgvector/pgvector/blob/master/LICENSE)
use the PostgreSQL License. They allow commercial, offline and
redistribution use subject to their notice terms.

Test existing aliases across upgrade, publication with and without a
description, editing and clearing, canonical replay, concurrent edits,
stale revisions, foreign-workspace and Portal writes, and visibility only
through the existing authorized catalogue reads. Confirm that retarget
and rollback preserve the description and that edits leave the serving
revision and existing keys intact. Run an actual Console publish/edit
and Portal read on a disconnected installation.

The database migration and pinned fresh/restored catalogue signatures
are shared database ownership. The strict ModelAlias JSON schema and
its generated clients are DEV ownership. Update both before activating
the distribution field or write route. WEB owns Console and Portal
acceptance for S-304.

## Disconnected schema qualification

The PostgreSQL 18.6 fixture with pgvector 0.8.7 and only loopback
reproduced both current production catalogue signatures on lineage 57.
An isolated schema added `model_aliases.description`,
`model_aliases.description_revision` and immutable
`model_alias_description_intents`. A synthetic predecessor row
received an empty description and initial description revision of 1.
Updating the text and description revision kept the routing and
deployment revisions. The test seeded that row with foreign key
triggers disabled for the synthetic identifiers. The migration and
edit ran with normal triggers. Applying the down migration returned
the original fresh catalogue signature.

The catalogue query in
`services/control/internal/platform/database/catalog.go` measured the
initial, unscoped candidate after migration and after a logical schema
dump and reparse. The scoped candidate below supersedes these values.

| Catalogue | Candidate SHA-256 |
| --- | --- |
| Fresh | f7b8b2169bf305aedbf7c6339acb97cd62f319ae38607225a1e42e600e2eff37 |
| Restored | 21995e131bbd56a90983ad4867a1fd10a31646075c707f12d7bb7712ebd4362d |
| Restored at 55, upgraded through 57 | 32ebd0bf692acf93d8af50d7dd66e8b5d750a9cfd43b6fc13fc3e7b987c296e6 |

The candidate raises the table count from 118 to 119. The DB owner
needs to land the migration with the exact catalogue signatures and
expected table count. Recalculate the signatures if another migration lands.
The isolated SQL files are retained at
`/root/lane-gw-acceptance/gw120.3Htyrvwt/candidate.up.sql` and
`candidate.down.sql`. The distribution acceptance below covers the HTTP flow with that DDL.
The versioned contract remains a prerequisite for Console and Portal
acceptance.

## Distribution integration

Use the existing scoped Admin alias publication with optional
`description`, and a separate scoped Admin POST to
`/aliases/{alias_id}/description` with `description`,
`expected_description_revision`, `reason`, and the standard UUID
`Idempotency-Key`. The response has the current text and description
revision, the applied description revision, and a replay flag. A replay
after a later edit returns the current value and the original applied
revision. The standard origin, session, CSRF, workspace and role checks
apply. An alias can be edited while paused. Portal keeps the existing
visibility rules and reads the fields through alias list and detail.

Use the existing scoped transaction and a row lock before comparing the
description revision, following [PostgreSQL row locking](https://www.postgresql.org/docs/18/explicit-locking.html#LOCKING-ROWS).
Persist only the two description fields, the immutable intent and one
audit event. Keep the same description semantics for publication and
edit. Normalize CRLF and trim outer whitespace after validating UTF-8
and controls. Permit newlines and horizontal tabs. Omit empty text from
the publish fingerprint so old idempotency keys remain replayable.
Reuse the shared strict JSON decoder and standard problem codes.

Candidate writes recognize the owned intent table in the current
PostgreSQL schema and return SERVICE_NOT_READY on a predecessor or
another database engine. A normal publish with an omitted or blank
description keeps the predecessor SQL and fingerprint. The authorized
HTTP alias projection uses [PostgreSQL row-to-JSON conversion](https://www.postgresql.org/docs/18/functions-json.html)
to read the optional columns in the existing held-request lookup. The
projection omits the fields when the predecessor schema is active.
Inference admission and its alias readiness queries do not read the
description. An unconditional SELECT or INSERT of the new fields would
break the current catalogue before the shared migration is published.

Test the current schema and candidate successively in one disposable
database through the real HTTPS dispatcher. Apply the unnumbered DDL
only to the test schema after publishing an alias with the existing
contract. Cover predecessor retries, real Admin and Portal sessions,
a second Admin concurrently editing the same revision, foreign
workspace and tenant refusal, replay with a conflicting payload,
audit rollback, input bounds, clear, and preservation of target history,
route revision and existing key authority. Verify the current published
OpenAPI against predecessor responses. The shared PostgreSQL lineage,
catalogue constants, public schema and generated clients remain
separate integration steps.

The disconnected PostgreSQL and HTTPS fixture
`TestAliasDescriptionPostgreSQLUpgradeAndHTTPS` passed on 2026-11-09.
The real dispatcher returned the published alias schema on the
predecessor, and a described publication and edit returned
SERVICE_NOT_READY. Applying the candidate allowed a predecessor
publication to replay with the same key and the existing alias to
show empty text at description revision 1. Described publication
normalized and replayed the same text. Reusing the key with different
text was refused.

A Portal member read the description from both the scoped page and
detail. Portal writes, an alias in another workspace path, a foreign
tenant workspace, and a missing CSRF token or idempotency key were
refused. The validator accepted 500 Unicode characters and rejected
overlength, invalid UTF-8, prohibited controls, duplicate and null
fields. Two independently authenticated Admin sessions edited the
same expected revision over HTTPS. One won and one received
VERSION_CONFLICT. A historical replay retained the original applied
revision and returned the current description. A forced audit failure
rolled back the update and its intent, and the same idempotency key
could then be used for a different request. Clearing text while paused
left the routing revision, deployment binding and target history
intact, and the existing API key remained valid after resume. The current down SQL refuses retained content, revisions and replay
intents; a pristine downgrade preserves the predecessor response shape.

The unnumbered, test-only up and down scripts live in
`services/control/internal/modules/distribution/testdata/`. The fixture
uses its own PostgreSQL schema and applies both scripts through that
connection. To reproduce in a disconnected loopback fixture with
PostgreSQL 18.6 and pgvector 0.8.7, set
`PROJECT_ONE_TEST_DSN_FILE` to the private fixture DSN and run
`go -C services/control test -p 1 ./internal/modules/distribution -run '^TestAliasDescriptionPostgreSQLUpgradeAndHTTPS$' -count=1`
from the repository root.

The catalogue values above qualify the previous tail only. Once
REL-113 and the audit migration settle, the shared database owner
must append the exact DDL at the then-current tail, recompute the
catalogue signatures and table count, and verify fresh, upgrade and
restore. The main lineage now also admits a schema restored at 55
and then upgraded through 56 and 57 (df1ec219). The disconnected
qualification reproduced that baseline signature, applied the candidate
and verified the same predecessor signature after applying the down
SQL. Recheck that history alongside fresh and current-schema logical
restore after the shared tail moves. The public contract is available.
WEB can verify publishing and editing in Console and the S-304 display
in Portal once installations activate the migration.

## Public OpenAPI and client boundary

The [OpenAPI Schema Object](https://spec.openapis.org/oas/v3.1.0.html#schema-object)
supports optional response fields for predecessor schemas. Declare
description on the existing publication body and description and
description_revision as optional, non-null alias fields that appear
together after activation. Keep the released operation IDs, paths and
required properties. Publish the scoped Admin edit with the existing
session, Origin, CSRF and UUID idempotency rules. Its response distinguishes
the current revision from the one applied by the original intent. A
replacement of the alias would couple catalogue edits to serving revisions.

The server normalizes text before enforcing the 500-character limit.
Document that order, preserve the empty string for clearing a field and
treat the result as plain text. Use the existing [SDK dependencies](sdk.md#licenses)
and local generator. No dependency, model or dataset is added.

Verify both predecessor and candidate responses against the public
schemas over the production TLS dispatcher on a disconnected PostgreSQL
fixture. Cover ordinary and described publication, Admin detail, Portal
list and detail, edit, clear, replay, stale revisions and scoped refusals.
Before activation, writes that need the new schema return SERVICE_NOT_READY.
The migration and Console and Portal interaction remain separate gates.

The disconnected PostgreSQL fixture validated the public alias and edit
schemas through the actual Admin and Portal dispatcher. The predecessor
omitted the optional fields and returned the typed SERVICE_NOT_READY
problem for edits and described publication. The candidate returned both
fields on authorized reads, preserved the independent revision across a
cutover, and decoded successful clearing, historical replay and scope or
revision refusals. Go and Python local client probes preserved omitted
descriptions and explicit empty strings and decoded the typed replay and
predecessor problem.

### Console and Portal handoff

Use the optional publication field and the generated Admin
adminEditAliasDescription operation. Send the current
description_revision with the edit and retain the idempotency key and
payload when retrying an uncertain response. Render only the text in
the Portal catalogue. The two alias fields are absent on predecessor
schemas. The Admin controls and Portal display need installed migration
evidence and their own UI qualification.

## Observed edit failures

The disconnected PostgreSQL and Admin HTTPS probe verified standard
`application/problem+json` responses without a success envelope. The
problem contains `type`, `title`, `status`, `code` and `request_id`.
The predecessor response used the configured `Retry-After` seconds and
its body correlation ID matched the `X-Request-ID` header.

| HTTP status | Observed code | Trigger |
| --- | --- | --- |
| 400 | `VALIDATION_FAILED` | Missing idempotency key or invalid request object, encoding, control characters or description length |
| 401 | `AUTH_REQUIRED` | Anonymous caller with a valid CSRF context |
| 403 | `CSRF_INVALID` | Anonymous POST or an Admin POST without its CSRF proof |
| 404 | `MODEL_ALIAS_NOT_FOUND` | Alias absent from another workspace in the same tenant |
| 404 | `RESOURCE_NOT_FOUND` | Foreign tenant workspace or Portal mutation path |
| 409 | `VERSION_CONFLICT` | Stale description revision in a concurrent edit |
| 409 | `IDEMPOTENCY_CONFLICT` | Changed payload under the same intent key |
| 413 | `PAYLOAD_TOO_LARGE` | Request object exceeds the JSON envelope limit |
| 415 | `UNSUPPORTED_MEDIA_TYPE` | Edit sent as plain text |
| 503 | `SERVICE_NOT_READY` | Alias description fields are absent from the predecessor database |
| 503 | `STORAGE_UNAVAILABLE` | Forced audit insertion failure rolled back the edit |

## Site and cell scope for edit intents

ARC's audit migration qualification identified the unscoped edit-intent
table. Follow [ADR 0006](../decisions/0006-cell-and-site-scope.md) and the
existing alias scope. The new table gets non-null site and cell columns
with the local registry functions as defaults. A composite foreign key
ties both columns to the referenced alias alongside its tenant and
workspace. The alias keeps its original primary key and gets a unique
key for the reference. This uses a
[PostgreSQL foreign key](https://www.postgresql.org/docs/18/ddl-constraints.html#DDL-CONSTRAINTS-FK)
to enforce consistency without locking the registry row on every edit.
A check constraint that reads other tables would make restore order
significant. The service resolves the local scope in SQL and HTTP
continues to reject foreign cell paths before dispatch.

The new table starts empty. Predecessor aliases already carry scope and
receive the same empty description and initial description revision.
Retain alias identity, serving revisions, existing keys and legacy
publication replay. Verify defaulted scope against a second registered
site and cell in an isolated transaction, refusal of mismatched scope
and null values, and no mutation on a foreign-cell HTTP path. Verify the
down SQL removes the new alias reference key and restores the
predecessor. No dependency, model or dataset is added. The existing
PostgreSQL and Go license terms above still apply. ARC owns the numbered
migration and must calculate the catalog signatures on the committed tail.

The scoped SQL is kept unnumbered in
`services/control/internal/modules/distribution/testdata/alias_description_candidate.up.sql`
and `alias_description_candidate.down.sql`. The disconnected PostgreSQL
18.6 and pgvector 0.8.7 run passed
`TestAliasDescriptionPostgreSQLUpgradeAndHTTPS`, including 23502 for
explicit null, 23503 for a mismatched site or cell, defaulting to a
second registered scope inside a rolled-back transaction, and
RESOURCE_NOT_FOUND for a foreign cell write with no alias change or
intent. The old publication replayed and its original deployment
binding remained usable.

An isolated SQL overlay applied the exact ARC 000058 up SQL from
`032b21e6` before applying the scoped candidate. It reproduced all three
of ARC's predecessor signatures, preserved the seeded alias binding
and description defaults, and restored the baseline signatures on
the fresh and restored-at-55 paths after running the down SQL.

| ARC 000058 overlay history | Scoped candidate SHA-256 |
| --- | --- |
| Fresh | 72aa05f9653316c589fd79c12ba43580b589f019a35b09b2569754b91d1ee782 |
| Restored from current schema | 18bc8279d46bf95454b7fd0236b9ab6ee54b073eb12551a87968d2a545db59dc |
| Restored at 55, upgraded through 58 | 8c2f55bab0055c374d9083cfa993fc9986cf9b3ebb7e8fd6b06c0adfdd415069 |

These are overlay measurements. Recompute the signatures, release
lineage and expected table count from the actual committed tail when
the migration is ordered. The candidate adds one table.


## Schema-58 migration handoff

The committed baseline 106ca52d includes the GW-120 production source
from 8d359f49 and its scoped correction c9654657, plus the published
OpenAPI contract. No additional production Go patch is needed to
activate descriptions. ARC must append the exact unnumbered up SQL
from distribution testdata at the actual shared tail, preserve all
previous migration bytes, and recalculate the complete catalogue and
runtime access checks. This gateway lane does not assign the number.
The candidate adds one public table and raises the baseline count from
118 to 119 before any subsequent migration.

The new table is model_alias_description_intents: tenant, workspace,
site, cell, actor, UUID intent key, alias, applied description revision,
32-byte request fingerprint and creation time. Its composite primary
key retains actor-scoped idempotency; its composite alias foreign key
fences tenant, workspace, site and cell together. Both cell columns
are non-null and default from the local registry. The existing
protect_alias_cutover_record trigger rejects updates and deletes.
model_aliases receives the plain text and independent revision, plus
the composite unique key needed by the new foreign key. No role,
sequence, extension, model, dataset or dependency is introduced.

Apply migration DDL with the existing operator and installation lock,
then extend the installed restricted runtime principal through the
existing runtime-access step before starting control. The current
grant policy requires SELECT, INSERT, UPDATE and DELETE on business
tables, with no ownership, DDL, TRUNCATE, REFERENCES, TRIGGER or grant
option; the immutable trigger still refuses intent updates/deletes.
The new table is not an exception to that existing policy. Do not
grant the runtime role access to audit_chain_head or writes to the
migration ledger. ARC must verify effective runtime privileges against
the actual new TableCount; the gateway candidate does not weaken the
platform admission predicate.

### Safe rollback

Run the down script in one operator transaction while serving is
stopped. Acquire ACCESS EXCLUSIVE locks on aliases and description
intents before checking retained state. Refuse with SQLSTATE 55000 and
MODEL_ALIAS_DESCRIPTION_DOWNGRADE_REQUIRES_RESTORE if any description
is nonempty, any independent revision differs from 1, or any edit
intent exists. Clearing the visible text does not make its revision
and replay history disposable. A verified pre-upgrade backup and the
normal restore procedure are required once the feature has been used;
the down script never clears content or deletes intents to force a
rollback. A pristine, unused upgrade can remove the new columns,
reference constraint and table and retain the original alias routing
state. The [PostgreSQL lock modes](https://www.postgresql.org/docs/18/explicit-locking.html#LOCKING-TABLES)
and [exception semantics](https://www.postgresql.org/docs/18/plpgsql-errors-and-messages.html)
provide the same-transaction check and refusal. Existing licence
terms above apply.

Keep the predecessor HTTPS upgrade fixture pinned to schema 58 so it
continues to prove omitted fields, SERVICE_NOT_READY and legacy
publication replay after the production migration lands. A separate
installed-schema fixture must use normal database migration/checking
and run the same Admin/Portal flow without applying candidate DDL.
Until ARC installs the shared migration, that fixture remains pending.
Test pristine rollback, every retained-state predicate, rollback after
clearing, and a writer committed while downgrade waits for its lock.
Neither the feature nor S-304 is complete until the installed schema,
restricted runtime and actual Portal consumer are qualified.


### Committed schema-58 qualification

The exact production schema-58 files from 106ca52d reproduced the three
baseline catalogue signatures before applying the candidate. Fresh
schema, a schema-58 logical restore followed by candidate application,
a current candidate dump/reparse, and restore at 55 followed by upgrade
through 58 all passed on PostgreSQL 18.6 and pgvector 0.8.7 with no
external network. The latter two current restore paths agreed on the
same digest. Pristine down returned the exact fresh and restored-at-55
baseline catalogues. The three resulting signatures remain the values
in the scoped table above. These are candidate measurements against
the committed predecessor, not the formally numbered production tail.

The unnumbered up SQL SHA-256 is
399432d5de8be8daad68db91d74706e52456ff3d0560048e33d5a10088ab72ab.
The original handoff down SQL SHA-256 is
4180f1bf9791fd545163f29a49840727c7b9a20693d6fcd4356888f450da3ac2.
Changing only the down refusal does not change the candidate catalogue.
Keep the up bytes unchanged. The isolation successor below tightens
the down script before formal admission. Verify the complete release
lineage and runtime principal against the final numbered files.

The schema-58 HTTPS upgrade test passed with real sessions, CSRF,
concurrent revision refusal, exact replay, scope checks and retry after
audit rollback. TestAliasDescriptionRollbackPreservesRetainedState
passed pristine rollback and independent text, revision and intent
refusals. TestAliasDescriptionRollbackWaitsForCommittedWriter observed
the table-lock wait, committed the writer, and then received the
retained-state refusal without losing the write. Clearing through the
HTTP route still prevented downgrade and retained a successful replay.
The installed-schema test was explicitly skipped on schema 58.

The Linux handoff and logs are retained in
/root/lane-gw-acceptance/gw120-schema58._65ygj2r. run-tests.sh runs the
focused PostgreSQL/HTTPS checks; catalog.sh reproduces fresh, current
restore and old restore in one disposable container. Both acquire the
shared Linux heavy-test and SCI replay locks and enforce the resource
preflight. The schema fixture uses only its own namespace and no host
listener. No model or GPU ran. For an already prepared private fixture,
run the native tests from the repository root:

```sh
go -C services/control test -p 1 ./internal/modules/distribution -run '^TestAliasDescription(PostgreSQLUpgradeAndHTTPS|InstalledSchemaAndHTTPS|RollbackPreservesRetainedState|RollbackWaitsForCommittedWriter)$' -count=1
```

After formal migration, require InstalledSchemaAndHTTPS to pass using
the normal production migrator and catalog checks, without applying the
candidate SQL. ARC owns the exact numbered files, catalogue constants,
TableCount and restricted runtime verification; GW owns this backend
read/write regression; WEB and DEL own the installed S-304 consumer
qualification. Neither the candidate run nor a skipped installed test
is a completed production description flow.


The pristine predecessor rollback assertion compares the exact captured
schema-58 catalogue before and after the candidate, rather than calling
the check for the installed current tail. This keeps the predecessor
regression valid when ARC adds the production migration. The focused
PostgreSQL/HTTPS and concurrent rollback tests passed with this assertion.


## Formal migration admission plan

The integration baseline is 1aad4eed, which retains schema 58 and 118
public tables. The handoff from ba815623 was checked locally against
its complete SHA-256 manifest, including the eight unchanged production
source files. Assign only the next contiguous version, 59. Preserve the exact
up bytes and use the isolation-guarded down successor below. Preserve every earlier SQL asset and release
lineage entry. This changes no agent protocol or agent version policy.
No new dependency, model, dataset or network runtime is introduced.

The PostgreSQL 18 locking and exception references above were reviewed
again on 2026-11-12. ACCESS EXCLUSIVE conflicts with both reads and
writes and remains held through the transaction; a raised exception
aborts the transaction. Use the operator's existing bounded transaction
and stopped-serving installation procedure. Reject destructive rollback
rather than silently discarding retained content. Reject dropping only
the intent table or checking state before taking locks, because either
would bypass the retained-state or concurrent-writer guarantee.

Acceptance requires independently reproducing the three catalogue
forms on PostgreSQL 18.6 with pgvector 0.8.7, using production migrations
and the historical 55 through 58 restore inputs plus a current dump.
Require repeated normal migration/readiness, unchanged predecessor
checksums and retained audit checkpoints. Exercise missing-grant refusal,
existing-role grant resume without OID or password changes, runtime
intent mutation refusal and journal/private-head denial. Run the handed-off
predecessor, installed-schema, retained-state and waiting-writer tests
against the same formal source commit, then complete the repository gates.
These are admission requirements, not new run results. UI qualification
and the GW-120 roadmap tick remain with the Gateway and Web owners.


### Downgrade transaction isolation

[PostgreSQL LOCK](https://www.postgresql.org/docs/18/sql-lock.html)
requires either READ COMMITTED or taking the lock before the first
snapshot in a REPEATABLE READ or SERIALIZABLE transaction. A table lock
taken after an earlier snapshot does not make subsequent reads current.
A transaction that first inspects an alias, then waits for an outside
writer, can miss the newly committed description when checking whether
downgrade is pristine. Stopping new serving traffic does not repair an
already captured operator snapshot.

An isolated prototype on 2026-11-12 used the frozen 62a3792f source,
PostgreSQL 18.6 and pgvector 0.8.7 in a network-none Linux arm64
container on the Mac arm64 workstation. The original down script passed
its destructive DDL after another transaction committed a description;
the added assertion failed as expected. The prototype rolled back its
DDL and retained the writer's committed data. Prepending SET TRANSACTION
ISOLATION LEVEL READ COMMITTED rejected the stale snapshot with SQLSTATE
25001 before locking or DDL, and the retained-data assertion passed.
This is a prototype result, not the complete formal migration gate.

Make that SET the first executable statement of both the qualified
fixture and numbered down script. A fresh operator transaction selects
READ COMMITTED before acquiring the two ACCESS EXCLUSIVE locks. A
transaction that already captured another isolation snapshot refuses;
roll it back and start a fresh operator transaction. Do not merely
mention the isolation requirement in a comment or rely on a deployment's
default_transaction_isolation. Neither guarantees a current state check.
The up checksum and structural catalogues remain unchanged. The current
down SHA-256 is 61a6eb6f8dabc011610c5b307ab904c5c038ec7119a967d48512adbd5dd52c86.

Add production-script regressions for prior REPEATABLE READ and
SERIALIZABLE snapshots, checking exact 25001 refusal and retained writer
data, plus fresh transactions at every supported PostgreSQL isolation
level, which must still refuse retained description state with 55000.
Keep the original pristine, cleared revision, intent-only, concurrent
lock-wait and installed HTTPS cases. Require all of them to run on the
same formal source before publication.


## Formal schema 59 acceptance

The exact source a446ec580cf6dffc63df5d8269518f1ef8175d40 passed the
formal admission on 2026-11-12. PostgreSQL 18.6 and pgvector 0.8.7 ran
in network-none Linux arm64 containers on the Darwin arm64 workstation.
The accepted schema-58 predecessor was built separately from 106ca52d.
All 58 preceding up/down assets and all 58 release entries remained
byte-identical. Migration 000059 uses the owner up bytes and the
isolation-guarded down successor qualified above. The normal production
migrator, schema checker and runtime grant catalog now bind 59 entries
and 119 public tables, including the migration ledger.

| Actual migration history | Catalogue SHA-256 |
| --- | --- |
| Fresh installation or native 58-to-59 upgrade | 72aa05f9653316c589fd79c12ba43580b589f019a35b09b2569754b91d1ee782 |
| Restore at 55, then upgrade to 59 | 8c2f55bab0055c374d9083cfa993fc9986cf9b3ebb7e8fd6b06c0adfdd415069 |
| Restore at 56, 57, 58 or 59, then admit 59 | 18bc8279d46bf95454b7fd0236b9ab6ee54b073eb12551a87968d2a545db59dc |

All seven histories ran the production migrator twice and the normal
schema check, asserting exactly 59 ledger entries and 119 tables.
The original 55 through 58 custom-format archives were checksum-checked
before use. Audit exports from all four historical restores matched
the retained original JSONL bytes, and the independent offline verifier
accepted the original checkpoint each time. These runs qualify the
measured catalogue histories and retained audit data; they are not a
full installed browser or model-serving qualification.

Six distribution test roots passed without skips: predecessor HTTPS,
normal installed-schema HTTPS, four retained-state rollback cases,
the committed-writer lock wait, two prior-snapshot refusals and four
fresh-isolation cases. Eight runtime-access roots also passed without
skips, including the new 58-to-59 grant resume. Missing new-table access
refused serving with DATABASE_RUNTIME_PRIVILEGES_INCOMPATIBLE and SQL
42501. Grant-only resume and repeat retained the role OID, password
verifier and every predecessor ledger row. The actual runtime principal
reached the new foreign key but could not read audit_chain_head, mutate
the ledger, truncate the intent table or disable its triggers.

The same source passed an uninterrupted make check, make build and
make test on the workstation. Chromium passed 1,075 Console and 389
Portal cases; the unchanged live-environment skips were 28 and 9.
Both suites ran without retries, at four and one workers respectively.
Go, Rust and Web checks and tests passed. The private APFS temporary
volume and all owned PostgreSQL containers, volumes and credential files
were removed after the runs. The raw catalogue/restore log SHA-256 is
069f70673f0764e6c16adb027488c53ed2d47aa32733d51ce0b01ed1c4e46a1d.
The retained admission manifest SHA-256 is
4a3e92dbd921d0d971ebcd1395329da32ed8e0dcf9b8b901f592123d667a76b4.

The separate native Linux amd64 queue exited at resource admission and
ran no new compiler or database. Do not report this arm64 result as an
amd64 rerun. No model, GPU or production browser installation ran in
this qualification. GW-120 and S-304 remain open until Console editing
and the actual Portal consumer complete their installed acceptance.

With an explicitly owned private PostgreSQL fixture configured through
PROJECT_ONE_TEST_DSN_FILE, reproduce the current backend regressions:

```sh
go -C services/control test -p 1 ./internal/modules/distribution -run '^TestAliasDescription' -count=1 -v
go -C services/control test -p 1 ./internal/operations/databaseaccess -run '^(TestAliasDescriptionUpgradeResumesExistingRuntimePrincipal|TestUpgrade.*|TestActualRuntimeAccessPreparationAccountWorkspaceDMLAndNoDDL)$' -count=1 -v
```


## Console description editing and Portal reading

Reviewed 2026-11-12 against the published adminEditAliasDescription contract,
the existing S-205 gateway card and S-304 catalogue drawer. The Portal
already reads the authorized alias's optional description as plain text,
preserves line breaks and uses workspace-keyed queries. Retain that reader
and its predecessor fallback. Do not manufacture a price or mark POR-104
complete while TEN-107 is absent.

Place Edit description beside the published name on S-205. It is a
secondary action on a detail page, opening the design system's short-form
dialog with a labelled multiline description and an audit reason. Use a
controlled textarea, following [React's form guidance](https://react.dev/reference/react-dom/components/textarea),
so failed writes and revision conflicts never replace the draft. Reuse
native HTML Dialog for focus containment, Escape and trigger restoration,
as specified by [WAI-ARIA's modal dialog pattern](https://www.w3.org/WAI/ARIA/apg/patterns/dialog-modal/).
Do not nest dialogs. The dialog's one primary action is Save description.

Send the dedicated typed Admin request with the alias/workspace from the
surrounding server resource, the description revision and a UUID intent.
Reuse the same intent for an identical unanswered request, including an
unknown commit outcome. Changing the payload or reviewed revision starts
a new intent. Do not automatically retry a mutation or optimistically
claim success; [TanStack's mutation guidance](https://tanstack.com/query/latest/docs/framework/react/guides/mutations)
describes explicit mutation state and retry control. On VERSION_CONFLICT,
keep description and reason, offer Review latest, show the current text,
and require a deliberate save against that reviewed revision. A read
failure retains the draft and offers retry. A missing alias or permission
refusal explains the boundary and does not offer another write. Never
reload the whole page to handle a stale description.

Count normalized Unicode code points for the 500-character guidance;
HTML maxlength counts UTF-16 units and would reject valid supplementary
characters. Keep server validation authoritative for UTF-8, controls and
normalization. Empty text clears. Clearing is reversible: offer a Toast
Undo that restores the previous text with the returned description
revision and a separate retained intent, so a later writer is never
silently overwritten. A historical replay may return a newer current
value; show that value, distinguish it from this intent's applied revision,
and do not offer an Undo based on text that this request no longer owns.
Description edits must leave the routing revision, target and existing
key authority alone.

| State | Console description behavior |
| --- | --- |
| Loading | The gateway card retains its existing skeleton; review of current text marks its own request busy. |
| First-time empty | The published name says No description yet and offers Edit description. |
| Filtered empty | S-205 has no description filter; S-304 retains its distinct filtered catalogue empty state and Clear filters. |
| Partial | A description write or latest-text read fails without discarding the deployment, endpoint or draft. |
| Error | Explain the server problem and retry an identical transient or unknown-outcome request with the same intent. |
| Denied | Keep the draft readable, show the real 403/404 problem, and disable saving in that dialog. Portal has no editor. |
| Not ready | An alias without the paired description fields keeps Edit description visible with an upgrade explanation and aria-disabled. |

Verify save and reload, clear and Undo, competing revisions and review,
a failed latest read, lost-answer replay including a later writer,
403/404 refusal, a predecessor response and 500 supplementary characters.
Check English and Simplified Chinese with keyboard operation and actual
screenshots, including 390-pixel width. The Portal must reflect description
changes only in the current authorized workspace and remain read-only.
Local preview assertions complement the existing real PostgreSQL/HTTPS
backend tests; actual same-source installed Console/Portal acceptance
remains required and must be recorded separately.

No dependency, model or dataset is added. Existing React, TanStack Query
and Radix UI are MIT; Playwright is Apache-2.0. Their licence terms allow
commercial, redistribution and offline use with the retained notices.
The existing Go, pgx, PostgreSQL and pgvector terms above still apply.

The existing catalogue polling retains successful query data on a later
error. Distinguish a transient refresh failure from an authorization
refusal: the former keeps the known names with a retry notice; a 403 or
404 must close the description drawer and show the refusal instead of
continuing to present the cached catalogue as current. Test the transition
after a successful read, not only an initial failed request. This is a UI
truthfulness boundary; the backend still owns membership and tenant checks.

### Web consumer qualification

The Web tree committed as bc3fa720c788db651fa00347bb25edd181261463
passed 28 local Console Chromium cases and 23 Portal cases, with one
worker, no skips, retries or unexpected results. Both production Web
builds passed their bundle budgets. The focused checks passed both app
type checks, lint, the two normalization cases and all 49 locale tests.
The full repository gate is recorded separately after it finishes.

The cases cover save/reload, clear/Undo, a competing description revision,
a failed latest read with retained draft and reason, explicit review and
save, an unavailable predecessor, 403/404 refusal, lost-answer replay with
a later writer, an unknown commit outcome, 500 supplementary characters,
an edit while paused, a conflicting Undo and an answer after closing the
editor. Existing gateway publication and key flows remain exercised.
Portal checks both languages, switches between two permitted workspaces,
renders markup as text without requesting its image, exposes no editor,
withdraws cached descriptions on a later 403/404 and retains known data
with recovery on a transient refresh failure. These are preview tests;
the real backend's authorization and transaction tests remain independent.

Eight new screenshots were read: Console editing and current-text review,
and Portal reading and permission refusal, each in English and Simplified
Chinese. The English views use the desktop viewport; Chinese uses 390 by
844 pixels. Raw reports and images are retained outside Git in the
GW-120 Web qualification artifacts.

S-205 remains a detail page with its existing tabs and gateway card. Its
page primary action remains Scale; Edit description is secondary. The
short-form dialog has one primary action, Save description. It contains
no nested surface, preserves a clear label and reason, and shows the
reviewed current text above the retained draft. The seven states are
defined in the table above. The empty-description message is not the
catalogue's filtered-empty message. Escape restores the opener, Tab shows
focus, and the form and reviewed text fit the narrow viewport.

S-304 remains a list with filter tiles, search and rows, opening a preview
drawer for a selected model. Its specified primary action is Try a model;
that option remains visible and aria-disabled with the playground reason.
Issue a key offers the available path. Loading, initial empty, filtered
empty with Clear filters, partial facts/refresh failure, failed read,
permission refusal and Not ready all retain distinct behavior. On refusal,
the drawer closes and the real problem replaces the cached rows. Status
has text as well as color. The narrow description and request ID wrap
inside their containers. Neither screen claims a price or a working
playground. Actual same-source installed Console edit/clear/retry and
Portal read, including database evidence, remain open.
