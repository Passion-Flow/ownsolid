# Browser sessions on the account page

Reviewed 2026-11-13 for the session-control part of POR-112. This is a frontend
research and interaction proposal, not an implemented or visually accepted page.
The [session-management research](session-management.md) owns the server protocol,
authority, audit and real-provider evidence; this note owns the account interface.

## Problem and primary sources

The shared [Account page](../../web/packages/shell/src/account-page.tsx) already
provides profile, password, language, keyboard preferences and linked providers.
It has no way to review or end another browser session. The generated identity
contract already exposes both operations, but
`web/packages/api/src/identity.ts` has no typed convenience functions for them.
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
  explicit write. Preserve the target and do not automatically replay deletion.
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
observer/cache boundary; React lifecycle integration still needs its own checks.
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
section-specific actions. Use the existing table, StatusPill, ProblemNotice and
TypedConfirmation. All text belongs in both locale catalogs; statuses carry words.

Loading has a delayed table-shaped skeleton. A first successful list containing
only the current entry explains that there are no other sessions. A live list
page emptied by expiry explains that the list changed and offers Refresh; it must
not reuse the first-empty text or erase previously loaded rows. A true filtered
empty state is not claimed in this first path: no filter contract or misleading
page-local search is introduced. Partial, error and denied behavior are specified
above. An older service that does not expose the operations keeps the section and
its unavailable reason visible; do not misreport that service as a current
permission refusal. These definitions still need final screen specification and
actual screenshot review before implementation is called done.

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

Add typed client functions and transport tests for the existing GET and bodyless
204 DELETE, then the shared account section and bilingual Help. Test list loading,
both empty contexts, paging, invalid cursor, rollback, lost reply, unknown commit,
Retry-After, duplicate clicks, cancellation and denied access. Exercise same-account
relogin and password rotation so old Current markers cannot be reused.

Both entrances need EN/ZH, keyboard, axe, desktop and 390px screenshots that are
actually opened. An installed flow must create two real same-account sessions,
end the peer from the UI, prove the peer is rejected while the manager remains
usable, and independently verify the single durable revocation audit. Preserve
cross-account, cross-entrance, no-workspace and license-blocked acceptance. Use a
real SSO session for the sourced-session case; the backend HTTP fixture alone is
not rendered-browser acceptance.

The public operations and observer/cache prototype are ready inputs. React
integration, implementation, browser/visual checks, full gates and installed
acceptance have not run for this frontend slice. POR-112 remains open. Shared
browser/full/install resources and
the existing specification boundary remain explicit; this research adds no UI
shell or support claim while those checks are unavailable.
