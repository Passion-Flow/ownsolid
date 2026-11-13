# Audit-read Web owner relay

Source review: main d99dbb25. Product Web files were not edited by lane D.
The raw Identity contract remains SHA-256
7fce16936a778912b2ea21d3f70e653aed1d6584d7210de8dccc72c0de4acb32.

Apply `web-owner.patch` in the original Web author's checkout. It contains
exactly five locale/generated paths: Identity TypeScript, the source Chinese
Help description, Admin API Help, Admin contract summary and generated Chinese
Help descriptions. The private original generators and both checks passed;
product `make generate` still belongs to that owner.

Apply `web-consumer-retry.patch` separately to audit-page.tsx and resource-page.tsx.
It disables automatic retries and implicit mount/focus/reconnect refetches only
for those two audited reads. Both patch apply checks pass on the candidate
base; neither patch was applied to product Web by lane D. The consumer patch
has not been typechecked or run in a browser. It does not complete the recovery
UI: continuation errors, an explicit retry of only the failed cursor, a pinned
first-page selection and contextual bilingual uncertainty copy still need the
original author. See browser-retry-review.json for exact source hashes and
acceptance cases. Keep other queries' defaults and the public event fields.

Use the existing console e2e/audit.spec.ts and e2e/resource-pages.spec.ts to test
first and continuation pages with 409, lost reply and timeout; after focus,
reconnect and cached remount, the count stays one until a deliberate user
request. Then prove one new read with the preserved selection and retain the
earlier request ID. Run the owner's existing typecheck, focused Playwright and
product generation/check commands in an admitted resource window.

No server authorization, PostgreSQL commit or installed browser acceptance is
claimed by these generated artifacts or static patches.

The executable command arguments and exact paths/hashes are in handoff.json.
For the original Web author's admitted window, from its checkout root:

    node tools/codegen/web-api.mjs --check
    node tools/codegen/web-help.mjs --check
    pnpm --dir web --filter @ownsolid/console typecheck
    pnpm --dir web/apps/console test:browser --workers=1 e2e/audit.spec.ts e2e/resource-pages.spec.ts

Add the missing failure/manual-retry cases to those two existing browser files
before claiming qualification. These commands do not select live-audit.spec.ts
or infer installed acceptance. Product make generate/check/build still applies
with the owner's existing offline dependency preparation.

Precisely: HTTP 409 itself is not retryable in the current ApiProblem classifier.
The inherited retry risk includes lost replies and timeouts classified as
status 0. The patch also suppresses implicit mount/focus/reconnect refetches.
Keep the coordinator's c864 main and be62 installed assets separate; neither is
requalified here. Patch checks were on 8b17/d99-reviewed consumer bytes. Reconcile
with the Web author's actual source rather than replacing its newer work.
