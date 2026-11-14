# Resource history lightweight qualification

Current source: `43eaa0cbf0574b620745a760d4a4928064e706ce`. Tested code: `314a133a1d635d5403efff6974a296d11054bad4`. The Web trees are identical; the later commit only records evidence in the research note. Worktree clean; nothing pushed to main or ticked. Bundle SHA256: `c197093f357a8e56c3ef2205f72239204929f2b0481a6ecd8d28efac82b7f328`.

This supersedes the earlier source-only 7f9e handoff, which remains unchanged as a preparation snapshot. Adopt all six commits in order from the published base `6ed5c627a04a52b97d273f6782fdb0733d8703c4`. This is separate from the five-file public audit metadata projection `c7ba6271`; DEV must still pair that with its genuine original contract/SDK/tool source.

## Passed scope

Focused lint for changed TypeScript, Console and i18n typechecks, 22 Console tests and 22 i18n tests passed on the exact tested source. The admitted run took about 14 seconds, released the existing shared lock and left no process or service running. Raw initial busy, lint failure and subsequent typecheck failure are retained beside the passing log. The lint guard and two missed model/deployment callers were corrected; no selector was weakened to make them pass.

Five resource consumers now use the same bounded tab: nodes, models, deployments, accounts and connectors. The old node historical-event test still reaches its old event through an explicit bounded URL. The existing 41-case recovery file, shared Help helper and five public metadata projection files are byte-identical to the published base.

## Remaining verification

No browser, screenshot, complete/native or installed run has executed for this candidate. The prepared browser selection is `resource-activity.spec.ts` (21 expected cases, not discovered), unchanged `audit-read-recovery.spec.ts`, `resource-pages.spec.ts`, and `node-page.spec.ts`, plus the page inventory accessibility/keyboard flows. Planned screenshots are 27 raw states across English desktop, Chinese 390px and invalid local dates. Generated/read: zero. Run the old-build negative control for the 400-day-old-resource policy cases separately; it remains unrun.

Before publication, complete `make check`, `make build`, `make test` on final combined source and actually open every new screenshot. The three design answers are: Detail archetype; seven defined states with different recent/filtered empty remedies and existing visible not-ready explanations; Read activity as the primary tab action. These are definitions pending visual confirmation.

No additional full/native/browser allocation or kernel waiter was created. Installed acceptance requires fresh exact-source assets/client and the coordinator's DEL → WEB/browser/independent SQL → GW order. Do not relabel the frozen 44ab Help package or 1,230-asset archive; do not invent an installed origin, runtime object, cookie or credential.
