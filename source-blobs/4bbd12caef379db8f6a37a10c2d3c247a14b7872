# In-product help and API reference

Research for UX-111 and POR-113: help and API documentation that every installation serves itself,
matching its own version on an air-gapped network.

## How others do it

- **GitLab** serves a copy of its documentation at `/help` on every self-managed instance,
  matching that instance's version; requests map onto the Markdown files of its `doc/` directory
  ([GitLab /help](https://docs.gitlab.com/development/documentation/help/)). Its own issue tracker
  records why the copy cannot be dropped: air-gapped customers depend on it, and the online site's
  search makes a web request per query, so it does not work offline
  ([issue 214164](https://gitlab.com/gitlab-org/gitlab/-/issues/214164)).
- **Harbor** serves a Swagger UI of its REST API from the instance itself, reached from the
  navigation, calling the API with the signed-in user's rights
  ([API explorer](https://goharbor.io/docs/working-with-projects/using-api-explorer/)).
- **The console's error catalog** (UX-107, [research](errors-and-empty-states.md)) already explains
  every code an API call declares, in both languages; an API reference should reuse it rather than
  write the explanations twice.

The lesson in both: the help that ships is the help for the version installed, it is reachable from
the product's own navigation, and nothing about it needs the internet, including search.

## The approach

- **Help pages at `/help` in each entrance**, written for that entrance's reader: the console's for
  people who run the installation, the portal's for people who use it. Articles are Markdown in
  `web/packages/help/locales/<language>/<entrance>/`, one file per article and language, since
  translated product text lives only in `locales/` (AGENTS.md section 7); an article about a page
  both entrances have is written once, in `shared/`. `articles.json` orders them, gives each a
  topic and names the pages whose help link opens it.
- **Parsed at build time, rendered with the design system.** `tools/codegen/web-help.mjs`, run by
  `make generate` and checked by `make check`, turns each article into an mdast tree with
  `mdast-util-from-markdown` and the GFM table extension; it refuses raw HTML, images, headings
  deeper than three levels and any link that is not a page path or `help:<article>`, so no article
  can point outside the installation. The page renders
  the tree with the design system's own components (headings, code blocks, tables, links). No HTML
  string reaches the page, so there is no sanitizer to get wrong and the Content-Security-Policy
  stays as it is. An article loads with its route, under the bundle budget.
- **Search offline.** The help index (titles, headings, a summary) is a `SearchSource` of the
  command menu and the help page's filter, matched in the browser.
- **Context.** The top bar's help button opens the article that names the longest prefix of the
  current path, else the help page; on a phone, where the top bar has no room, help is in the user
  menu. A link to a page or article that does not exist fails a test, as navigation targets do.
- **API reference at `/help/api`**, built from `api/openapi/v1` at build time and filtered to the
  entrance's audience (`x-audiences`), so the portal never lists the console's paths. Each
  operation shows its method and path, parameters, request body and response fields, and its
  declared error codes with the explanation the error catalog already gives. The contract itself
  can be saved as a file, filtered to the operations this entrance serves. It is a reference, not a
  console: the page sends nothing to the API.
- **What it leaves out, and why.** Every operation declares its entrance, and the generator fails
  on one that does not. Most inherited operations declare no `x-error-codes`; the reference says
  so on each such operation instead of guessing, until UX-131 declares them. The inherited
  knowledge contract is never listed: knowledge belongs to DeepParser (AGENTS.md
  section 1). Operation IDs repeat across contracts, so an operation's anchor is
  `<contract>.<operationId>`.

### Rejected

- **Swagger UI, Redoc or Scalar bundled.** All are permissively licensed, but each brings its own
  look, a large bundle and inline styles the installation's Content-Security-Policy refuses, and
  none knows the error catalog. The contracts are plain OpenAPI 3.1 JSON; rendering the parts a
  reader needs with the design system is smaller and consistent.
- **A runtime Markdown renderer or HTML strings.** Parsing in the browser costs bundle size for
  nothing, and HTML strings need sanitizing. The tree is made once, at build time.
- **Linking to an online documentation site.** It fails in exactly the networks this product is for.

## Licences

| Dependency | Licence | Use |
| --- | --- | --- |
| `mdast-util-from-markdown` 2.1.0 and its micromark dependencies | MIT | build time only |
| `mdast-util-gfm-table` 2.0.0, `micromark-extension-gfm-table` 2.1.2 | MIT | build time only |
| `@types/mdast` | MIT | types |

Nothing is downloaded at runtime; the installation serves the built files. `mdast-util-gfm-table`
2.0.1 was four days old at the time of choosing, so 2.0.0 is pinned.

## Failure modes and tests

- An article missing in one language, or a link to a page or article that does not exist: a unit
  test fails the build's tests.
- An operation without `x-audiences`, or an audience leaking into the other entrance: a unit test
  over the generated reference.
- A code with no explanation: already enforced by the error catalog's coverage test.
- A narrow portal screen can reach its own article from the account menu, including on the Models
  and API keys pages. Both header layouts fall back to the help index on a route without an article.
- Search with no match says so and offers to clear it; both languages; axe in both themes;
  keyboard; the bundle budget.
- Offline: the live run checks that no request leaves the installation.

### Alias cutover recovery copy (GW-111)

Use the existing bilingual `problem.<code>.title` and `detail` entries.
[RFC 9457](https://www.rfc-editor.org/rfc/rfc9457.html#section-3.1.4)
describes the detail as recovery guidance. The
[cutover contract](gateway-cutover-preview.md#public-contract-and-client-acceptance)
and the preview and apply validators define five distinct 409 conflicts.
Pause before changing the target, refresh when already paused or already
enabled, wait for held calls to release capacity, and use the recorded
alias target history to choose a matching deployment for rollback.
These responses have no Retry-After. Treating all five as a generic
retry would lose the change needed to proceed.

Web owns the two message catalogs. The API contract owner publishes the
codes in the versioned `x-error-codes` index, which supplies the existing
Help renderer. The downloadable contract already describes the conflicts.
Use the existing catalog and Help tests, and check the production Problem
status and headers offline. This adds no dependency, model or dataset.

A loopback HTTPS probe of the production Problem writer returned all
five codes as 409 with a correlation ID and without Retry-After.
The bilingual catalog and existing Help unit tests passed.

### Dataset tokenizer recovery copy (DAT-107)

The Admin and Portal
[preview contract](../../api/openapi/v1/datasets/openapi.json) describes
eight dataset-specific problems. The scoped 404 calls for selecting an
accessible dataset and draft. A 409 calls for verified files. Both 422
responses require rows or validation corrections before another request.
The four 503 problems carry the server's wait hint. For time and memory
limits, use a smaller sample or source after that delay. For repeated
storage or result failures, ask the administrator to check the owning
service. The existing 429 guidance already covers the busy preview slot.
See the [dispatcher and client qualification](training-tokenizer-preview.md)
and the [HTTP Retry-After definition](https://www.rfc-editor.org/rfc/rfc9110.html#name-retry-after).

The loopback HTTPS Problem probe verified the redacted body, absent
retry hints on 404, 409 and 422, and the configured hint on 503. It also
checked that the typed 429 hint takes precedence over the policy default.
Keep the shared text applicable to other dataset operations. A new screen
or a preview-specific catalog would duplicate the same problem semantics.
The contract owner supplies the versioned Help index entries. This adds
no dependency, model or dataset.

### Bilingual error review

The existing Help browser suite passed in Admin and Portal. For visual
review, a scratch browser fixture added only the expected status-to-code
groups to the loaded Help index in memory. The running pages then showed
all thirteen titles and recovery details in both languages, including
the narrow layout. The fixture observed zero requests outside the local
origin. Its index overlay was confined to the test browser. The
versioned metadata and generated files remain the contract owner's
publication boundary.

## What the finished item does

Both entrances have `/help` with articles for their reader, searchable from the page and the command
menu, a help link on each page that has an article, and `/help/api` listing every operation that
entrance serves with its fields and explained errors, in both languages, with nothing fetched from
outside the installation.

### Portal visual acceptance (POR-113, S-107)

The help index is a reference list: title, search, then topic groups and links. The primary action
is to find an answer using search. Its articles ship with the build, so a newly installed portal
already has help. The filtered empty state names the query and offers Clear search. There are no
workspace permissions or partially loaded topics on the index. Sign-in gates the signed-in frame.

An article is a reference detail: the link back to all help, the title, and the article's sections.
The primary action is to read the answer, with links to the pages where it applies. The article
keeps the authored introduction and its links, and the index uses the plain-text summary. The route
can load before the article chunk has arrived, so the summary gives context while the body is
loading or when it cannot be loaded. An unknown slug shows a missing-article notice
with a way back. Activity and resource actions do not apply to these versioned help articles.
No feature on either screen is selectable while unavailable. There are no new dependencies.

The Playwright preview ports can be offset together when local worktrees test concurrently.
Playwright requires an available web server URL and the preview command must bind that same
port ([web server configuration](https://playwright.dev/docs/test-webserver)). The default ports
are unchanged. A worktree must run its own preview, since reusing another lane's process can
test the wrong build.

## Contract path references in the reference test

The [OpenAPI Path Item Object](https://spec.openapis.org/oas/v3.1.1.html#path-item-object)
can refer to a path defined in a different contract. The Nodes contract
retains node exporter bookmarks and client paths through references to
the canonical Components definitions. The Web generators share
`tools/codegen/openapi-paths.mjs`, which checks the owner and exact
pointer before resolving a path item. Iterating raw Nodes entries
mistakes a reference for an operation without an audience.

Read effective paths through that same resolver in the reference test.
Keep the expected audience-filtered set derived from source contracts
and compare it to the separately generated public reference. Avoid a
second resolver with a different list of supported references. The
test uses the workspace's pinned Node runtime, whose
[synchronous ESM loading](https://nodejs.org/api/modules.html#loading-ecmascript-modules-using-require)
can read the existing resolver. A malformed reference must fail the
test, and any extra or missing operation in either entrance must still
fail the set comparison. There is no runtime dependency or UI change.

## Versioned preview problem index (GW-111, DAT-107)

The Web catalogs now supply titles and recovery details for all thirteen
codes in English and Simplified Chinese. Use the existing per-operation
`x-error-codes` [OpenAPI extension](https://spec.openapis.org/oas/v3.1.1.html#specification-extensions)
as the source of the generated Help list. The runtime
[RFC 9457 Problem](https://www.rfc-editor.org/rfc/rfc9457.html#section-3)
responses remain the source of the codes, status and headers. Copying
localized strings into the contract would create a second catalog.
Adding the codes to unrelated operations would describe failures those
operations have not qualified.

Restore the five existing 409 codes to the Admin alias cutover preview
index. Add the eight dataset codes to both registered tokenizer preview
indices under 404, 409, 422 and 503. Keep existing generic codes and the
downloadable responses. The established SDK, Web and Chromium test
[licences](sdk.md#licenses) apply. No dependency, model or data set is
added.

Verify each indexed code against its released response group and the two
catalog entries. Regenerate the offline Help projections and verify
the real pages open the three operations with localized titles and
details. Check that Portal has only its authorized operation index
and that the wire contract apart from this metadata stays byte-equivalent
when parsed.

The Linux amd64 run built the real Console and Portal with the generated
index and no browser-side index overlay. Chromium opened all three
operations directly in both languages at a 390-pixel viewport. Each
listed status, title and detail matched its released mapping. Portal
excluded the Admin cutover preview. The local fixture made no request
outside its loopback origin. The 19 Help tests and projection
regression passed, and parsing the pre-change and final OpenAPI
confirmed that only the three error indices changed.

## Error declarations on the published operation

An [OpenAPI specification extension](https://spec.openapis.org/oas/v3.1.1.html#specification-extensions)
can attach structured metadata to an operation. The source of truth for
S-108 is each effective contract operation's status-to-code map.
Compare those maps to the generated details, keyed by contract, HTTP
method and path in each entrance. The existing locale check then covers
the exact set of codes readers can see. A missing, extra, moved or
misclassified code must fail even if all locale entries exist.
Re-running generation alone cannot detect an error dropped consistently
by the generator.

For browser coverage, open the published change-password operation by
its shareable anchor and assert the 403 status, title and recovery text
in the same error row in English and Simplified Chinese. The
[Playwright locator assertions](https://playwright.dev/docs/test-assertions)
wait for the generated detail and locale chunks to load. Keep the real
generated index in the browser. Do not supply a test error list or
replace the loaded chunk. The alias cutover and tokenizer preview
checks can use this same path when their specific codes are published
by the API contract owner. This adds no dependency, model or dataset.

The built reference in both entrances rendered the declared password
error's status, title and recovery guidance in both languages in local
Chromium, with zero requests outside the preview origin. The
focused preview checks read the five alias conflict codes on
`adminPreviewAliasCutover` and the eight dataset codes on both
`adminPreviewDatasetTokenizer` and `portalPreviewDatasetTokenizer`.
The separate `adminCutoverAlias` operation calls the same cutover
planner but still has no published error map.

The same visual run exposed an independent parameter gap. The Identity
contract declares `#/components/parameters/Csrf` on `changePassword`.
Both generated details instead have an unnamed parameter with no
location, and the page shows `help.api.in.undefined`. The generator
must resolve Parameter Object references before deciding what to
project. Preserve the current policy of omitting browser-managed header
and cookie parameters from the on-screen table while retaining the
reference in the downloadable contract. Verify that `changePassword`
has no empty parameter row and that a referenced path or query parameter
retains its real location, name, schema and required flag.

### Published preview acceptance on the Web development Mac

Built both entrances after syncing `7d0d9e79` and opened each operation
through its shareable address. Six local Chromium cases checked the
status, title and recovery detail in the same error row in English and
Simplified Chinese. Admin showed all five 409 conflicts on the alias
preview. Both tokenizer previews showed all eight dataset codes under
404, 409, 422 and 503. A Portal search for the Admin preview returned the
filtered empty state. The browser loaded the published index as built,
with zero requests outside the local preview origin. The 390-pixel
screenshots retained the code and recovery text, including the four
503 recovery instructions. The Help and locale suites passed against
the same generated index.

### Monitoring and native transaction recovery copy (OBS-109)

The full fleet-history contract publishes eleven monitoring-specific
problems and three native transaction problems that still lack locale
entries. Add their titles and recovery details to the existing two
catalogs. The shared Problem component and generated Help both consume
these keys. The [RFC 9457 detail guidance](https://www.rfc-editor.org/rfc/rfc9457.html#section-3.1.4)
calls for actionable recovery information. Preserve the codes and HTTP
statuses supplied by the server.

Review the messages against the protected-policy loader, the bounded
history reader and the native transaction error adapter. A range can be
too short after alignment to the evaluation grid. Its recovery must allow
adjusting its duration. Missing source samples can yield a successful empty
history and must not be described as this range error. Cancellation,
deadlock and lock timeout ask the reader to refresh current state before
retrying. The [PostgreSQL error codes](https://www.postgresql.org/docs/18/errcodes-appendix.html)
and [deadlock handling](https://www.postgresql.org/docs/18/explicit-locking.html#LOCKING-DEADLOCKS)
describe the underlying distinctions. Keep the existing timeout and
rolled-back transaction entries.

Check all fourteen codes for both title/detail entries, equal keys and
placeholders across languages, and the Help consumer's locale coverage.
The publication owner combines these two locale files with the complete
operation and verifies the final generated index. This adds no
dependency, model or dataset. The existing locale and Help dependencies
and their licences apply.

The two final catalogs passed the 49 locale tests, 19 Help tests and
locale type checking. A separate read of the full Admin fleet-history
contract found all 28 declared codes explained in each language. The
catalog diff adds exactly 28 keys per language and preserves every
existing value. The two-file handoff is independent of the pending
Portal scope and parameter-reference browser candidates. Final
publication and live history verification belong to the combined
backend, contract and locale revision.

### Consumer checks after the parameter-reference repair

The published generator repair resolves local Parameter Object references
before it omits browser-managed headers and cookies. The generated
change-password operation now has an empty parameter array. Its Web
consumer should show the localized No parameters message and no parameter
table. The [OpenAPI Parameter Object](https://spec.openapis.org/oas/v3.1.1.html#parameter-object)
also permits referenced path and query parameters. Check a workspace
detail's referenced workspace_id in both entrances for its real name,
localized Path location, required flag and UUID schema. This prevents an
empty-state workaround from hiding every referenced parameter.

The released Admin apply-cutover operation now declares the five
qualified 409 conflicts. Open that exact operation through its shareable
anchor and check each status, title and recovery detail in English and
Simplified Chinese. These checks load the actual generated details.
The existing generator regression tests retain ownership of reference
resolution and source validation. The Web assertions cover what the
reader sees after the locale and detail chunks arrive. Use the existing
Playwright and Help dependencies and their documented licences. No
dependency, model or dataset is added.

The initial consumer run found that searching for getAdminWorkspace also
returns getAdminWorkspaceOperation. Match the complete operation ID at
the end of the button's accessible name before checking its expanded
state. Keep the referenced parameter assertions on the exact detail
panel; a broader search result must not change which contract is checked.

The corrected consumer passed all 13 Console and 17 Portal Help cases in
local Chromium with one worker and no retries, at Web revision 5d595469.
Its complete Web tree matches the frozen combination 1389de28. The ten
attached screenshots were read: both entrances retain the referenced
workspace parameter's name, required flag, localized Path and UUID type;
both password details show No parameters without an empty table; the
Admin apply operation explains all five qualified 409 conflicts in both
languages. At 390 pixels the page remains within the viewport, with the
parameter table's additional notes in its horizontal scroll area. These
are built-reference checks with mocked authentication and zero requests
outside each preview origin. Real installed login and bilingual
acceptance remain separate.

The S-108 archetype is a reference list with inline details: title and
search, contract groups, then an operation expanded in its surroundings.
Its primary action is to find an operation. No dialog or drawer applies.
The state boundaries of that reference are:

| State | Behavior |
| --- | --- |
| Loading | The opened operation keeps its summary and shows detail-shaped skeletons. |
| First-time empty | A fresh installation already includes its versioned reference; no user-created resource is needed to fill it. |
| Filtered empty | No operation matches names the query and offers Clear search. |
| Partial | A detail chunk failure leaves the other operation summaries available. |
| Error | A failed operation detail states that it did not load and offers a retry. |
| Denied | The signed-in frame gates access and the build projects the entrance's audience. Reading documentation does not grant permission to execute an operation. |
| Not ready | This is a reference without an API executor. A missing error declaration retains its explicit explanation rather than inventing codes. |

### Contract download discovery during a connection failure

The contract loader currently imports a separate JavaScript module just
to obtain each file's URL. If that module cannot load, Save the contract
has no address and disappears. The address itself is static build data.
[Vite glob imports](https://vite.dev/guide/features#glob-import) support
eager default imports with a `?url` query. Include those URL strings in
the reference module while keeping the JSON files as separate same-origin
assets. The existing zero-inline asset policy still applies. Read this
constant address synchronously in the reference header. The private Web
helper retains its name, entrance projection and null result for an
unknown contract; its only runtime consumer no longer needs a query.

[TanStack Query's network modes](https://tanstack.com/query/latest/docs/framework/react/guides/network-mode)
explain a second failure: its default online query pauses while offline,
even if its result needs no network. Changing only the import would leave
the header paused. An always-running query could avoid that pause but
still adds asynchronous state and a cache for a constant build value.

Reject fetching every full contract in advance: finding a download
address does not require reading its content. A second manually maintained
URL map could drift from the generated entrance projection. Reloading
the entire page to repair a tiny address lookup would discard the reader's
place without removing this unnecessary request.

Use [Playwright's offline context](https://playwright.dev/docs/api/class-browsercontext#browser-context-set-offline)
to open the already loaded reference, interrupt its connection, then
filter to a contract not previously displayed. Its download link must
remain available. Restore the connection and save the actual file;
verify the contract's audience, rather than replacing its response.
Also refuse one file download, retain the link, and retry it successfully.
The initial and changed bundle sizes must stay within the current budget.
Retain screenshots and a controlled run against the old loader so a
passing download after recovery cannot hide a missing-link regression.

This uses the existing pinned Vite (MIT) and Playwright (Apache-2.0).
Both permit commercial use, redistribution and offline execution with
their licence notices. No dependency, model or dataset is added. The
frozen installed acceptance source remains independent of this follow-up
until the new implementation has its own validation.

In the first Chromium run, the intended page-route abort did not refuse
the native download: failure() returned null. That run is not evidence
of a failed download. Inject the failure in the same preview server's
HTTP response using Vite's
[configurePreviewServer middleware hook](https://vite.dev/guide/api-plugin#configurepreviewserver).
A cookie belonging to the isolated test browser names the one JSON
asset to refuse. Return 503 with no-store while that cookie is present;
clear it before retrying. Check the real 503 response, the native
download failure and the recovered file's audience. Parallel contexts
have separate cookies and never rename or replace shared build assets.
The hook applies only to the local preview server; the installed Control
asset server is unaffected.

### Installed entrance boundary

The frozen [Identity dispatcher](../../services/control/internal/modules/identity/http/handler.go)
refuses Admin paths on Portal before dispatch. The
[Workspace handler](../../services/control/internal/modules/workspaces/http/handler.go)
also refuses Portal workspace paths on Admin. Verify both directions with a real signed-in account, its
server-supplied default workspace and a successful read through the
correct entrance. The same known workspace through the other entrance's
path must return the real 404 Problem, no-store and a matching request
ID. An invented workspace ID could return 404 even without the entrance
check, so it is not a sufficient positive control.

In each language, search the installed reference for the other entrance's
workspace operation. Show the filtered empty state and its Clear search
action, then recover to the allowed operation. Keep these real-authentication
checks separate from the preview suite and retain the frozen product
revision when extending the private acceptance client. No dependency,
model or dataset is added.

### Download recovery qualification

At source b49514bf6b34689e754533c86ec5a5d65d10749a, make check and
both Web production builds passed. Four local Chromium cases, one per
entrance and language, passed without skips, retries or unexpected
results. Each retained the previously unseen contract's download link
while offline, observed the real preview HTTP 503 with no-store and a
failed native download, then cleared its own fault cookie and downloaded
the actual audience-projected JSON. The Help article link and keyboard
focus walk passed. No HTTP request left the local preview origin.

The eight screenshots were read at desktop English and 390-pixel
Simplified Chinese widths. S-108 retains its reference-list structure
and inline details; finding an operation remains its primary action,
with saving a contract secondary. Its seven state boundaries are listed
above; interruption no longer removes the download address. S-107
retains the reference-detail structure of back link, title, contents and
sections. Reading an answer is its primary action. Its bundled initial
content, unknown-article fallback, local loading and retry, signed-in
gate and independent sections retain the existing state boundaries.
First-time content and an unsuccessful search have distinct meanings.
Both new language sections fit their viewports and their API links lead
to the reference. No dialog or drawer is needed for either screen.

The raw reports are retained outside Git in the native-503-st2hx3ct
qualification directory, with source and exit records. The eight images
are retained under help-contract-discovery/qualified-b49514bf in the
session's visualization directory. These are local preview results with
mocked authentication, not installed-server acceptance. The earlier
page-route attempt remains a failed fault-injection result and is not
counted as download-failure coverage. Actual installed login, entrance
boundaries and workspace isolation remain open until a new same-source
installation is available.


## API reference filter restoration

Reviewed 2026-11-13 for the shared UX-111 / POR-113 browser flow. MODEL reported
Portal failures at `684f07ef` on `db7ee24a` and `b4e222a7` on `d0ab719a`:
the article's API-reference link was expected to end at `/help/api`, but the
observed final address included `q=changePassword`. A focused pass or the later
complete `c70e9f59` Web pass does not erase either failed full invocation. Their
native reports and traces remain with the original owner. A new source must be
checked independently rather than presumed to fail or pass.

### Product intent and source evidence

The [list design](console-list-pattern.md#shareable-links-ux-114) makes an explicit
list query authoritative. A destination without list parameters restores this
browser's remembered screen and writes it into the address. The current
[useListView](../../web/packages/shell/src/list/use-list-view.ts) implements that
choice in its restoration effect; `linksList` also recognizes an explicitly empty
`q` as a supplied filter. API reference uses this same list behavior. The real
getting-started articles link to `/help/api` without a filter.

The existing download-recovery test changes the API search to `changePassword`
before following that article link. Expecting an unfiltered final URL contradicts
the remembered-filter behavior. This is a source-based diagnosis until the seeded
browser control runs. React's [effect reference](https://react.dev/reference/react/useEffect)
describes synchronization after a commit, including possible paint before an
effect. Playwright's [URL assertion](https://playwright.dev/docs/api/class-pageassertions#page-assertions-to-have-url)
waits for its predicate to match; a bare URL alone is not evidence that restored
filter state, input and content have settled together.

### Chosen verification and rejected changes

Preserve remembered filters. Explicitly establish and verify this account's stored
`help-api` query through the existing search UI before leaving the reference.
Follow the actual article link, then assert the restored input, exact query and
matching operation, including absence of the operation excluded by that filter.
Open the real operation, reload it, and verify the query, anchor, expanded state
and published detail. A different explicit operation link must override the prior
remembered query. An explicit Clear search must empty the input and query while
showing both operations; a later bare article link must then retain that empty
state. Run both entrances and both languages using their normal isolated contexts.

Keep an old-assertion control that waits for the restored input before checking
the former bare URL. Record its exact source, test patch, output, exit and trace;
expectation failures are not browser-runner failures. Keep all original offline,
503/no-store, failed native download, retry, audience and keyboard checks.

Reject changing production restoration to discard remembered filters, accepting
any final URL, clearing global storage, disabling concurrency, retrying cases or
adding sleeps. These change product intent or mask the transition without proving
the final screen. Runtime code and generated inputs need no change if the seeded
control confirms the diagnosis. The correction belongs in the shared assertion.

### Licences, failure coverage and completion

No dependency, model or dataset is added. Existing React/TanStack MIT and
Playwright Apache-2.0 licences permit commercial use, redistribution and offline
execution with their required notices. The test uses the current built reference
and isolated preview control plane, never a replacement API index or external site.

A missing saved query must fail before article navigation. A transient bare URL
must not satisfy the final result check. Stale content, a wrong operation anchor,
a lost filter on reload, a remembered query overriding an explicit link, or an
explicit clear being ignored must fail their own assertions. EN desktop and ZH
390px retain screenshots for inspection. Completion requires accurate-source
focused results, then the required complete gate in a separately qualified window.
New installation-client checks and actual installed acceptance remain separate.


### Seeded restoration qualification

The two Portal controls at candidate `2b983dbf75feaf36f6ac3423f4d1b0a1ecf0fc52`
first established the saved `changePassword` query through the real search UI,
then waited for that value in the input after the actual article link. Both
languages rejected the former bare-URL assertion with the observed
`/help/api?q=changePassword`. Each failed only that expectation, without a case
retry. The one-line negative patch, source/file hashes, raw exits and both native
traces remain separate from the corrected results. This confirms the assertion
contradicted restoration; it does not claim every unchanged old invocation must
fail on every source or erase either reported MODEL failure.

The corrected source passed four cases: EN and ZH in each entrance, with the two
locale contexts running concurrently. All original offline discovery, real
503/no-store, failed native download, recovered audience-projected file,
keyboard and no-external-request checks remained. Final query/input/content,
operation click and reload, an overriding explicit operation link, and a persisted
clear followed by the article link all passed. No skips, flaky results, retries,
fixed sleeps, global storage resets or production restoration edits were used.

The correction was published at `44ab52293e516128ba2efc0edc2cb2053acc3462`.
All Web/API/fixture/generator/capability/Makefile inputs match the focused candidate.
Only the reviewed shared test helper differs from the prior 8dee production base,
so its 1,230 qualified asset bytes remain reusable. The source Web tree includes
test files and is now `d9da502cbd488c5576a73c909c2717621e677996`; this is not a
claim that the old full tree or an old e5c/be62 package has been rebound.

All twelve screenshots were actually read at English desktop and ZH 390px.
The reference retains its back link, heading, search and matching operation card;
the path wraps inside the narrow card, and the contract remains discoverable.
Article pictures retain the real scroll position of the API link. No image was
cropped, overlaid or edited. The reference-list/inline-detail and article-detail
archetypes, existing seven-state boundaries and their distinct filtered recovery
remain; finding an operation or explanation is primary, saving a contract secondary.

A new code-only installation client bound to that exact published source passed
TypeScript no-emit, eighteen input guards, four output-preservation guards and
one GW120/Help/Scope discovery each. Those selftests created no browser and used
no real server or credential; no old run, cookie, session or storage was copied.
The actual image, live schema/table proof, ready bytes and protected runtime
fixtures are still supplied by DEL. A complete gate with the corrected shared
helper and actual new installed browser/independent SQL acceptance remain open.
The prior c70 complete pass remains prior evidence, not a new-source full result.
