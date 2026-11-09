# Opt-in assistant actor in the public audit contract

The existing Admin audit list is an installation-wide, authenticated read.
The [workspace projection](audit-list-assistant-projection.md) verifies the
assistant writer's V2 row commitment and preserves the separate user subject.
IAM's staged HTTP implementation at `9a6d1b6c` uses the same Admin route,
session, filters and cursor with an opt-in representation. It adds the
assistant pair only for a verified row in a matching workspace of the
installed tenant on PostgreSQL. Other rows and storage dialects retain
the legacy representation.

Declare the optional `X-Ownsolid-Audit-Actor` request header with the
supported value `true`. Exactly one such header requests attribution.
Absent, unrecognized or repeated values keep the legacy representation.
The preference grants no authority. Describe the existing no-store
response policy and the [Vary semantics](https://www.rfc-editor.org/rfc/rfc9110.html#section-12.5.5).
Follow the [OpenAPI header parameter and Schema Object rules](https://spec.openapis.org/oas/v3.1.1.html#parameter-object).

Keep `AuditEvent` closed and preserve its required fields and nullability.
Add the two optional, non-null strings with the values the writer and
reader agree on. [JSON Schema dependentRequired](https://json-schema.org/understanding-json-schema/reference/conditionals#dependentrequired)
in both directions expresses the pair without making the legacy response
invalid. Publishing unconditional fields would break strict clients that
still use the old schema. A separate endpoint or workspace selector
would alter the list's authority or pagination. Generated consumers
receive the optional request and response shape while the HTTP handler
continues to select the representation.

The contract uses the existing OpenAPI, Web generators and JSON Schema
validator documented in the [SDK review](sdk.md) and the [IAM projection
research](audit-list-assistant-projection.md). No dependency, model,
dataset or runtime network fetch is introduced.

Verify the opt-in response against the new schema and the default response
against the original closed schema. Reject incomplete or invalid actor
pairs and metadata. Exercise legacy and opted-in cursors, unrecognized and
repeated headers, redaction, workspace history and revoked sessions in the
real HTTP acceptance. Run full-repository generation and verify the
generated packages. Keep the Nodes contract patch separate.

## Landing order

Land the IAM handler first in a joint commit sequence, then this OpenAPI
and generated-client commit immediately after it in the same fast-forward
update. The preceding IAM commit is the transport prerequisite. Use the
joined revision for validation and availability claims. The plain read
continues to serve the legacy representation throughout. Keep the
generated API and the prepared actor contract out of main until both
owners can land the joined change. AUD-101 and AGT-107 have separate
operator flow and broader attribution acceptance.

## Joined verification

The joint candidate replays the IAM patch unchanged onto the DEV base. An
overlay of the IAM HTTP test validated opted-in responses against the new
OpenAPI and default events against the original closed schema. It also
rejected either missing field and either null field, and confirmed legacy
fallback for an unrecognized value. The underlying IAM test exercised the
actual writer, foreign and mismatched scope, duplicate headers, retained
history, cursor compatibility and revoked sessions.

The enhanced HTTP test, legacy response-schema and authorization tests,
and the workspace repository projection passed on PostgreSQL 18.6 with
networking disabled. `make generate` and the repository-wide generated
consistency check passed on this worktree. The independent Nodes
candidate passed its own generation check and enrollment fixture.
