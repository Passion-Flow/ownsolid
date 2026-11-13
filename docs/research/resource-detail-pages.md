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
At `314a133a`, focused lint, Console/i18n typechecks, 22 Console tests for date
validation and explicit audit reads, and 22 i18n formatter/catalog tests passed.
This is not browser, visual, full/native or installed acceptance. Before
publication, run the browser cases, the unchanged explicit-recovery suite,
node and resource-page flows, inspect
their screenshots, then complete `make check`, `make build` and `make test` on the
combined source. Installed acceptance must use a newly qualified asset/client
package, not a renamed earlier audit or Help archive.

### Source-bound browser prerequisite

At `43eaa0cb`, actual Playwright discovery found 21 resource-window cases, all
41 original recovery cases in disjoint 19/19/3 partitions, 9 resource-page cases,
10 node-page cases and 30 affected accessibility/keyboard inventory cases. The
accurate Console build passed and contained both the date parameters and cached
window metadata. Its 696-file asset map has SHA-256
`31ecbbae60314b2cc5a6ae229cc1efeafc802734df85ac1e29d9993842fee9e0`.

The bounded operation then failed during collection of the external old-runtime
negative probe: its directory had no package manifest, so Playwright loaded the
entry as CommonJS and refused `import.meta` in the shared fixture imports. This
is a harness collection failure, not the expected old-runtime policy refusal.
The actual guard exit was 1 after 15.756 seconds, with cleanup confirmed. Zero
negative or positive browser cases ran and zero screenshots were generated or
inspected. These discovery/build results do not qualify the UI or its installed
consumer.

External probes must declare the same explicit ESM boundary as the Console.
[Node's package rules](https://nodejs.org/api/packages.html#type) and
[TypeScript module selection](https://nodejs.org/api/typescript.html#determining-module-system)
describe that boundary. The pinned Playwright 1.64.1 `fileIsModule` helper returned
false for the original external entry and true for both the Console entry and a
new probe copy with `package.json` declaring `type: module`. The test and fixture
bytes remain unchanged. That isolated classification check passed; collection
and browser execution were still unrun at that prerequisite boundary. The
subsequent execution below retains that original failed operation and the
separate nonblocking-busy outcome without relabelling either as browser evidence.

### Cursor transport regression and visual acceptance boundary

The bounded Chromium execution on source `ce177336` used Web tree
`0c70c6e25706594d17de574e1bae2f5989855a10`. Its Console build passed in
7.652 seconds; all 696 asset hashes matched the earlier frozen runtime map.
The corrected external ESM probe actually collected and ran four old-runtime
cases. All four failed at the unchanged policy row assertion (expected two rows,
received zero), with no collection errors, skips, retries or flaky results. This
is the intended negative control, not a positive product pass.

The positive resource-window file actually ran 21 cases: 17 passed and four
failed, with no skips or retries. In both languages, next-page recovery expected
41 rows but retained 21, and returning after a cancelled page expected 26 rows
but retained 21. The tab and hook retained the successful first page's exact
bounds, but `web/packages/api/src/audit.ts` omitted explicit `from` and `to`
whenever a cursor was present. The policy-faithful fixture correctly refused the
changed query fingerprint. No test assertion, fixture or cursor protocol was
weakened.

`f655af2b` preserves explicitly supplied bounds on cursor requests. `3f614d69`
adds two transport regression cases: first-page and cursor query fingerprints
must match, and cursor-only callers must not acquire invented time bounds.
This follows the existing server contract described above and introduces no
dependency, API field, permission or licence change. These two tests and the
corrected source have **not run** through unit tests, typechecks, lint, a new
build or browser acceptance. The existing 696 built assets still represent
`ce177336`; they cannot qualify the corrected Web tree
`da6882b8d6d0510323fd9ed50d080c8ca72876eb`.

The remaining 90 planned positive browser cases did not run: the original
41-case explicit-recovery suite, 19 resource/node-page cases, and 30 affected
accessibility/keyboard cases. The private review runner also omitted the owned
API source directory from its permitted correction paths. No supplemental
executor or rerun started. Its absolute work cutoff expired while awaiting
review; the parent then recorded a `PermissionError` during deadline handling,
exit 1 after 540.278 seconds, and confirmed cleanup. A later read-only check found neither
known parent/worker PID nor a listener on the two owned preview ports. Retain the
cutoff and wrapper error as failures; this is not a clean successful guard run.

#### Screenshot review

The reporter embedded 25 PNG bodies in its JSON. The initial file-only collector
reported zero images because those bodies were not separate PNG files. All 25
were extracted without changing their bytes, hashed, opened and visually read.
The original zero-image collector record remains intact. Two planned EN/ZH
partial-page screenshots were never generated because pagination assertions
failed. There are no screenshots of the corrected source.

The actual images show five resources in English at desktop width and Simplified
Chinese at 390 pixels, plus recent-empty, refused-range, historical,
filtered-empty, loading, error and denied states in both languages, and one
English invalid-local-date state. They establish the following review result:

- **Archetype:** Detail, with breadcrumb/back link, name and textual status,
  resource actions and an Activity tab. Node headers show four actions and
  deployment headers six, exceeding the required two most-used visible actions.
  The overall detail pages therefore fail visual acceptance.
- **Seven states:** loading, the two different empty states, error and denied are
  visible. Recent-empty offers earlier dates; filtered-empty explains the selected
  period and offers to clear it. Partial has prepared assertions but its two
  images are missing. Existing unready deployment actions remain visible with
  reasons, but those reasons expose roadmap IDs rather than useful product guidance.
  The Chinese filtered-empty image ends below the viewport before its remedy;
  that image alone does not establish the remedy's keyboard or scroll access.
- **One primary action:** the tab's intended action is Read activity. Node and
  model pages also emphasize Collect facts or Deploy a model, creating competing
  primary buttons in both desktop and narrow views. Connector and account images
  show only the activity primary. Statuses include words as well as colour.

Keep UX-127 open. Qualification requires the two new API tests, affected types
and lint, a source-bound Console rebuild, the original 21 resource-window and
41 recovery cases, resource/node pages, accessibility and keyboard coverage,
new partial screenshots and actual review of every generated image. Fix the
observed header hierarchy and unavailable-action explanations before accepting
the detail screens. Full `make check`, `make build`, `make test`, native and
installed SQL/authority/offline/SSO acceptance remain separate requirements.

### Detail action hierarchy correction

The existing [WAI-ARIA menu-button pattern](https://www.w3.org/WAI/ARIA/apg/patterns/menu-button/)
keeps secondary commands under a named button, with Enter/Space, arrow navigation,
Escape and focus return. Reuse the existing Radix-based Menu and MenuItem rather
than building another focus manager. Radix is MIT; no dependency, model or dataset
is added, and the existing commercial redistribution and offline notices apply.

Keep one frequent node command beside More actions; put Rename, Disable/Enable
and destructive Remove in that menu. Keep node drawers' existing layout and the
same mutation, undo, permission and typed-confirmation paths. Deployment pages
keep Start when available, otherwise Check, beside More actions. The menu retains
verification, stopping, removal, scaling and rollback with their original guards.
Unavailable commands stay in the command set, aria-disabled with a visible reason;
replace roadmap-ID explanations with what this release can do. On Activity, header
commands are secondary so Read activity is the sole primary. The model Deploy
link becomes secondary only on Activity. No capability or confirmation is removed.

Reject hiding unavailable commands, removing resource actions or replacing typed
confirmation with direct destructive writes. Original node/deployment browser
flows must open the menu before the same mutation assertions; add EN/ZH desktop
and narrow keyboard/menu coverage and screenshots. Check menu-to-dialog focus,
Escape return, disabled explanations, all original audit paging/recovery assertions,
and the seven existing activity states. Candidate qualification remains pending.

### Directed action and cursor qualification

The executed candidate is `1a2344ca`, including the unchanged cursor transport
correction and the new detail action hierarchy. API unit tests passed 43 cases
(including both cursor regressions), affected Console tests passed 30, and the two
i18n files passed 22. API and i18n typechecks passed. The Console typecheck/build
passed and produced a new 697-file asset set. Its source-bound asset manifest has
SHA-256 `6c2f186f379ceaed64ecfa118ec9232e8e5fcc0aa978721dfb9b58afcd90b569`.
This is a new candidate build, not the earlier 696-file runtime.

The combined focused lint process exhausted its unchanged 512 MiB heap and exited
with signal 6. Serial grouping retained every requested path and every rule:
Console source and browser inputs passed; API lint found two unused parameters
in the new test mocks. The correction at `25e33516` makes those mocks assert the
requested audit path. No changed lint rules, excluded failures or new skips were
used. The corrected mocks have not run again.

Actual browser results, all with one worker and zero retries or skips:

| File or partition | Passed | Failed |
| --- | ---: | ---: |
| Resource activity | 21 | 0 |
| Original recovery, English | 19 | 0 |
| Original recovery, Simplified Chinese | 19 | 0 |
| Original recovery boundaries | 3 | 0 |
| Resource and node pages | 19 | 0 |
| Affected accessibility inventory | 20 | 0 |
| Affected keyboard inventory | 10 | 0 |
| Existing service flows | 23 | 0 |
| New bilingual desktop/narrow action cases | 0 | 4 |

All 134 original/affected cases passed. Each of the four new action cases reached
its final assertion: Escape closed Rename but did not return focus to More
actions. `25e33516` restores focus after native dialog cleanup and stacks narrow
detail headers so names no longer compete with commands for width. The original
assertions remain intact. A later executor refused the already-expired work
cutoff before starting any Node, build or browser command; these corrections are
therefore unverified. `4c4928b9` additionally applies the existing disabled cursor
and opacity styles to unavailable menu items; it is also unverified.

All 61 actual screenshots were extracted, hashed, opened and visually read:
29 resource-window images, 12 original recovery/Help images and 20 action images.
Both formerly missing partial images now show retained rows, the cached window,
the cause and explicit recovery. A second scrolled filtered-empty image shows
the Chinese Clear dates remedy. Recent and selected-period empty messages differ.
The intended Detail structure and one Activity primary are visible. No latest-fix
screenshots exist. Remaining visual qualification includes the narrow heading
correction, disabled menu styling and the selected English Activity tab initially
lying outside the visible portion of the horizontal tab strip.

The single admitted operation retained its original absolute deadline and carrier.
Its parent exited 1 after 540.310 seconds, with the work cutoff and a deadline-
handling PermissionError preserved, followed by confirmed owned cleanup. The
additional directed process group ran while the original guard held its descriptor;
its kernel checks verified that exact parent descriptor before/after each command.
It did not inherit that descriptor, which is recorded separately in the private
evidence. This is component execution evidence, not a successful full guard gate.
Post-exit checks found no known worker PID or owned preview listener. The four
unchanged old-runtime negative failures remain earlier evidence and were not rerun.

Keep UX-127 and POR-112 open. Freeze and validate the latest fixes, including API
lint/unit assertions, detail-header types/build, original node/service mutation
flows, menu/dialog focus and EN/ZH desktop/narrow screenshots. Recheck any affected
inventory and active-tab visibility. The full make gates and real installed
SQL/authority/offline/SSO consumer acceptance remain unqualified.
