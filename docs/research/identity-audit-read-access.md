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

## Explicit Web read recovery

Reviewed 2026-11-13 against the implemented identity reader and TanStack Query
5.105.3. Both the installation list and resource activity call the existing
`GET /api/v1/admin/audit-events`. A successful read also appends an access event.
Loss of the response cannot establish whether that append committed. The current
server's uncertain transaction outcome is HTTP 409 with
`STORAGE_COMMIT_OUTCOME_UNKNOWN`, no page and no Retry-After. The standard thirteen
event fields and `category` filter remain unchanged. DEV owns the public contract
annotation; this consumer must not import an unqualified candidate projection.

### Sources and selected approach

- [TanStack query options](https://tanstack.com/query/latest/docs/framework/react/reference/functions/useQuery)
  document retry, retry-on-mount, window focus, reconnect and network-mode controls.
  The installed source confirms that `networkMode: always` does not queue a read
  until connectivity returns. Disable retries and every background refetch trigger
  for this audited endpoint only; retain the application's normal policy elsewhere.
- [TanStack infinite queries](https://tanstack.com/query/latest/docs/framework/react/guides/infinite-queries)
  refetch every cached page sequentially. A recovery action must instead retry the
  failed next cursor once, or read only the first page when restarting an invalid
  cursor. The pinned QueryClient `infiniteQuery` implementation accepts `pages: 1`;
  failed fetches retain previous data until a successful replacement arrives.
- [HTTP semantics, section 9.2](https://www.rfc-editor.org/rfc/rfc9110.html#section-9.2)
  permit incidental logging for safe methods. GET alone is not evidence that the
  access record was absent after response loss. This product deliberately requires
  a person to start another audit read, including after an uncertain GET result.
- The primary product implementation is `identity/audit_read.go`, its HTTP
  dispatcher and the real SQL/wire integration tests. Each explicit successful
  read has a new server request ID and access event. Browser fixtures can establish
  consumer dispatch counts, not durable commit outcomes or actual SQL authority.

Use one shared Console query policy and recovery notice for the two consumers.
Do not treat the existing 409 as automatically retried: `ApiProblem.retryable`
includes status 0/429/502/503/504, not 409. The actual regression is automatic
retry after network/timeout (status 0), plus implicit refetches. Mark these cached
pages as manually refreshed, disable retry-on-mount/focus/reconnect/polling, and
execute each chosen request immediately rather than queuing it offline.
The pinned Query `isStatic` check returns false until data exists. Therefore an
initial failed query must additionally become ineligible for automatic execution;
otherwise invalidation refetches it despite static stale time. Imperative manual
recovery remains available. Retain the failing invalidation control and verify
the fix without changing the global scheduler or removing that assertion.

An initial error replaces loading with an inline Problem. An uncertain result
explains that the read may already have been recorded and that Read again starts
a new request. Never call it a rollback, an absent access or an empty list. Keep
the server Problem and request ID available. A partial pagination failure keeps
previous pages, says they are the pages already read and offers one explicit
recovery action. Busy recovery cannot start a second overlapping request.
Retry-After remains enforced by the shared Problem notice.

A current 403/404 refusal removes all cached event rows and any open event preview
from the rendered consumer; it offers no retry against the same denied authority.
Discard inactive cached audit filters/resource views on that refusal, retaining
the active failure so remounting cannot retry it. A different filter must not
resurface events cached before access was refused. Other query families remain.
Bind snapshots to the authenticated session's reader, audience, tenant and
Workspace in the cache key; none is sent as a new API input. A later sign-in or
Workspace change cannot reuse a previous reader's successful cached page.
Session-expiry handling remains with the existing session owner. When page reload,
deliberate filter change or route navigation needs another read, it is user
initiated. Merely focusing a tab, reconnecting, remounting a failed cached query or
invalidating cached data must not issue another read. No background status poll
or new server operation is introduced.

Rejected: changing the global retry policy (unrelated consumers need it), assuming
409 already retries, relabeling network loss as a definitely failed append,
generic whole-page reload recovery that discards reader context, an ordinary
infinite-query refetch that re-reads every cached page, silently
discarding older pages before a replacement succeeds, and fabricating a new
Problem or exact-action enum. Keep ordinary initial/filtered empty text distinct.

### Dependencies, failure checks and acceptance

No dependency, model or dataset is added. Existing React and TanStack Query are
MIT, Lucide is ISC and Playwright is Apache-2.0. Commercial use, redistribution
and offline execution remain allowed with their existing notices. All production
assets and messages remain local to the installation.

Cover both actual UI consumers in English and Simplified Chinese: a response lost
after a fixture has observed the request, 409 commit-unknown, network refusal,
configured transport timeout, transient 503, and malformed response. Count all
attempts including failed ones. Wait beyond the old retry delay, exercise actual
focus/visibility and offline/reconnect events, and remount a cached failed query;
none may silently issue another read. Explicit recovery increments the count by
exactly one. Recover a failed next cursor without re-reading previous pages;
recover an invalid cursor by one first-page request and replace history only on
success. Cover permission revocation after a loaded page and an open preview,
the Portal refusal, fresh page reload and keyboard activation. Retain request IDs,
verify no `action` parameter and read desktop/narrow screenshots. Add the new
uncertain and partial states to the existing accessibility/localization inventory.
Update the existing built-in audit article in both languages and verify its new
recovery section can be found through a settled filtered Help search.

Finished consumer behavior requires targeted checks plus a new exact-source full
repository gate. The older 1,134/410 browser result is not evidence for this
change. Actual installed access accounting and independent SQL remain separate;
AUD-108 and installed acceptance stay open until their owners' requirements hold.

### Consumer states and focused checks

| State | Audit list and resource Activity |
| --- | --- |
| Loading | Retain the list-shaped initial skeleton; recovery keeps its notice/old rows and names the in-flight read. |
| First-time empty | A successful empty trail says nothing was recorded in the window; Activity says no activity yet. |
| Filtered empty | The trail says no events match and offers Clear filters, distinct from its first empty. An Activity detail has no collection filter. |
| Partial | Keep every page already read, identify the result as incomplete and offer one failed-cursor recovery. |
| Error | An unknown response is not an empty list or a rollback. Explain the possible recorded read, retain correlation and require a new explicit request. |
| Denied | Hide current rows/preview, discard inactive audited views and offer no same-authority recovery. Cache keys separate current readers and scopes. |
| Not ready | Busy recovery remains visible as Reading; Retry-After keeps its countdown and disabled action. No implicit deferred read follows reconnection. |

The list retains title/filter/table and its existing preview drawer. Resource
Activity retains its detail header/tabs/table; neither gains a new surface.
The immediate recovery action is Read again, or Read from the start for an
invalid cursor. The existing Help article remains reference detail: a built-in
first empty is inapplicable, and a filtered index has No article matches.
Its immediate task is finding the recovery answer.

Focused Mac arm64 checks passed make check, the Console production build,
54 Console unit tests, 54 UI tests, 49 locale tests and 19 Help tests.
Chromium passed 70 consumer flows, 12 new accessibility/keyboard checks and
four new localization checks. The two Help flows also passed a second check
through the settled search and native section link. No case skipped, retried
or became flaky. Eighteen desktop/narrow screenshots were opened, including
the full recovery section after scrolling; long IDs stay within the notice
and partial tables retain their contained horizontal scroll.

The old c864fbb1 production assets remained unchanged during the controlled
response-loss comparison. A single opening plus focus/reconnect, with no
manual retry, produced six requests in each consumer. Both controls failed
the expected one-request assertion. This establishes dispatch behavior, not
six durable database appends. Raw reports and the exact old asset manifest
remain separate from the new passing browser result.

The first check rejected untyped test mocks/assertion callbacks; the next unit
run caught the real no-data invalidation hole plus four assertions observing
React before its query notification arrived. Typed mocks, settled state checks
and the explicit failed-query eligibility guard fixed them without weakening
an assertion or the shared scheduler. Both failed runs remain retained.
The complete exact-source repository gate and actual installed/SQL acceptance
are separate from these focused results; AUD-108 remains open.
