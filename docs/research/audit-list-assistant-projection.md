# AUD-101 assistant attribution in the audit list repository

A workspace audit page needs the built-in assistant's identity and the separate
user subject while preserving the current actor user, subject and pagination
fields. [RFC 8693 section 4.1](https://www.rfc-editor.org/rfc/rfc8693.html#section-4.1)
models the acting party separately from the subject. The
[assistant writer](../../services/control/internal/modules/audit/product_assistant.go)
and [offline disclosure](audit-assistant-disclosure.md) define the stored
metadata and the accepted chain format.

## Scope and storage

Reuse the identity audit list's plan, filters, cursor, row scanner and
transactional session recheck. The opt-in PostgreSQL repository method takes a
workspace UUID. The current Admin session is rechecked before resolving the
workspace against the server's completed installation tenant. Unknown and
foreign workspaces receive the same refusal. Both scope IDs constrain the SQL
before seek and limit, and the workspace is part of the cursor fingerprint.
Archived workspaces remain readable by the same platform administrator for
historical audit. The existing HTTP list keeps its installation-wide Admin
authorization, filters, cursors and JSON schema. Legacy trusted callers and
MySQL/Dameng list SQL use the original projection.

Match the exact two-key JSONB object, no human actor and a present user subject
on a chained row with the writer's assistant action and audience. PostgreSQL
[JSONB equality](https://www.postgresql.org/docs/18/functions-json.html#FUNCTIONS-JSON-PROCESSING)
checks the entire object with typed values. Containment, a prefix alone and
extracting arbitrary metadata fields would also admit lookalikes. SQL returns
a boolean for the validated case, and Go supplies the fixed pair in a
repository field excluded from JSON serialization. The lookup key combines the
public event ID and timestamp, matching the partitioned table's primary key.

The operator export chooses the chain format using the protected chain head.
The [runtime privilege contract](../../services/control/internal/platform/database/runtime_privileges.go)
forbids that read even at column level. Use the stored row's V2 commitment
instead. In a [row-dependent CASE](https://www.postgresql.org/docs/18/functions-conditional.html#FUNCTIONS-CASE),
reuse the canonical V2 projection and framing, and recompute
[SHA-256](https://www.postgresql.org/docs/18/functions-binarystring.html)
with the domain and ordered fields defined by
[migration 000054](../../services/control/migrations/postgresql/000054_audit_chain_scope.up.sql).
This distinguishes a genuine V1 lookalike and rejects an invalid commitment
without a grant, new migration or chain scan. A row commitment does not
authenticate against a separately retained tail hash. The offline verifier
remains the chain and checkpoint verification entry point. A future shared
database helper can own the field order if a migration changes the chain format.

The implementation adds no dependencies, models, data sets or runtime network
services. It uses existing Go standard packages, [pgx v5.11.0 (MIT)](https://github.com/jackc/pgx/blob/v5.11.0/LICENSE),
[Google UUID v1.6.0 (BSD-3-Clause)](https://github.com/google/uuid/blob/v1.6.0/LICENSE),
and built-in functions in [PostgreSQL](https://www.postgresql.org/about/licence/).
These dependencies permit commercial redistribution with their required notices
and run offline.

## Acceptance and handoff

Run the repository test against disconnected PostgreSQL with a restricted role
that cannot read the chain head. Exercise the actual assistant writer and V1
upgrade, an unchained lookalike, altered commitment, extra metadata, incorrect
field type or value, a human actor, absent subject and a prefixed ordinary event.
Assert that a genuine assistant retains its independent user subject and that
ordinary records preserve existing actor and subject fields. Validate stale
sessions, Portal and missing sessions, foreign or unknown workspace selection,
sibling workspace isolation and cursor reuse across scopes. Verify that neither
the hidden actor projection nor raw metadata changes the legacy JSON.

A future HTTP wiring change can use `identity.Service.ListWorkspaceAuditForActor`
with a workspace resolved by its route and look up each returned item by
`identity.AuditEventKey{ID, OccurredAt}` in `AssistantActors`. Absent entries
have no assistant attribution. Add the public OpenAPI fields and generated
clients, settle workspace selection for the existing installation-wide list,
and verify the public schema and console path end to end. The
repository change does not close AUD-101 or AGT-107.

## Verification

The workspace projection passed three consecutive runs against PostgreSQL 18.6
with pgvector 0.8.7 in a container with networking disabled. The reader role
had no table or column privileges on `audit_chain_head`. The identity HTTP
regressions `TestM01EveryHTTPResponseSchema`,
`TestM01CurrentHTTPKnownResourceScope` and
`TestAuditListNamesPeopleAndFiltersByFamily` passed in the same fixture.

With `PROJECT_ONE_TEST_DSN` pointing at an isolated loopback PostgreSQL test
database and pgvector installed, rerun the repository and HTTP tests from the
repository root.

```sh
go -C services/control test ./internal/modules/identity \
  -run '^TestWorkspaceAuditAssistantProjection$' -count=3
go -C services/control test -tags repository_integration ./internal/modules/identity/http \
  -run '^(TestM01EveryHTTPResponseSchema|TestM01CurrentHTTPKnownResourceScope|TestAuditListNamesPeopleAndFiltersByFamily)$' -count=1
```
