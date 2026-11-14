# Published model descriptions (GW-120)

Reviewed 2026-11-09.

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
intact, and the existing API key remained valid after resume. The
candidate down SQL restored the predecessor HTTP response shape.

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
