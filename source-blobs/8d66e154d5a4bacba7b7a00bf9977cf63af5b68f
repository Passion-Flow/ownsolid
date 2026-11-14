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
  and needs no permission of its own. It asks from when the node joined rather than the trail's default window, so
  a node's whole life is there. Each row links to the event in the audit trail.
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
