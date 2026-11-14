# Web consumer handoff

Published source: `c864fbb10294fa36bdbf4256fe1601f8358ad1f8`. Full gate source: `6a77bcdbc372a0aa7fac24b3f21a83105c246c86`.

## Delivered consumers

- AUD-108: localize the audit family and `audit.events_read` in the existing list/preview. Keep category filtering, raw action/request identity and the thirteen-field public shape. No exact-action enum is added.
- MOD-121: distinguish expired and pending-cleanup uploads; stop terminal transfers; explain disabled actions; retain known state on transient refresh but remove it after denied reads. A fresh form creates a distinct intent key. Help search finds the visible lifecycle term in both languages.
- DEL-109 documentation: use the existing installer `control_settings.runtime.licensing.maximum_imports` input; generated Control edits are overwritten. Materialization checks do not establish installed quota acceptance.

## Verification

The complete `make check`, `make build`, `make test` gate passed at the exact full-gate source: Console 1,134 passed/28 existing conditional skips; Portal 410 passed/10 existing conditional skips; no unexpected/flaky/retried cases. Raw artifacts: `/Users/passion./.local/state/ownsolid/web-scopes/web-consumers-20261113/expiry-help-discovery-tzmrh6j8/full`.

After rebase, browser inputs were byte-identical. Publication repeated every other check/build/test target and reused that exact complete browser result. Publication artifacts: `/Users/passion./.local/state/ownsolid/web-scopes/web-consumers-20261113/publication-jX7K1K`. Original-author series and its patch are recorded in the adjacent manifest.

The old production build fails the corrected English expired-search assertion and retains the Chinese positive control. The earlier unfiltered-article false positive and interrupted listener-loss run are retained as failures, not gate evidence. Ten final screenshots were opened and read; their exact paths and hashes are in the adjacent manifest.

## Required screen answers

| Screen | Archetype and fixed structure | Seven states and empty copy | One primary task |
| --- | --- | --- | --- |
| Audit | List plus preview: title, filters, table; event details in the existing drawer | Existing loading, first empty, filtered empty, partial, error, denied and unavailable states; no records differs from no events match | Locate and read an event |
| Import status | Existing three-step flow ending in a status detail; no added modal | Explicit loading, initial history empty, filtered library empty, retained partial read, retryable error, denied and unavailable decisions; history empty differs from filtered library empty | Expired: Upload a new bundle; pending cleanup explains why writes are unavailable |
| Help | Reference detail with searchable index and article sections | Loading/error/denied/unavailable behavior retained; built-in first-time empty is inapplicable and filtered no-article match has its own recovery | Find the lifecycle answer |

## Installation boundary

The be62 Web/OpenAPI tree, asset archive, client bundle and entry manifests remain unchanged. This consumer publication does not qualify a new installed instance. Formal new-instance inputs and source binding must come from Delivery. Run GW120, Admin/Portal Help, Scope, independent native SQL, CLI, then original OBS7 in that order. Do not reuse old4463, rebind old evidence in place, or substitute these development assets into the frozen be62 instance.

Actual AUD-108 append/collector accounting, installed MOD-121 expiry and startup binding, Scope SQL and other owner acceptance remain open. No roadmap box was ticked from preview evidence.
