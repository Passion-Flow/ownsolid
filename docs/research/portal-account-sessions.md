# Browser sessions on the account page

Reviewed 2026-11-13 for the session-control part of POR-112. The corrected frontend
implementation has scoped browser and visual qualification; full joint-source and
installed qualification remain open.
The [session-management research](session-management.md) owns the server protocol,
authority, audit and real-provider evidence; this note owns the account interface.

## Problem and primary sources

The shared [Account page](../../web/packages/shell/src/account-page.tsx) already
provides profile, password, language, keyboard preferences and linked providers.
Before this implementation it had no way to review or end another browser session.
The generated identity contract already exposed both operations; the implementation
adds their typed convenience functions in `web/packages/api/src/identity.ts`.
There is no reason to invent another service or request tenant/workspace IDs.

- [GitLab Active Sessions](https://docs.gitlab.com/user/profile/active_sessions/)
  places review and targeted revocation under the account. It distinguishes the
  current session from other entries and reserves its current-session action for
  ordinary sign-out. Use that interaction, without copying device descriptions
  or other facts that ownsolid does not return.
- [NIST session management](https://pages.nist.gov/800-63-4/sp800-63b/session/)
  keeps lifetime and invalidation authority on the server and calls for an
  accessible sign-out mechanism. The browser must not extend or approximate the
  server's lifetime with a client timer.
- The [identity contract](../../api/openapi/v1/identity/openapi.json), generated
  types and existing typed transport are the implementation inputs. Its current
  session description and the server research agree on account/entrance scope,
  background reads, bodyless deletion and cookie cleanup.
- The [interaction system](../product/design/README.md) requires a settings
  section, an explicit irreversible decision and persistent actionable errors.
  The current [Account implementation](../../web/packages/shell/src/account-page.tsx)
  establishes the shared Admin/Portal placement and section layout.

## Contract boundary

| Operation | What the interface may use |
| --- | --- |
| `GET /api/v1/me/sessions` | `items`, opaque `next_cursor`, optional configured `limit` and optional `X-Ownsolid-Background: true` |
| `DELETE /api/v1/me/sessions/{session_id}` | Owned session UUID in the path; existing Origin/CSRF transport; no query, body or invented idempotency header; confirmed success is bodyless 204 |

Each row has exactly the published fields: `id`, `current`, `created_at`,
`last_seen_at`, `absolute_expires_at` and `authentication_method` (`password` or
`sso`). There is no device name, browser family, client address, geography,
current-session identifier in `Me`, or total count. Do not infer any of them.
The current marker is a server result, not a comparison with a cookie or token.

Both entrances expose the operations for the current account on that entrance.
The absence of a selected workspace does not hide account security. New-business
license admission does not gate this section. This personal list cannot implement
the separate administrator-wide S-408 destination.

## Chosen interaction

Add one Browser sessions section to the existing Account settings page, shared
by both entrances. Keep profile/password/provider flows intact and independently
usable when the session list fails. Do not add a new navigation route or drawer
for a record with six fields.

The section's main job is ending a selected other session. Show the server's
Current marker, authentication method, creation time, last activity and absolute
expiry using the common formatter. The resource identifier is supporting detail
with Copy. End session on the current row stays visible, `aria-disabled`, with
the reason to use the existing Sign out action instead. Never fabricate a
physical-device label for password or SSO rows.

Selecting another row's End session opens one typed-confirmation dialog. Name
the exact row by its sign-in time and method, show its UUID, and require that
UUID as the confirmation value: this contract has no resource name. Explain that
the target must sign in again and that this cannot be undone. Do not stack a
second dialog or offer a fake Undo. Cancel performs no request. Disable duplicate
submission while the original request is pending.

Keep a manual Refresh and opaque-cursor Load more. An automatic mount read uses
the background option and does not pretend to be a person's activity. No interval
poll, client clock expiry, invented search or client filtering across only one
loaded page is needed for the complete first path.

After a confirmed 204, refresh the session list. A response that discovers the
managing session has expired follows the existing signed-out navigation and
removes the account section. This also covers a cookie/current-session change
while confirmation was open; do not attempt to inspect `Set-Cookie` in JavaScript.
Do not remove a row optimistically before the server confirms the operation.

## Failure and cache behavior

- `STORAGE_COMMIT_ROLLED_BACK` is a confirmed rollback, not success. Keep the
  target and confirmation input, show the request ID, honor Retry-After and offer
  an explicit retry of the same bodyless DELETE.
- `STORAGE_COMMIT_OUTCOME_UNKNOWN` needs a fresh list read before a further
  explicit write. Retain the Problem, close the confirmation and do not automatically replay deletion.
  A bounded page omitting a target does not prove that this request ended it.
  Override a generic full-page Reload remedy with a section-specific read so the
  other Account drafts are not discarded.
- A 404 is not an already-ended success: the server deliberately gives the same
  response for an unknown or foreign target. Keep the Problem and offer a fresh
  list. Only the owned idempotent 204 confirms the requested operation.
- On 401/403, hide the old session rows and confirmation target. Do not offer
  same-authority blind recovery. Keep the permission/sign-in explanation.
- A failed next page retains earlier authorized rows and names the incomplete
  part. INVALID_CURSOR offers an explicit first-page restart. A failed restart
  does not erase the previous list or claim it is empty.
- Partition data by account and entrance, cancel reads on exit and reject late
  results from another view. Never persist the list or credentials in browser
  storage. Changing workspace does not turn this into a workspace-scoped API.

Do not rely on an inaccurate cache-clearing assumption: the current
`createQueryClient` authentication-error handler sets `sessionKey` to null and
navigates; explicit `useSignOut` clears the client, but the error handler does
not clear all cached queries. The new section must discard or withhold old
snapshots across authentication changes and perform a current authorized read
before enabling actions. The outside-product prototype below establishes the
observer/cache boundary; the actual React checks are described below.
Password rotation must invalidate the view's current-marker snapshot as well.

### Cache prototype and selected direction

A standalone prototype used pinned TanStack Query 5.105.3, synthetic rows, no HTTP,
and one process capped at 256 MiB and five seconds. Eight assertions passed across
seven fixture reads. It deliberately retained inactive cache entries indefinitely
so the result could not depend on garbage collection:

- A naive reused account/entrance key returned its old Current marker after the
  simulated authentication handler set the identity to null and the same account
  signed in again. This is a negative control for a proposed hook, not a test of
  an existing session-management interface.
- A fresh per-view key showed no old rows while its own first read was pending;
  a refusal still exposed none. One explicit recovery replaced the marker with
  the new response.
- Leaving cancelled a pending next page. Its late fixture answer could not enter
  the newly mounted view.
- An explicit view reset for credential rotation withheld the prior marker until
  a fresh read. A subsequent 401 hid rows even though the cached page remained.
- Focus, reconnect and invalidation did not replay the failed read.

Use an ephemeral identifier per mounted session section in addition to account
and entrance. Keep it only in memory and never send it as authority or a request
parameter. Reset the section after a confirmed password change and authentication
transition. Inactive pages may be collected immediately in the implementation;
the prototype proves isolation without relying on that collection. Current
authority refusals must also hide the view's rows and selected target.

The prototype does not mount React, exercise StrictMode or prove that the existing
password/sign-in callbacks reset the section. Those integrations, cancellation
during React remount and confirmation-target removal remain implementation tests.
It supplies no server, browser, SQL or durable revocation evidence.

## States and presentation

This remains the Settings archetype: account heading, independent sections and
section-specific actions. Responsive fact rows fit a narrow settings section without a
second resource route or a horizontally scrolling table. Reuse Card, ProblemNotice and
TypedConfirmation; the current marker always carries text. All text belongs in both locale catalogs.

Loading has a delayed row-shaped skeleton. A first successful list containing
only the current entry explains that there are no other sessions. A live list
page emptied by expiry explains that the list changed and offers Refresh; it must
not reuse the first-empty text or erase previously loaded rows. A true filtered
empty state is not claimed in this first path: no filter contract or misleading
page-local search is introduced. Partial, error and denied behavior are specified
above. An older service that does not expose the operations keeps the section and
its unavailable reason visible; do not misreport that service as a current
permission refusal. S-308 now specifies these states; actual screenshot review remains necessary.

## Dependencies, licences and rejected alternatives

No dependency, model or dataset is added. Reuse the pinned React 19.3.1 and
TanStack Query 5.105.3 (MIT), existing Radix UI 1.8.1 (MIT), Lucide React 1.66.0
(ISC), and Playwright 1.64.1 (Apache-2.0) for verification. Their existing notices
permit commercial use, redistribution and offline use. No GitLab/NIST code,
assets or datasets are copied, and no external request is made at runtime.

Reject browser-side-only logout, fabricated device labels, a cross-entrance
list, workspace parameters, a second revocation ledger, a new bulk-delete API,
optimistic revocation, automatic mutation retries, and treating a list absence
as proof of a particular write. Each either invents authority/data, obscures an
uncertain outcome or adds a variant before the targeted path works.

## Acceptance and remaining work

S-308 in the build plan is the specification for this shared Account section, reviewed
against published main `6f785818` on 2026-11-13. S-408 remains the separate future administrator
screen; it is not part of this item. The explicit filtered-empty state is inapplicable because
the session endpoint has no filter. A current-only list and a genuinely empty live list have
different explanations; neither invents a row or a filter.

### Product implementation

The implementation is isolated on `codex/portal-account-sessions`, based on published
`6f785818`. Corrected source `0a50a040` passed the scoped frontend checks and visual review
below, with Web tree `b656b51b998d4e2a23fa9b3b6f2062b60669f282`. Its layout correction is
`6d3d6b8c`. The earlier `763169e3` evidence remains tied to its separate Web tree
`7bab205eb4942575843dfb628f13538ff5265178`. The previous UX-127
source remains on `codex/web-resource-history-ui` at `ce177336` with its own asset and check
records. Neither branch's qualification is transferred to the other.

The shared Account page now contains the session section, typed GET and bodyless 204 DELETE
clients, English/Simplified Chinese text and shared Help. No backend contract, device field,
Admin navigation or S-408 route is added. GET omits limit to use the installed page size.
The transport's existing CSRF retry is explicitly disabled for this DELETE; other operations
keep their previous default. Typed confirmation can remain blocked during Retry-After without
losing the target. Section recovery never requires discarding other Account drafts.

Each mounted view owns a random query key and cancels reads on unmount. A QueryClient-scoped
WeakMap holds an additional memory-only credential generation. SignInPage and the confirmed
PasswordCard callback replace it; useSyncExternalStore delivers that change synchronously,
instead of waiting for a deferred query observer notification. The first integration test of
the deferred version found an old current marker still visible immediately after a same-account
sign-in boundary. The synchronous version passed that test. Neither profile edits nor workspace
changes invent a new credential generation. Late password and revoke callbacks check the view
that started them; they cannot replace a later view's identity. A permission refusal is latched
until a successful first-page read, so a failed recovery cannot reveal old authorized rows.

Shared scripted browser fixtures and both entrances' account-sessions specs now run. They
include bilingual screenshots of the settings section, one confirmation, current-only and
empty-live lists, delayed loading, partial pagination, refusal, unavailable service and narrow
dark layout. A lost reply and 404/unknown commit never replay DELETE. Scripted-browser outcomes
are frontend protocol evidence, not installed server authority or durable revocation evidence.

### Actual focused checks and limits

On 2026-11-13 the existing bounded workstation admission ran these checks serially with a
512 MiB Node heap and one Vitest worker:

- API/session transport: 30 tests passed, including CSRF refusal without replay and empty 204.
- Shell session/Account integration: 31 tests passed, including StrictMode, same-account
  credential-boundary replacement, the actual password form callback, late page/write results,
  sticky denial, invalid cursor, retained history, rollback, Retry-After and duplicate clicks.
- Shared UI components: 33 tests passed, including a typed confirmation blocked by a retry
  prerequisite. Internationalization: 49 passed. Generated Help: 19 passed.
- API, Shell, Console and Portal types passed. Changed-file lint passed in five sequential
  package/application groups; formatter checks passed. The initial combined lint exceeded the
  512 MiB heap. Splitting retained the same rules, cap and all changed TypeScript paths.

The real SignInPage callback test, late-password-after-relogin test and null-recovery regression
first reached an admission refused by resource
preflight at 46,555 free 16 KiB pages (about 728 MiB), below the 2 GiB floor. No formatter,
typecheck, test or browser work child started in that attempt. That refusal remains separate
from the later successful admission; it is never backfilled with a later resource sample.

### Scoped frontend qualification and actual screenshot review

The separately authorized `portal-account-sessions-window-l0wkhyg3` admission measured
407,739 free 16 KiB pages and held the original permanent carrier once. The reviewed absolute
deadline/cleanup parent retained its 600-second total and 30-second cleanup reserve; only the
owned Portal preview cwd binding changed. It finished in 184.011 seconds, exit 0, with process
groups, previews on 31036/31037, wake guard and carrier FD confirmed gone. The permanent regular
carrier remained inode 50313339; no retry admission or resource waiter was created.

The final scoped run passed grouped affected-file lint, Shell types and formatting, 33
Shell/session/Account tests and 34 UI tests. Those include the three formerly unverified
regressions and the generic-retry suppression fix. Both application builds succeeded and
their own asset maps are bound to `763169e3`. Collection selected 11 cases per entrance:
the original nine plus actual same-account re-login and password-form rotation. Both final
selections passed 11/11, with zero skips, retries or flaky outcomes. The first round retained
10/11 passes at each entrance: the new login test navigated away before CSRF completed. Its
trace contained a cancelled CSRF request and no login write. Waiting for the real sign-in
redirect corrected the test; none of its assertions was removed.

The window generated 70 raw PNGs across both rounds. After cleanup, all 36 final-round images
were opened individually; the 34 first-round images remain raw comparisons and were not counted
as read. Actual inspection found:

- **Archetype:** Account is visibly a settings page with independently saved profile/password,
  language, shortcuts and providers, followed by the Sessions section. The loaded desktop rows
  show method, three labeled times, a textual Current session marker and the separate End action.
- **Seven states:** current-only and empty-live copy are visibly different; denied removes rows,
  not-ready leaves a disabled action with a reason, partial keeps earlier rows beside a recovery
  notice, and the one confirmation identifies the irreversible target. Filtered-empty remains
  explicitly inapplicable. The loading and narrow captures missed the session section, so they
  do not qualify its visual states despite the passing DOM/axe assertions.
- **Primary:** Change password remains the Account page's principal action. Ending another
  session is the section-specific secondary action; its single confirmation has one End submit.

The screenshot failure matters: main scrolls independently of the document. Changing viewport
left the page at an unrelated setting, so the 390px/360px and re-login captures did not show the
session target; some full-page empty/partial images also clipped its lower edge. The shortcut
heading was squeezed beside a long explanation: three English lines and a vertical Chinese
column. Candidate `6d3d6b8c` moves that explanation below the heading and captures the actual
session section, with separate page context, 1440px, shortcut and narrow-dialog views. That
correction required the separate qualification below; the earlier green tests were not transferred.

### Corrected source and visual qualification

The separately authorized `portal-account-sessions-visual-6tebiwnq` entry froze
`0a50a0405b4841e8117d40ce65b000a2344ceaea` and Web tree
`b656b51b998d4e2a23fa9b3b6f2062b60669f282`. Its own preflight measured 391,025 free 16 KiB
pages, with more than 15 GiB disk free. The original carrier was acquired once. The same reviewed
absolute-deadline parent, unchanged 512 MiB heap cap, single concurrent worker and per-stage
bounds applied. It exited 0 in 74.826 seconds; process groups, previews, wake guard and FD were
confirmed gone before any image was opened. No extra admission, queue or background waiter ran.

Only the two changed Shell inputs were formatted/linted; the formatter made no changes.
Shell types, 33 Account/session tests and 34 applicable UI tests passed. Both exact-source
application builds passed. Each entrance collected and passed all 11 account-sessions cases,
with zero skips, retries or flaky outcomes. No unchanged API, i18n or Help suite was rerun to
inflate the count. That entry preceded composition with the later DEV-generated contracts;
its checks and screenshots do not qualify the joint source below.

This entry generated 56 raw PNGs and all 56 were individually opened after cleanup. Actual
review confirms:

- **Settings archetype:** separate full-page context shows the Account title and independently
  saved sections. The 1280px and 1440px target captures show the complete session card, with
  labeled facts, textual current marker, disabled current action and its Sign out explanation.
- **States:** the delayed loading images now contain both row skeletons. Current-only and
  empty-live captures show their distinct explanations and complete refresh action. Partial
  pagination retains the earlier row and names the incomplete read; denied contains no rows;
  service-not-ready retains the unavailable action and reason. Filtered-empty is explicitly
  inapplicable because the endpoint supplies no filter.
- **Primary:** Change password remains the main Account action; End session belongs to its
  section. Desktop and mobile confirmations show one irreversible decision, the exact target,
  focused typed field, Cancel and a blocked End until confirmed. Escape dismissal and axe passed.
- **Narrow layout:** 390px light and 360px dark captures contain the actual session section in
  both languages. Facts stack without horizontal overflow, long IDs wrap, and shortcut headings
  stay on one line with the explanation underneath. Re-login images show the new current ID;
  password rotation shows one current row, with the existing transient success toast visible.

The scripted single-sign-on row only verifies the authentication-method label. It does not
qualify a real provider session. Previous input/failed-run packages and the 70/36 generated/read
counts stay unchanged; they are not merged with this entry's 56/56 counts.

The local scripted entrance flows cover EN/ZH, keyboard, axe, desktop and narrow-width views.
An installed flow must still create two real same-account sessions,
end the peer from the UI, prove the peer is rejected while the manager remains
usable, and independently verify the single durable revocation audit. Preserve
cross-account, cross-entrance, no-workspace and license-blocked acceptance. Use a
real SSO session for the sourced-session case; the backend HTTP fixture alone is
not rendered-browser acceptance.

The corrected source is qualified for this scoped frontend path. `make check && make build
&& make test`, disconnected-network installation and the installed
authority/audit/real-SSO flow remain unqualified. No previous UX-127, Help or audit asset is
relabelled as this implementation. POR-112 remains open. Both scoped frontend opportunities
are consumed and cleaned up. The separate joint full-entry refusal below does not extend
their qualification. Publish no fully qualified release or roadmap tick before the full and
installed requirements and later same-source composition checks hold.

### Original-author joint source and full-entry boundary

Joint candidate `9e7404f63fe7587ace0decb34fd01b6214977181` has exactly two parents:
the frontend/document source `379aab980485df41b1a66c6fb5e6af65814f886f` and DEV's
original-author `2e64ab4c6c16bd84ae5c101b9de5444550760ff2`. The candidate tree is
`6c5938e198745062ba5a14e1f952cb4593797ccd`; its Web tree is
`8ce0c9946a8403a5d4502b54e4337a754de38bfc`. It preserves `6d3d6b8c`'s visual correction.
The exact missing bundle prerequisites were imported from the author's local object store
before bundle verification. No file patch or replacement author history was substituted.

Composition review verified all 19 author delivery paths, all 122 external generated SDK
files and all five protected Web consumer/helper files against their frozen manifests.
All 79 projection inputs match the author's source. In the joint source, 77 remain equal;
the two Account Help source articles retain the frontend branch's exact bytes. The merge
also inherits four original-author SAML/group-mapping research and decision paths through
its real ancestry. There were no merge conflicts or manual changes to DEV files. Both
owners' status and research facts remain. The original 430-operation compatibility audit
still has its explicit 409 source-review signal and actual exit 1; it was not rerun or
converted to a successful full-gate result.

The separately authorized `portal-account-joint-full-4deb4qpz` entry admitted this exact clean
candidate through one nonblocking acquisition of the existing permanent regular carrier,
inode 50313339. The reviewed deadline/cleanup guard retained one 1800-second absolute budget
with 120 seconds reserved for cleanup. Its prepared original targets were `make web-install`,
`make check`, `make build` and `make test`, with no Makefile overlay or changed test selection.
The prepared limits were Node old-space at most 4096 MiB while preserving stricter inherited
limits, Go package parallelism one and GOMAXPROCS at most two, Cargo jobs one, and serial
workspace/browser/test workers. These are prepared controls, not evidence that work ran.

Actual entry exited 1 after 0.072 seconds: `vm_stat` reported only 22,958 free 16 KiB pages
(about 0.350 GiB), below the required 6 GiB. It refused before any work child, wake guard,
scratch volume, install, check, build or test target started. The separate disk readback was
about 154 GiB free; it does not replace the failed live memory sample. Cleanup was confirmed,
the parent exited, both preview ports were empty, the carrier FD was closed and its inode
was preserved. The consumed entry is not queued or automatically retried.

This is a resource refusal, not a product/test failure or a passing gate. No joint assets,
browser cases, skips, retries, Linux/native run or installed acceptance were produced. The
earlier 1233-file scoped frontend archive stays bound to `0a50a040` and its own Web tree;
it is not a joint-source release artifact. Mac cache-only tooling would not establish a
Linux network namespace or a disconnected installed flow. The later strict-JSON generator
candidate is not part of `2e64ab4c`, this composition or any qualification claimed here.

The next full-source attempt needs a new explicit resource opportunity and a fresh live
sample satisfying both memory and disk floors. DEV must first publish its own exact source;
the frontend lane then combines actual published main and reviews the true affected changes
before publication. Even a passing joint full gate would leave real peer revocation,
independent SQL audit, disconnected installation and real SSO UI acceptance open for POR-112.

### Current full-gate admission prerequisites

After the workstation update, the new authorized full opportunity was prepared
from exact frozen source `82c602d32b2d957459b3a742499de96867da27a9`, tree
`fbbf65a10a349d43726a355e60fdb171837c97d5`, and Web tree
`8ce0c9946a8403a5d4502b54e4337a754de38bfc`. All 5099 tracked files were hashed
with a clean source. The original unsplit ESLint command remained; the separately
unqualified `56a19799` tooling and the later strict-JSON generator were excluded.
The original DEV `2e64ab4c` ancestry and frontend `379aab98`/`6d3d6b8c` inputs remain.

Read-only reconciliation found only the prior terminal resource refusal: its
work child had never started, cleanup was confirmed and its parent was gone.
No newer owned full-gate instance or result existed, and neither preview port
had a listener. The required permanent carrier, previously regular and owned by
UID 501 at inode 50313339, was now absent (`lstat` ENOENT). Its identity cannot
be verified by recreating the path, and no replacement or alternative lock was
created. The cause of the disappearance was not established.

A separate fresh read-only sample at 2026-11-13 10:54:59 UTC measured 361,013
free 16 KiB pages (5.509 GiB) and 156.342 GiB disk free. It does not reuse the
coordinator's earlier larger sample or replace an actual admission preflight.
It also falls below this full profile's unchanged 6 GiB memory floor; the disk
floor remains 30 GiB. The intended entry keeps 1800 seconds including at least
120 seconds of cleanup, the inherited stricter Node cap or at most 4096 MiB,
Go package parallelism one/GOMAXPROCS at most two, Cargo jobs one and the
original complete targets, offline caches and worker controls.

No entry or nonblocking acquisition was attempted: no descriptor, work child,
scratch volume, service, test, new asset or negative fixture was created.
`web-install`, `check`, `build`, `test`, cases/skips/retries and asset qualification
are all NOTRUN. No retry or resource waiter followed. The frozen joint and
serial-lint branches remain unchanged; this record lives on a separate
documentation branch. The prior refusals, original 430-operation audit exit 1
and scoped frontend evidence remain separate. Full work needs the authorized
shared carrier identity restored or explicitly superseded through coordination,
then a fresh sample satisfying the same resource floors before one admission.
POR-112's installed revocation, independent SQL audit, offline and SSO acceptance
remain open regardless of future full-gate results.
