# Errors and empty states

Research behind UX-107: every empty state and error in the console and portal states the cause
and the next action.

## How others solve it

- **Nielsen Norman Group**
  ([error-message guidelines](https://www.nngroup.com/articles/error-message-guidelines/)):
  show the message next to its source, sized to its impact; plain language, with codes only
  for diagnosis; "merely stating the problem is also not enough; offer some potential
  remedies"; no blame, no humour; keep what the person entered.
- **GOV.UK Design System**
  ([error message](https://design-system.service.gov.uk/components/error-message/),
  [error summary](https://design-system.service.gov.uk/components/error-summary/)): "explain
  what went wrong and how to fix it"; instructions over descriptions ("Enter your first
  name"); never clear fields; avoid "sorry", "oops", "invalid"; permission problems get their
  own explanation of next steps rather than a field error.
- **Carbon** ([empty states](https://carbondesignsystem.com/patterns/empty-states-pattern/)):
  a short title, a body with the next action, an optional primary action; for no results,
  say why and suggest adjusting the search or filters; for errors, say whether it is a
  permission, system or configuration problem and give the first step; "don't lead the user
  into a dead end"; the empty state replaces the whole table so a screen reader does not read
  an empty one.
- **Google AIP-193** ([errors](https://google.aip.dev/193)): stable machine-readable reasons,
  a message that is "brief but actionable", and a localized message with "a call to action".
- Atlassian's writing guidelines were fetched but rendered no content, so they are not used.

## Choices

- **The contract lists what can fail.** Every operation the client calls declares its codes
  in `x-error-codes`. A Go test holds that each declared code leaves the server as itself with
  the declared status, because any code the server does not know turns into
  `STORAGE_UNAVAILABLE` on the way out.
- **The interface explains every one of them.** A Vitest test finds every call in the API
  client, reads the codes its operation declares, adds the client's own
  (`NETWORK_UNAVAILABLE`, `REQUEST_TIMEOUT`, `RESPONSE_INVALID`, and the fallback), and fails
  until English and Chinese both have a title saying what happened and a detail saying what to
  do. A new call or a new code cannot ship unexplained.
- **The action fits the cause.** `ProblemNotice` offers Try again only when the failure may
  pass on its own, and holds it for the wait the server asked for, with the time left shown.
  A stale page or an unknown save outcome gets Reload the page instead, because repeating the
  same request would not help. The request ID can be copied for support.
- **Empty states name the next step,** and a filtered list that comes back empty offers to
  clear its filters (UX-103). `EmptyState` takes the explanation as required content.

## Failure modes and tests

- A contract declares a code the server does not map: the Go test fails.
- The client calls an operation that declares no codes, or a code has no explanation in one
  language: the Vitest test fails.
- A retry before the server's wait ends would be refused again: the button stays disabled
  until then (component test).
- A stale page retried in a loop: reload replaces retry for those codes (component test).

## Done when

Every operation the console and portal call declares its failures, each has an explanation
with a next step in both languages, the notices offer the action that fits, and the tests
above hold all of it.
