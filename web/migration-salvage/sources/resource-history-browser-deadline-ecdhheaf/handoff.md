# Inspected-boundary correction variant

Packet: `/Users/passion./.local/state/ownsolid/web-scopes/resource-history-browser-deadline-ecdhheaf`. The predecessor `/Users/passion./.local/state/ownsolid/web-scopes/resource-history-browser-preparation-w5jpf3m7` and its ten regular files remain byte-identical. Product source43eaa0cb / Web tree0c70c6e, plan, all tests/negative probe and frozen asset map are unchanged. No lock acquisition, valid work entry, discovery, build or browser was executed. The original short opportunity is unused; this variant is handed back for the current inspection.

## Requested corrections

1. A single system-clock origin fixes the 600-second total deadline, work cutoff at +570 and cleanup reserve30. Preflight, startup-following waits, cleanup commands/loops and evidence deduct from the same clock. The parent alarm backstop stays armed through finally, descriptor release and terminal recording; it is not stopped for cleanup. Physical kernel work may delay signal/timeout delivery, so there is no unconditional hard-wall guarantee. Deadline interruption, unconfirmed cleanup, or incomplete/contradictory terminal evidence fails conservatively. Success requires the actual guard process return code0 and confirmed cleanup, not an existing status file alone.
2. The launcher fixes `python3 -I`; parent and runner explicitly refuse nonisolated/optimized execution before assertion contracts. Child argv also contains `-I`.
3. Registry parse/read failure assigns real exit1, marks cleanup evidence failed and denies cleanup confirmation. Other cleanup evidence failures also return nonzero. The same FD is held through cleanup/evidence and then unlocked/closed.

## Clock compatibility finding

The local `python3` is3.9.6 and its `time.monotonic()` reading starts near the process lifetime. Python's primary documentation records that macOS cross-process equivalence was added in3.10: https://docs.python.org/3/library/time.html#time.monotonic . Both scripts therefore use `time.clock_gettime(time.CLOCK_MONOTONIC)` for entry, elapsed time and budgets. A real isolated-process probe proved the child value lies between parent before/after samples. The pre-clock variant is retained under `before-system-clock-review`.

## Exact identities and verification

Parent SHA256: `e6bab498143b0daa94fad1893a089465778b8a2a9633d4e39254e15786ef387a`

Runner SHA256: `fc2492b985775e7c59b5ae420a514f0a3666faaf4bd3874bfcca0f338ff8905c`

Plan SHA256, unchanged: `96002e8d3914a2c130a64fabedfaa3b4e102c5de991eedb3886c332d02477d1e`

The latest eleven pure boundary checks passed. They cover syntax, fake-clock/fake-signal budget/backstop behavior, actual invalid-runtime refusals against nonexistent packet paths, real malformed/missing registry branches, byte preservation and the real shared-clock probe. Earlier source-review results are retained. There were twelve tiny invalid-runtime refusals across four source-review pure runs and one clock probe; none could enter a lock-capable packet. No 44-product-unit or runtime browser/full/native/installed qualification was repeated or claimed.

`reviewed-boundary-fixes.patch` compares the reviewed predecessor parent/runner and adds the fixed launcher. `stage-plan.json` remains exactly the original bytes. The plan still expects21 new cases, all41 uniquely partitioned recovery cases,30 inventory cases and27 new screenshots; actual discovery/build/browser/images remain0. Command caps60/70, two workers, NB once/no wait or retry,512 MiB/minimum stricter inherited heap and original resource limits are retained. No scheduler/observer framework or background queue was added.

The current inspection is the only handback boundary for this unused opportunity. No new slot, full/native/installed operation, publication or wait is implied. Eventual browser execution must release the FD before actual image inspection and report generated/read counts separately. The repository stays clean at43eaa0cb.
