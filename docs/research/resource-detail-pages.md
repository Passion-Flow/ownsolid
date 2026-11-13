# Resource detail pages

Research behind UX-127. [ADR 0037](../decisions/0037-simple-on-the-surface-deep-underneath.md) leaves open that
"list pages need a real detail view with its own tabs rather than only a drawer", and the build plan's S-105 asks
that a node's Activity show who did what.

## How others solve it

- **WAI-ARIA tabs pattern** ([APG](https://www.w3.org/WAI/ARIA/apg/patterns/tabs/)): a tab list with roving
  focus, the arrow keys moving between tabs, and each panel named by its tab.
- **[The interaction system](../product/design/README.md)**: a list's row opens a drawer, and the detail archetype
  is the page. The drawer is the quick look, the page is where the history lives.

## Choices

- **The drawer stays and links to the page.** It answers "is this the one?" without leaving the list, and the list
  keeps its filters behind it. The page has room for history, and an address that names one node.
- **Three tabs that exist today**: Overview, Tasks and Activity. Metrics and Configuration from S-105 join with the
  modules that hold them. A tab with nothing behind it would be decoration, as ADR 0036 says of unready options.
- **The chosen tab lives in the address**, so a view can be sent to a colleague. Moving between tabs replaces the
  entry, so Back leaves the page rather than walking through tabs, as filters on a list do.
- **Activity reads the audit trail through a new `target_id` filter**, so the page shows what the audit page would
  and needs no permission of its own. The current creation-to-now window is rejected once the resource is older
  than the configured audit maximum; the correction below is planned to preserve access to older events.
  Each row links to the event in the audit trail.
- **An index for it, on the parent table.** Without one, a node with few events walks the whole trail in date order,
  the same shape the task search had ([research](global-search.md)). It is partial, since events with no target
  are never asked for this way, and an index on the parent reaches every partition, now and to come.
- **Actions are the same code in both places**, `useNodeActions`, and the page differs only in what happens after:
  undo is in reach on the page, and removing a node goes back to the list.
- **A tab's data loads when the tab opens.** A person who came for the overview does not pay for the history.

## Which resources

Nodes, people and single sign-on connectors, the lists whose row opened only a drawer. Workspaces and tasks already
had pages. Their parts are shared in `resource-page.tsx`, and each resource's facts and actions sit in one file used
by its drawer and its page. The API has no read of one connector, and an installation has a handful, so that page
finds its connector in the list.

A person has a third tab: **Activity** is what was done to them, and **Their actions** is what they did, through the
`actor_user_id` filter the audit trail already had.

Audit events and email deliveries stay with only a drawer. They are records rather than things with a life, and
everything about one fits there; a page would hold one screen of the same.

Rejected: replacing the drawer with the page, which loses the quick look a list relies on; a page that repeats the
list on the left, which is a layout this console has nowhere else.

## Licences

No new dependency.

## Failure modes and tests

- A node that does not exist, a link with a bad ID: says so, with the way back; browser flow.
- A failed read of the node or of either tab: says so and retries; browser flow.
- A tab name that means nothing in the address falls back to the overview; browser flow.
- Keyboard: arrows, Home and End across the tabs; browser flow, and every state through axe and the tab walk.
- A filter value that is not an ID is refused, and a cursor belongs to its target; Go test.
- The index reaches a partition created later; Go test on PostgreSQL.
- A real node's enrolment shows under Activity and opens in the audit trail; live run.
- A real connector's creation shows under its Activity, and a real administrator's actions under theirs; live runs.

## What the finished item must do

From the Nodes, Users and Single sign-on lists, reach a page of its own with tabs for what it is and who did what.

## Bounded history review

Reviewed 2026-11-13 for the existing UX-127 activity flow. This section defines the
correction; the product implementation and its installed acceptance are still open.

### Existing contract and observed boundary

`ResourceActivity` sends creation minus one minute as `from`, without `to`.
The [server parser](../../services/control/internal/modules/identity/lists.go)
uses server now for the missing end and rejects windows beyond `AuditMaxDays`.
The [typed policy](../../services/control/internal/platform/config/policy.go)
defaults to 31 days, accepts maxima from 1 through 366 days, and requires its
default window not to exceed its maximum. A resource exactly 31 days old already
exceeds the default maximum by one minute. A creation-to-now read therefore cannot
provide reliable lifetime history. No public policy-bounds projection is present
in the current Web/OpenAPI inputs.

A cursor pins the window's start and end, but its filter fingerprint also covers
the original query parameters, including explicit time bounds. The parser can
restore omitted bounds only when the first page omitted them too. After an
explicit first-page window, every subsequent page must send the same `from` and
`to` strings. Otherwise the fingerprint check rejects the cursor before restoring
its window. The client forwards the opaque cursor without inspecting it.
The audit schema, category filter, 13 event fields, server-derived authority and
existing audit-read permission remain unchanged.

### Primary references

- [Grafana time controls](https://grafana.com/docs/grafana/latest/visualizations/dashboards/use-dashboards/)
  distinguish relative and absolute windows, display the active range, and apply
  explicit start/end fields. Its URL parameters carry the selected window.
- [Kibana filtering](https://www.elastic.co/docs/explore-analyze/query-filter/filtering)
  gives a recent default, explicit custom bounds and Apply, with a separate manual
  refresh. These are interaction references; no vendor implementation is copied.
- [TanStack infinite queries](https://tanstack.com/query/latest/docs/framework/react/guides/infinite-queries)
  describe sequential refetch of cached pages. A whole-cache refetch would violate
  the one-request explicit audit recovery requirement.
- [TanStack defaults](https://tanstack.com/query/latest/docs/framework/react/guides/important-defaults)
  describe mount/focus/reconnect refetch and static queries. The pinned version's
  failed-initial-query behavior is separately covered by
  [the audit-read research](identity-audit-read-access.md).
- [Native local date/time fields](https://developer.mozilla.org/en-US/docs/Web/HTML/Reference/Elements/input/datetime-local)
  carry no time zone and may accept nonexistent daylight-saving times. Interpret
  them in the browser's displayed zone, reject normalization by round-tripping
  local components, and serialize valid instants as UTC for the existing API.

### Chosen approach

Keep the resource detail archetype and its current tabs. Start with the last
24 hours, a product default that fits the smallest valid maximum; it is not a
hardcoded deployment policy. Let the person select absolute dates to inspect any
older period. Keep the fields on the tab, apply them with one explicit action,
and put applied bounds in the address. Draft edits do not read audit data.

Store the actual first-page bounds as typed client-only cache metadata beside the
returned audit page. The recent range is snapped when its explicit initial read
starts, and the visible dates describe that cached read. Remounting the tab must
not silently move the window or create a new cache key. Next-page reads send the
cursor, stable resource/person filters and the exact cached first-page bounds.
The shared read hook exposes typed first-page client data to its read callback;
no server response field or cursor interpretation is introduced. A successful
one-page restart replaces both the old
pages and their window metadata; a failed read preserves them together.

Reuse `useAuditRead` rather than a second retry framework. Keep retries, focus,
reconnect, invalidation replay and offline queuing disabled. A deliberate refresh
or recovery sends one request. Preserve server refusal, correlation ID, partial
pages, reader/workspace cache partition and permission-revocation behavior.
The server validates its configured maximum; the UI never guesses it. A refused
valid custom range keeps its fields and offers an actionable shorter-range remedy.

An isolated Node prototype used pinned `@tanstack/react-query` 5.105.3 before
product changes. Eight assertions passed: initial 24-hour bounds; a next-page
fixture read; first-page metadata retained through paging;
remount without a read; unknown response retaining two old pages and their bounds;
invalidation without a read; exactly one explicit recovery request; and successful
recovery replacing one page and its bounds. There were four explicit fixture reads.
This proves the cache mechanism only, not a product, server, browser or SQL run.
The fixture incorrectly allowed omitted next-page bounds. Source review of
`makeScopedListPlan` corrected that assumption before implementation. Acceptance
must enforce the original query fingerprint as well as the pinned window; the
prototype's paging assertion is not evidence for the real cursor protocol.

Rejected: a fixed 31-day policy limit, an unlabelled recent slice, an automatic
multi-window history scan, weakening server validation, parsing the cursor in the
client, pre-clearing cached pages before a restart, and automatic failed-filter
retries. Each loses history, deployment flexibility or explicit-read semantics.

### Licences and offline use

No dependency, model or dataset is added. The existing React and TanStack packages
are MIT, Lucide is ISC, and Playwright is Apache-2.0. Their licences permit
commercial use, redistribution and offline use with their required notices; the
existing notices continue to ship. Native date/time fields and the installation's
API need no remote service, calendar library, font or download.

### States and acceptance

The tab remains part of a detail page; its main action is explicitly reading the
chosen period. Loading retains the period controls and uses a table-shaped
skeleton. A recent window with no events explains that window and offers older
dates. A selected range with no matches names the dates and offers to clear them;
it must use different text. Partial pages retain rows and name the missing page.
Errors retain the chosen fields and give recovery; denied access hides stale
rows and identifies the permission owner. No unready option is introduced; any
existing unavailable tab remains visible with its reason and `aria-disabled`.

Tests must cover an older-than-policy resource with a current event against a
policy-faithful fixture as the old-build negative control; minimum one-day and
default 31-day policy limits; historical absolute dates; reversed/invalid bounds;
out-of-policy refusal and correction; URL reload and tab return; cursor paging
and invalid-cursor restart; lost responses, timeout and explicit retry counts;
filter-cache revisits; revoked permissions and cross-workspace refusal; and
network disconnection. Run EN/ZH, keyboard, screen-reader checks, desktop and
390px, then actually inspect the screenshots. Each explicit action must yield its
single expected read. Real installed consumer acceptance remains required.

### Candidate acceptance boundary

The candidate uses `activity-window.ts` for UTC URL bounds and local date input,
`resource-activity.tsx` for the inline period form, and typed first-page client
context in `useAuditRead`. It adds no API fields, permissions, dependencies or
cursor decoding. Help source and its generated article projection describe the
recent snapshot and historical read flow together.

The browser cases in `resource-activity.spec.ts` use 400-day-old nodes, models,
deployments, accounts and connectors with current events under a one-day policy,
and additionally check the default 31-day policy. Their fixture checks the raw
query fingerprint, so omitting explicit time bounds on subsequent pages fails.
They cover historical ranges, URL/tab restoration, refusal and correction,
draft cancellation, distinct empty states, failed restart, loading and revoked
access in both languages, plus a daylight-saving gap. Shared hook tests cover
first-page metadata replacement, pending-page cancellation and actual cache
expiry. These are prepared acceptance cases, not a claim that they have run.

The screen remains the Detail archetype: existing breadcrumb, name/status,
resource actions and tabs stay in place. Its primary tab action is Read activity;
date changes are an inline form, while failures are persistent inline alerts.
Loading uses a table-shaped skeleton after 300 ms. The recent and selected-period
empty states have different titles and remedies. Partial reads retain rows and
their window. Errors retain dates and a server request ID. Denied access removes
rows and leaves the controls disabled with an administrator remedy. No new
not-ready capability is introduced; existing unavailable detail tabs keep their
visible reason. These are design definitions pending visual acceptance.

Source formatting and Help generation are authoring steps, not passed tests.
Automated, visual and installed acceptance remain unverified for this correction.
Before publication, run the focused unit/browser
cases, the unchanged explicit-recovery suite and resource-page flows, inspect
their screenshots, then complete `make check`, `make build` and `make test` on the
combined source. Installed acceptance must use a newly qualified asset/client
package, not a renamed earlier audit or Help archive.
