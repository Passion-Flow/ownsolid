# Portal home

Research for UX-102, POR-101 and screen S-301: the first page a person who *uses* the platform
sees, scoped to their own workspace ([ADR 0035](../decisions/0035-what-belongs-in-each-entrance.md)).

## How others do it

- **Backstage** ships a [home page plugin](https://backstage.io/docs/getting-started/homepage) whose
  widgets are recently visited pages, most visited pages and the entities a person starred
  ([reference](https://backstage.io/docs/reference/plugin-home)). A developer's home is where they
  left off, not a dashboard of the platform.
- **The console overview** ([research](console-overview.md)) answers what is broken, what is running
  and what it cost, for the whole installation. The portal answers the same questions for one person.
- **The demo** (S-301) draws three blocks: what needs you, usage tiles, and pick up where you left off,
  with a primary action of a new conversation.

- **GitLab** keeps a contextual sidebar, pinnable destinations and a global Search or go to
  entry ([navigation guide](https://docs.gitlab.com/tutorials/left_sidebar/)). ownsolid already
  has these in its shared frame, so the portal can reuse the same navigation behavior.

## What the server has today

| Section | Source | Exists |
| --- | --- | --- |
| Needs you | `GET /api/v1/notifications`: a workflow run of mine that failed, cancelled or was interrupted, unread | Yes |
| Pick up where you left off | `GET .../application-conversations`: my conversations, newest first | Yes |
| Published to you | `GET .../applications`: workflows published to my workspace | Yes |
| Usage | Tokens, calls and GPU hours against my quota | No: POR-111 |

The applications contract declares its `x-error-codes`, each checked against the status the server
really sends (`TestDeclaredErrorCodesLeaveTheServerAsDeclared`).

Both routes take `workspace_id` from the query string, so the service must not trust it. It does
not: inside one transaction it rechecks the session, then locks the workspace and reads the caller's
membership (`workspaces.RecheckScopeWithin`); a workspace the caller is not a member of answers
`RESOURCE_NOT_FOUND`, the same as one that does not exist, and the page is cleared. The repository
test `another_workspace_is_not_found` proves it for both lists and for the HTTP handler on
PostgreSQL 18.6. A console session gets `RESOURCE_NOT_FOUND` too: the lists belong to the portal.

A notification carries the workflow's ID but not its name. The page names a run from the
applications it has already read (a lookup page of up to 100) and from the person's conversations;
a run whose application is in neither, because it is no longer published or lies beyond the lookup page, is named "an application in this workspace", which is true either way. Putting the name in the notification would
change three dialects' locking queries in another module for a label, so it waits for POR-102,
which owns runs.

## The approach

- An overview, in the order S-301 specifies: needs you, then the two lists, then usage.
- Keep Home directly reachable. Group applications, conversations and models under AI tools,
  with keys, resources and the support reference under Workspace. Reuse the shared frame
  for disclosure, filtering, pinning and command search. Navigation does not set workspace
  scope and requires no new dependency.
- Each section reads on its own and fails on its own with a retry; a section that could not read
  never says it is empty.
- A row links once its page ships, as navigation items do. Until POR-102 and POR-103 ship, rows
  name the conversation or application and when it changed, and do not pretend to open.
- Usage shows the not-ready state: visible, with its reason and POR-111.
- The workspace is the one the switcher chose, else the session's; no section takes a workspace
  from the address. Every part of the page reads one shared choice (`useWorkspaces`), so the
  sections follow a switch at once, and their query keys name the workspace, so a switch never
  shows the previous workspace's rows. The page waits for the workspace list before reading, as a
  choice made earlier is only known once it loads.
- Needs you reads the notification list and keeps the person's unread runs in this workspace
  that failed, were cancelled or were interrupted. It names five; more say that the rest are in
  Notifications. Mark as read is the bell's action, and either place refreshes the other.

### Rejected

- **Building conversation and application pages here.** They are POR-102 and POR-103; ADR 0041
  limits this item to what exists.
- **Invented figures or sample rows.** ADR 0036 removed them; a new installation has none.
- **A workspace and outcome filter on the notification list for this page.** A person's notices
  are bounded by the retention period and read a lookup page at a time; filtering that page in the
  browser shows the same rows without a new query shape in three dialects.

## Failure modes and tests

- One source fails: the others render, the failed one offers Try again (browser flow per source).
- Nothing published, no conversations, nothing failed: each section says what will appear and how.
  There is no filtered empty state on this overview because it has no result filter.
- A workspace list request that fails for a session without a default workspace shows the cause
  and a retry. It cannot conclude the user has no workspace until the request succeeds.
- After navigation, the current group opens and groups opened on the previous page close.
  Pending destinations keep the shared unavailable state and remain discoverable in the navigation
  filter. Unavailable destinations explain what is missing in text. The command menu can still
  open a live destination without expanding its group.
- Workspace-scoped pages delay placeholders for 300 ms, so a fast read does not flash a skeleton.
  Each home section renders and retries independently. The unavailable usage card keeps its
  working support link reachable.
- Another workspace's rows: the server scopes them; a flow checks the request carries no workspace
  the session does not have.
- Both languages, axe in both themes, keyboard order.

## What the finished item does

The portal's `/` shows needs you, recent conversations and published applications from the server,
usage as not ready, every state above, in both languages, run once against lane B's installation
with a published workflow.

The full POR-101 flow remains dependent on the conversation and application destinations
(POR-102 and POR-103) and on real usage (POR-111). Those omissions do not prevent testing
the shell, the workspace choice and each independently loaded section.

## Member isolation and a failed source on an installation

The existing live home flow reads published applications, recent
conversations and unread failed runs, then marks a notice as read. Run
that same flow with a regular member in a private HTTPS installation.
Seed a published projection with one two-turn conversation and one unread
failed run for the member. Give another account in the same workspace a
conversation with a distinct turn count for the same application. Use the
existing workflow repository fixture pattern and the terminal-run
notification trigger. The fixture checks the Portal read path and does
not exercise the workflow interpreter or publication admission.

Abort only the notification list requests with
[Playwright network routing](https://playwright.dev/docs/network#handle-requests).
Keep the applications and conversation requests on the real server.
Confirm both sections render their real data while Needs you offers a
retry. Release the failure and check the retry reveals the member's run.
Assert that no second conversation row from the other account appears,
and that directly naming an inaccessible workspace is refused by both
application projections. A stubbed home response could mask a broken
actor filter. Keep the existing real mark-as-read and reload assertions.
No new dependency is needed.

The screenshot of the partial overview showed the generic control-plane
connectivity title while both lists were present. Use each section's
own title with the shared retry notice. Keep the scope explanation for
a missing workspace and the original non-retryable error details.
A transient failure needs a retry for that section, with the request ID
available when the server supplies one.

The local Mac run used Chromium over private HTTPS with PostgreSQL 18.6.
The member saw the published application and only their two-turn
conversation during the notification failure. Retrying revealed their
failed run. The notice stayed read after reload, both foreign-workspace
requests were refused, and no request left the installation. PostgreSQL
confirmed the member's read timestamp and the presence of the other
account's seven-turn conversation. The ten Portal home browser tests
also passed, including the existing missing-workspace explanation.

## Foreign scope on a normal page request (POR-114)

The existing live home and catalogue flows check foreign-workspace URLs
with direct browser fetches. The remaining boundary is the page's own
request and its recovery after navigation, reload and a different login.
[OWASP authorization guidance](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html)
requires permission checks on every request, including a guessed resource
identifier. The [Applications contract](../../api/openapi/v1/applications/openapi.json)
uses a query workspace, while the [Distribution contract](../../api/openapi/v1/distribution/openapi.json)
uses a path workspace. Both services call the shared workspace scope
check. Keep the actual dispatcher and PostgreSQL response as the
authority for this case.

Use two ordinary fixture accounts. The first can select workspaces A and
B. The second belongs only to B. Seed distinguishable published
applications in A and B and a canary application in a third workspace
of a different tenant. On the ordinary Home request, use
[Playwright Route.continue](https://playwright.dev/docs/api/class-route#route-continue)
to replace only the applications request's workspace parameter with
the foreign ID. Retain the real cookies, method, origin and response.
Repeat on the ordinary Models alias-list request by replacing only
its workspace path segment. The UI must show the scope recovery guidance,
keep its independent sections, and never render the canary. A 200 with
an empty list fails this test.

Release the routing change and reload. Check normal Home and Models
navigation, the selected workspace after browser Back and reload, and
sign out then sign in as the second account in the same browser.
A workspace parameter in the page address must not change the chosen
session scope. Observe response bodies as well as rendered text so a
client-side filter cannot conceal leaked content. Verify the recorded
refusal request IDs against PostgreSQL for zero successful foreign-scope
audit records and confirm the foreign canary still exists. The fixture
seeds published projections and does not qualify workflow execution.
Use the existing pinned Playwright, PostgreSQL and Go dependencies and
their [documented licences](sdk.md#licenses). No dependency, model or dataset is added.

Run the opt-in flow in `web/apps/portal/e2e/live-workspace-scope.spec.ts`
with `OWNSOLID_LIVE_PORTAL_SCOPE_FILE` pointing to private fixture JSON
and the normal live HTTPS browser options. The file contains `people`
with email, password and a default `workspace`, a `second` workspace,
and `foreign` with workspace and marker. Each workspace has id, name
and application. The browser writes `scope-proof.json` next to the
fixture for its independent database verifier. Keep both files outside
the repository and remove the generated credentials after the run.

### Account security is scoped to the person and entrance

The shared S-308 Account route is an account-security surface, not a workspace business
resource. The [identity contract](../../api/openapi/v1/identity/openapi.json) exposes session
reads and revocation at `/api/v1/me/sessions`, without a workspace selector. The UI's
[typed helpers](../../web/packages/api/src/identity.ts) send only the cursor on GET and no
query/body on DELETE. The server derives account and entrance from authentication. Do not
add a workspace field to make Account look like Home or Models, and do not disable personal
session security when there is no selected workspace or a license blocks new business.

Extend installed POR-114 acceptance by observing the Account page's ordinary requests after
a workspace switch and with a foreign `workspace_id` in the page address: neither may forward
that parameter or change the owning account/entrance. Create a second same-account sign-in
through the actual entrance, revoke it through the manager UI, and verify its next request is
refused while the manager remains usable. Another account and the other entrance must keep
the same not-found response for that session ID. Verify durable revocation and audit separately
against PostgreSQL; a scripted browser fixture is not authority evidence. The existing opt-in
Home/Models scope flow still needs its real private fixture and independent database verifier.
These additions introduce no dependency, model, dataset or new server contract.
