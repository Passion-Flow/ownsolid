# Global search

Research behind UX-106: finding a node, a person, a task or a workspace from the command menu without
first opening the page that lists it.

## How others solve it

- **Kibana** ([global search README](https://github.com/elastic/kibana/blob/main/x-pack/platform/plugins/shared/global_search/README.md),
  [RFC](https://github.com/elastic/kibana/blob/main/legacy_rfcs/text/0011_global_search.md)): each
  feature registers a result provider with a `find` function. The service calls every provider, merges
  their results as they arrive, stops at a timeout, and passes each provider an abort signal so pending
  work can be cancelled. `maxResults` caps what a provider may return. Results are unsorted: each carries
  a score and the consumer orders them. The RFC, from 2020, says nothing on a provider that fails or on
  permissions, so those two choices below are ours.
- **GitHub** ([command palette](https://docs.github.com/en/get-started/accessibility/github-command-palette)):
  one box searches and runs commands, shows its scope, and reaches "any page that you have access to".
- **cmdk** ([README](https://github.com/pacocoursey/cmdk)), already the console's menu: asynchronous items
  are rendered as they become available, `filter` ranks each item and `Command.Loading` marks the wait.
  It is not virtualized and is good up to 2,000 to 3,000 items, far above this menu's few dozen.

## What the standards require

- [WAI-ARIA combobox pattern](https://www.w3.org/WAI/ARIA/apg/patterns/combobox/): focus stays in the
  input and `aria-activedescendant` names the row. cmdk does this, so results arriving must not move focus.
- [WCAG 2.2, 4.1.3](https://www.w3.org/WAI/WCAG22/Understanding/status-messages.html): results themselves
  are not a status message, but "Searching...", "18 results returned" and "No results returned" are, so
  those go in a `role="status"` region.

## Choices

- **One provider per resource, called from the browser, in the shape Kibana uses.** Each list endpoint
  already enforces its own permission and is already routed to its cell, so search cannot show what its
  page would not, and no new endpoint has to learn every module. A slow or failed source holds back only
  itself. Later modules add a source, not a server change.
- **One query parameter, `q`, on each list: case-insensitive, matching anywhere in the name.** The Nodes
  page's `name_prefix` never used an index (the name index is on `name COLLATE "C"`, the filter on
  `lower(name)`), so matching anywhere costs the same as matching the start and finds `a01` in
  `cn-bj-gpu-a01`. Both sides are lowered in SQL, so Go and the database cannot disagree about case.
  `name_prefix` and `email_prefix` stay for callers that want one exact row.
- **The list page searches the same way.** "See all" opens the page with the same `q`, so it shows the
  same rows. Nodes, Users, Tasks and Workspaces each gain or keep a search field on that parameter.
- **People match on address or display name**, since an administrator knows one or the other.
- **Tasks match on their node's name**, the one name a task has; a pasted task ID opens that task. The
  list also accepts `node_id`, which the contract already declared and the server refused.
- **A source the person may not use is left out silently**, as it would be from the navigation. Any other
  failure says which source failed and offers a retry, while the others keep their results.
- **Five rows per source, then "See all"**, as the prototype keeps the list short.
- **Rows sit in Go to with their kind on the right**, as in the prototype, in the order the answers
  arrive. A row inserted above the highlight would make Enter open something the person was not looking
  at; a browser flow caught exactly that.
- **Typing waits for a pause and a new search cancels the old one**, through the query's abort signal.

Models and applications have no console page to land on yet (S-201, S-309). A source is added with each,
so UX-106 stays open until then, as [ADR 0034](../decisions/0034-web-lane-governance-pages.md) expected.

Rejected: a server endpoint that searches everything, which would duplicate each module's permission
rule, and cross cells once ARC-202 lands; a search index or `pg_trgm`, which adds an extension for a
scale PostgreSQL scans in milliseconds (measured below); typo tolerance, which needs the same index.

## What it costs

Measured on PostgreSQL 18.6 with 10,003 nodes and 2,000,000 finished tasks, the published fleet size,
with the migrations' indexes, a page of 6, on a laptop. Times are execution only.

| Query | Time |
| --- | --- |
| Nodes, nothing matches (a scan of the 134 pages of nodes) | 2 ms |
| Tasks, `node_id IN (matching nodes)`, every node matches | 0.4 ms |
| Tasks, the same, one node matches | 3 ms |
| Tasks, the same, **matching nodes have no tasks or nothing matches** | **3,300 ms** |
| Tasks, nodes first then each one's newest page, nothing matches | 2 ms |
| Tasks, the same, three nodes without tasks | 2 ms |
| Tasks, the same, all 10,000 nodes match | 480 ms |
| Tasks, the same, all match, a state no task is in | 716 ms |
| The whole statement, both tables and node names, nothing matches | 5 ms |
| The whole statement, every node matches | 1,700 ms |

The planner, seeing many matching nodes, walks the whole history in date order, which is fastest when
a match comes soon and slowest when none does. A search that finds nothing is common, so the statement
reads nodes first. Its worst case is a term in every node's name, on a fleet at the published size, and
then the task rows take up to 1.7 seconds while the other sources have already answered. The console asks
only after a pause in typing and cancels what it no longer needs.

## Licences

No new dependency. cmdk is MIT, already bundled, and needs no network.

## Failure modes and tests

- `%`, `_` and `!` in a search match themselves; PostgreSQL test.
- A search must not reuse a cursor from another search; PostgreSQL test.
- A portal account and a non-administrator are refused; Go test, and the menu hides the source; browser flow.
- One source fails, the others still show and the failed one retries; browser flow.
- An old answer arriving after a newer search must not replace it; browser flow.
- A slow source answering after a fast one leaves the highlighted row first; browser flow.
- Pressing Enter on a result opens it, and closing the menu forgets the search; browser flow.
- A node, person or workspace found by search opens even when not on the list's first page; browser flow.
- A name in Chinese is found, on the server and in the page; PostgreSQL test and browser flow.

## What the finished item must do

From any console page, `Cmd K` and part of a name shows matching nodes, people, tasks and workspaces, each
opening its detail; "See all" opens the filtered page; failures retry; and it runs on a real install.
