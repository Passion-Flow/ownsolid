# Portal home

What UX-102 and the screen S-301 are built on: the first page a person who *uses* the platform
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

## What the server has today

| Section | Source | Exists |
| --- | --- | --- |
| Needs you | `GET /api/v1/notifications`: a workflow run of mine that failed, cancelled or was interrupted, unread | Yes |
| Pick up where you left off | `GET .../application-conversations`: my conversations, newest first | Yes |
| Published to you | `GET .../applications`: workflows published to my workspace | Yes |
| Usage | Tokens, calls and GPU hours against my quota | No: POR-111 |

The applications contract has no `x-error-codes`, so the page cannot explain its failures until the
contract declares them, each checked against what the handler really sends.

Its two routes also take `workspace_id` from the query string, and the handler answers
`RESOURCE_NOT_FOUND` to a console session. Before the page uses them, confirm the service checks the
caller's membership of that workspace against the session (AGENTS.md section 9), test the refusal of
another workspace, and declare each code with the status it really leaves with.

## The approach

- An overview, in the order the design asks for: needs you, then the two lists, then usage.
- Each section reads on its own and fails on its own with a retry; a section that could not read
  never says it is empty.
- A row links once its page ships, as navigation items do. Until POR-102 and POR-103 ship, rows
  name the conversation or application and when it changed, and do not pretend to open.
- Usage shows the not-ready state: visible, with its reason and POR-111.
- The workspace is the session's; no section takes a workspace from the address.

### Rejected

- **Building conversation and application pages here.** They are POR-102 and POR-103; ADR 0041
  limits this item to what exists.
- **Invented figures or sample rows.** ADR 0036 removed them; a new installation has none.

## Failure modes and tests

- One source fails: the others render, the failed one offers Try again (browser flow per source).
- Nothing published, no conversations, nothing failed: each section says what will appear and how.
- Another workspace's rows: the server scopes them; a flow checks the request carries no workspace
  the session does not have.
- Both languages, axe in both themes, keyboard order.

## What the finished item does

The portal's `/` shows needs you, recent conversations and published applications from the server,
usage as not ready, every state above, in both languages, run once against lane B's installation
with a published workflow.
