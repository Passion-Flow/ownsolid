# 0015: The web lane continues with P1 console foundations

Status: accepted.

## Context

Lane B owned UX-001 to UX-005 and the DEL-001 tick, and all of them are done. The rest
of P0 is lane A's fleet core and lane C's delivery work, which run in order inside their
lanes, and the roadmap defines P1 lanes only once P0 closes. A web session would
otherwise wait, or take items from a lane another session is working in.

Several P1 items in `usability-and-localization.md` are console foundations: they touch
only `web/` (or an identity endpoint the console already calls), depend on no open P0
item, and every page that later modules add builds on them. Doing them first means those
pages arrive split, checked for accessibility and keyboard use, and following the
user's preferences, instead of being retrofitted.

## Decision

Lane B continues, in order, with UX-115, UX-116, UX-112, UX-118, UX-120, UX-119, UX-113,
UX-110 and UX-109. The lane keeps owning `web/`, the control image's web build and
`docs/development/design-system.md`. An item that needs a server change makes it in the
module that owns the endpoint and stays out of lane A's and lane C's paths. The P1 lane
plan that follows P0 starts from whatever of this list is still open.

## Consequences

- P1 items get ticked while the phase is still P0. The phase changes only when every P0
  item is ticked, as before.
- Module pages (nodes, models, users and the rest) stay with their module's P1 item; this
  lane builds what those pages share.
