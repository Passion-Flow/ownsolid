# Shared Help candidate and pending qualification

Candidate: 2b983dbf75feaf36f6ac3423f4d1b0a1ecf0fc52.
Research commit: dbf04bd5. Only the shared browser helper changes under Web/API/build input roots.
Product runtime and generated Web inputs remain the qualified 8dee build.

The candidate keeps remembered-filter behavior and strengthens the final-state assertions.
The original bare URL remains asserted after an explicit persisted clear, where it is meaningful.
No retry, fixed sleep, global storage clearing or concurrency disable was added.
Prettier, targeted ESLint and shell TypeScript no-emit passed.

Browser and new-client results are not yet available while the one kernel wait is pending.
Read gate-state.json, focused-qualification.json and input-manifest.json only if each exists;
the latter two are created from actual successful stages. No prior full pass cancels the two
reported MODEL failures. The complete corrected-helper gate remains a later window.

Owner reproduction commands, with their own matching built assets and available preview ports:

    pnpm --dir web/apps/portal exec playwright test e2e/help.spec.ts --grep 'keeps a contract discoverable offline and retries its download in' --workers=2
    pnpm --dir web/apps/console exec playwright test e2e/help.spec.ts --grep 'keeps a contract discoverable offline and retries its download in' --workers=2

Use the recorded accurate source and normal retries0 configuration. The old-assertion control
is a separately preserved one-line test variant after the stored-query/input latch; it is
not evidence that every unmodified old run on every tree must fail.
