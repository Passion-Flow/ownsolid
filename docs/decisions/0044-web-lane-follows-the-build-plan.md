# 0044: The web lane follows the build plan's stages

Status: accepted.

## Context

Lane B finished the list in [ADR 0041](0041-web-lane-truth-and-portal.md). What is left of it stays open by design:
UX-117 and UX-106 wait for pages that do not exist. UX-131 declares the error codes of 392 operations, and every one of
those operations has no page yet; a test already refuses a page that calls an operation with undeclared codes.

The [build plan](../product/plan/README.md) changed the unit of work to the screen, and its second stage, "Serve a
model", has no screens at all. The navigation names Model library, Model services and Gateway and keys, and none opens.
The server behind them exists: import, deployment and distribution all have contracts.

Reading that server showed one gap the screens cannot work around: `ownsolid-install` configures no model store. On an
installed product `models.artifact_root` is empty, so an import cannot start and the page would show only a refusal.

## Decision

Lane B takes the build plan's stages in order. After Stage 1, it continues with Stage 2:

1. **DEL-117** the installer sets up the model store, and a backup carries it.
2. **MOD-119** the Model library: S-201's *Your models* and *Import a bundle*, and S-202.
3. **SRV-111** Model services: S-204, S-205 and the deploy flow S-203.
4. **GW-113** Gateway and keys: S-206.

UX-131 closes with the pages rather than before them. Each page declares the codes of the operations it calls, reading
each handler then; the test that refuses an undeclared call already enforces it. Whatever no page reaches by the end of
Stage 2 is declared as UX-131's own remaining work. Its box stays open until then.

The catalogue tabs of S-201 wait for MOD-113 to MOD-115, which build the index they show. The page shows them as not
ready with that reason, as [ADR 0036](0036-show-every-option-and-fetch-models-offline.md) requires.

## Consequences

- A server change a screen needs is made in the module that owns it, small and additive, as ADR 0041 set out.
- DEL-117 touches `deploy/`, which lane C owns; lane C's list is finished, so nothing collides.
- The UI of a stage cannot be finished ahead of its server: where the server lacks something a screen specifies, the
  screen says it is not ready, and a new roadmap item records the gap.
