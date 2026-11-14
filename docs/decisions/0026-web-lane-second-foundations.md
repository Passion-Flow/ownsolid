# 0026: The web lane's second set of console foundations

Status: accepted.

## Context

Lane B finished the list in [ADR 0015](0015-web-lane-after-p0.md) on 2026-11-06. P0 is still
open in lanes A and C, so the roadmap has no P1 lane plan yet, and a web session would again
wait or reach into another lane's paths.

Six more P1 items in `usability-and-localization.md` are patterns every module page will use,
and none depends on an open P0 item or on a module page. Built now, the first module pages
(nodes, models, users) use them instead of each inventing their own lists, links and undo.

## Decision

Lane B continues, in order, with:

1. **UX-121** precompressed assets. A smaller first download helps every page, and the change
   stays in the web build and the control plane's static file serving.
2. **UX-103** the shared list pattern: filter bar, saved views, column chooser, bulk selection
   and detail drawer.
3. **UX-114** shareable links, which put the list's filters, sort order and open drawer in the
   URL. It builds on UX-103.
4. **UX-107** empty states and errors that state the cause and the next action, as a component
   rule that lists and pages share.
5. **UX-105** undo for reversible actions, typed confirmation only for irreversible ones.
6. **UX-104** the long-task pattern: step timeline, live log, tasks that continue when the
   page is left.

Each pattern ships on the page that already exists (email delivery) or on a page of its own
item, and in the design system, with browser flows and accessibility states, so a module page
only has to use it. UX-106, UX-108, UX-111 and UX-117 stay with the P1 plan: each needs module
pages that do not exist yet.

## Consequences

- The lane keeps owning `web/`, the control image's web build and `design-system.md`. A server
  change goes into the module that owns the endpoint, as before.
- The P1 lane plan that follows P0 starts from whatever of this list is still open.
