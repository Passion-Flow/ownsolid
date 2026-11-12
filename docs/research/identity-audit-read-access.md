# Identity audit-read access (AUD-108)

## Consumer and primary sources

The existing public consumer is `GET /api/v1/admin/audit-events`, through
`identity/http.Handler.listAuditEvents` and `Service.listForActor`. It already
rechecks the current Admin session, reads a bounded allowlisted page and can
add verified assistant attribution. The SCI consumer review at
`e15269e221b949ea5091a6545cf0da489739ad47` identified its missing access append.
This note specifies that identity consumer, not the support collectors or a
replacement audit writer.

[OWASP logging guidance](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html)
places security event capture with the application that knows the operation
and actor, protects log reads, and excludes credentials from event payloads.
[Troubleshoot redactors](https://troubleshoot.sh/docs/redact/redactors/)
separate permitted collection from disclosure. Apply those boundaries with the
existing identity projection and transactional audit append. Do not create a
second receipt store or copy original result rows into an access event.

The [current identity contract](../../api/openapi/v1/identity/openapi.json)
permits module action strings in returned events, while its exact `action`
query accepts an enumerated subset. Preserve that subset until DEV changes
the public contract; the existing `category` filter can select the new audit
family. The standard and assistant representations keep their fields, cursor
domains and existing header negotiation.

## Chosen approach

Append a fixed `audit.events_read` success event in `listForActor`'s actual
transaction after current-session authorization and the bounded read, before
returning any page. Read the page before appending so it cannot contain its own
access event. Retain the existing timestamp window and lookahead semantics.
An append error, cancellation or uncertain commit releases no page or assistant
attribution. A later explicit read gets a new access event; it does not replay
the old append or prove that the earlier read never occurred.

Carry the HTTP handler's server-generated request ID through an additive
identity operation used by the real handler. Existing authenticated Go callers
also reach the common read transaction; calls without an HTTP correlation use
the writer's fresh operational UUID. Neither path accepts an actor, tenant or
workspace from query parameters. The human initiating the read remains the
audit actor even when the selected result representation discloses assistants.

For the existing installation-wide platform Admin view, use `WriteWithin`
with the installation target and only bounded selection facts: validated page
limit, returned count and selected representation. Its own installation/default
workspace attribution describes that installation operation. Copy no raw query,
cursor, original reason, result row, email filter, identifier digest, protected
metadata or driver error into the access event.

The explicit workspace Go view has a different scope. SCI owns the scoped
append contract required to attribute it to the actual authorized workspace.
The default-workspace writer cannot establish that coverage. Keep that
integration boundary visible until the actual scoped writer and consumer are
qualified together. The existing published HTTP view is installation-wide and
intentionally includes other managed tenants; a foreign workspace selection
through the workspace view still fails its server-resolved tenant check.

Preserve the existing thirteen-field allowlist and verified assistant pair.
Protected audit metadata and identifier digests are not response fields.
Operator reasons and actor/subject addresses remain intentionally visible under
the current authorized representation. Do not claim arbitrary sensitive-text
redaction or change that representation without DEV's public contract policy.

## Rejected alternatives and integration boundaries

An independent transaction can commit a receipt while the read transaction
fails, or expose a page whose audit append failed. A post-response logger is
too late to enforce access accounting. A configurable opt-out would weaken a
security invariant. A diagnostic helper that the HTTP consumer never calls
would leave the actual exposure unchanged. None is selected.

SCI retains the audit-module implementation and the exact workspace writer.
Web owns English and Simplified Chinese names for `audit.events_read` and its
audit family. DEV owns any addition to the exact-action query enumeration and
explicit endpoint documentation for the existing commit-unknown Problem.
Installer access admission and Control-down reconciliation remain with
Delivery. These owner boundaries keep AUD-108 open after an identity slice.

## Failure coverage and acceptance

Use actual PostgreSQL and verified HTTPS through the real existing route.
Verify ordinary and assistant representations, matching server request IDs,
the current human actor, bounded selection facts and independently read-back
audit rows. Seed protected metadata/digest sentinels and verify their absence
from pages, Problems and new access events. Keep existing operator-visible
fields and assistant verification as positive controls.

An audit insert refusal must return no protected page and permit a new explicit
read after repair. Actual COMMIT-not-forwarded and suppressed-ACK faults must
release no page, preserve the observed durable outcome and issue no automatic
second append. Use the existing isolated PostgreSQL wire-fault fixture, not a
simulated error after a successful commit.

Cover revoked sessions, Portal/member callers, forged actor context, rejected
tenant/workspace query fields, known foreign workspace IDs, malformed cursors,
cancellation, bounded concurrent reads and explicit retry. Compare pagination
and assistant attribution using their existing filter/window contracts.
No source-only or standalone append check establishes this acceptance.

## Dependencies and licensing

No dependency, model or dataset is added. The existing Go standard library is
BSD-3-Clause, pgx is MIT and PostgreSQL uses the PostgreSQL License. Their
commercial use, redistribution and offline operation remain permitted with
the existing notices. OWASP and Troubleshoot are references, not embedded code.
The complete item additionally requires scoped workspace accounting, the
public/UI owner changes and actual collector acceptance; keep its roadmap
box unticked until those paths meet their acceptance criteria.
