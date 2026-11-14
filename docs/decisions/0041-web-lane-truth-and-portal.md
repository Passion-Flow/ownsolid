# 0041: The web lane shows what is supported, then builds the portal's home

Status: accepted.

## Context

Lane B finished the list in [ADR 0039](0039-web-lane-navigation-and-lists.md). UX-122 and UX-106 stay open only
for what the model and catalog pages bring, and those pages depend on lane A's scheduling work. P0 is still open
in lane A, and the roadmap defines P1 lanes only when it closes.

The build plan's first stage, "See the truth", still lacks S-106, and
[ADR 0036](0036-show-every-option-and-fetch-models-offline.md) rests on the registry behind it. The portal has two
pages against the console's many, while [ADR 0035](0035-what-belongs-in-each-entrance.md) gives it its own surface.

## Decision

Lane B continues, in order, with:

1. **CMP-215** the capability registry, with S-106 "What is supported" as its page.
2. **UX-102** the portal's home, S-301, showing only what already exists.
3. **UX-111** in-product help and API documentation available offline.
4. **UX-108** English and Simplified Chinese complete for every page, email and error.
5. **UX-117** Playwright flows replacing the inherited browser suite.

An item that needs a server change makes it in the module that owns the endpoint, small and additive, and stays
out of the agent session, claim and heartbeat paths lane A is working in.

## Consequences

- The P1 lane plan that follows P0 starts from whatever of this list is still open.
