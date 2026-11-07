# 0039: The web lane builds the navigation and the list foundations next

Status: accepted.

## Context

Lane B finished the list in [ADR 0034](0034-web-lane-governance-pages.md). UX-122 and UX-106 stay open only for
what later pages bring. P0 is still open in lane A, and the roadmap defines P1 lanes only when it closes.

[ADR 0037](0037-simple-on-the-surface-deep-underneath.md) decided how the console stays simple without
getting shallow. Every page built before those decisions land has to be revisited after them, while the model
and catalog pages of the build plan's second stage depend on lane A's scheduling work. So these come first.

## Decision

Lane B continues, in order, with:

1. **UX-123** the sidebar: collapsing groups, a filter and an icon-only mode.
2. **UX-124** pinned destinations, which sit at the top of that sidebar.
3. **UX-128** `j` and `k` through a list, which every list inherits from the shared pattern.
4. **UX-126** summary tiles that filter the list they sit above.
5. **UX-125** one advanced fold per form.
6. **UX-127** resource detail pages with their own tabs.
7. **UX-129** English strings loaded as their own chunk, so the first download stops growing with every page.

An item that needs a server change makes it in the module that owns the endpoint, small and additive, and stays
out of the agent session, claim and heartbeat paths lane A is working in.

## Consequences

- The first download's limit rises to 170 kB for UX-123, and UX-129 brings it back down.
- The P1 lane plan that follows P0 starts from whatever of this list is still open.
