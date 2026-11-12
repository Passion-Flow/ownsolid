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

### Exact workspace consumer admission

SCI's `8531226d` adds the [scoped append contract](audit-diagnostic-access.md#scoped-append-contract)
and real PostgreSQL qualification without changing the installation or
assistant writers. Pair it with `ListWorkspaceAuditForActor` inside the existing
current-actor read transaction. Resolve the installed tenant and actual local
site/cell from the database; never use `Auth.Me.Workspace`, request parameters
or default scope constants as attribution. Retain the independent installed-
workspace authorization and disabled-workspace history policy.

After reading the page, append `audit.events_read` with the current human Admin,
exact authorized workspace and only the validated limit, returned count and
assistant projection choice. Use a new canonical server UUID for a Go call
without HTTP correlation. Propagate every scoped append, cancellation and
commit error, clearing the page, cursor and assistant map. Reading before
appending excludes the operation's own event. The existing unscoped HTTP route
continues to use installation attribution; this composition adds no workspace
route or client-selectable tenant.

The paired tests must call that actual identity method with a non-default
workspace and independently read the committed event. Preserve restricted-role
and assistant projection checks, foreign/missing workspace equivalence, forged
context and revoked/current session refusal, bounded selection and protected
field exclusion. The assistant pagination check fixes its existing `to` filter to the database
clock before any read-access append, preserving its original source window
without adding a public category or audience filter. Test append
refusal/repair, cancellation while the actual INSERT is blocked, concurrent
reads and both real COMMIT wire-loss modes. A scoped writer test alone does
not qualify this consumer, and this note claims no such execution yet.

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

## Qualification

The identity consumer passed repository checks, builds and all test stages with standard toolchain `umask 022`. Browser regressions reuse byte-identical Web, OpenAPI, generator, capability and Makefile inputs already qualified with 1,075 Console and 389 Portal Chromium cases; 37 installation-dependent cases were skipped. The test continuation used `make -o test-web-browser test`; no assertion was removed.

Seven actual PostgreSQL HTTPS roots cover the standard and assistant views, empty results, safe correlation and projection, append refusal and repair, real member/Portal/revoked-session refusals, rejected tenant/workspace inputs, concurrent reads, cancellation, existing assistant attribution and four actual wire COMMIT uncertainty subcases. COMMIT-not-forwarded and suppressed server ACK both release no page and perform no automatic second append; an explicit retry records a new read.

The existing workspace projection root also passed with a restricted principal that can read/append audit rows and resolve only the required local-cell columns, while remaining unable to read the private chain head. This confirms compatibility and authority, not workspace access accounting. The first seed-deadline, cookie-clearing and missing cell-column fixture failures are retained separately from their corrected passing runs.

Fixtures used PostgreSQL 18.6 and pgvector 0.8.7 in owned network-none Linux arm64 containers on the workstation. No native Linux amd64 installation or new rendered UI acceptance is claimed. The qualified runtime/test input is `fcbb839af30f6df6b7b6dd60bbf60c61442bf973`; the durable local evidence set is `aud108-identity-20261112.vi6mouqa`, including exact input, raw native logs, wire observations and full-gate exit codes. Fixtures and the owned scratch volume were cleaned up. Only this verification text changed after the gate.

AUD-108 remains open for SCI scoped-workspace accounting, Web action/family names, DEV public contract metadata and Delivery collector admission/reconciliation.

## Web action and family display

The existing S-407 audit list and preview drawer remain the consumer. Display
`audit.events_read` as "Audit trail read" and add the audit family to the existing
Kind of action selector. Keep the machine action in the drawer and keep using
`category=audit`; do not invent an exact-action query value or change the thirteen
public fields. The existing action-name fallback still handles future unnamed
actions without treating a module identifier as prose.

The [OWASP logging guidance](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html)
keeps event identity and access control with the producing application. The
[WCAG language guidance](https://www.w3.org/WAI/WCAG22/Understanding/language-of-parts.html)
allows technical identifiers while requiring readable interface language. Localized
names plus an explicit raw identifier in details preserve both needs. This uses
only the existing React (MIT), TanStack (MIT), Lucide (ISC) and ownsolid localization
code. Commercial distribution and offline use remain allowed with their notices;
no dependency, model, dataset, network resource or licence term is added.

Consumer checks must cover English and Simplified Chinese action names, the audit
family filter sent as `category` (without an `action` query), sharing/reloading that
filter, the retained machine identifier in the drawer, a filtered empty and a
failed read with explicit retry. Existing initial empty, partial pagination,
permission and Portal-refusal behavior remains unchanged. Register a representative
audit-read page state for the existing keyboard, accessibility and localization
checks. Read desktop and narrow screenshots before recording the Web slice.
This does not qualify the actual installed append, scoped workspace accounting,
collector admission or DEV's exact-action enum. AUD-108 stays open.

The focused Web consumer checks passed `make check`, both app type checks,
49 locale tests, the Console production build, 20 audit Chromium cases
with one unchanged conditional skip, and four new page-state checks.
English desktop and 390-pixel Simplified Chinese screenshots were opened:
the list keeps its title/filter/table structure and the preview drawer keeps
the original event and request identifiers. The seven state boundaries stay
in the existing audit page: layout-shaped loading, initial no-records,
filtered no-match with Clear filters, retained partial pages, retryable
read failure, denied read, and unavailable operations with reasons. Initial
and filtered empty copy remains distinct. This slice adds no surface or
primary action; reading an event remains the immediate task, with its
request/person follow-up in the preview. Installed append/SQL acceptance
is not claimed by the scripted browser checks. The complete integrated gate passed
at 6a77bcdbc372a0aa7fac24b3f21a83105c246c86;
see the [shared Web gate](model-import-cleanup.md#complete-web-consumer-gate).
This retains the exact-action and thirteen-field boundaries; installed AUD-108
acceptance is still separate.
