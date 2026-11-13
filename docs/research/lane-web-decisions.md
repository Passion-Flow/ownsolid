# Web lane boundaries

The browser and visual checks run on the workstation. The lane can consume the current
contracts and change the web client. A shared contract or a service change belongs to
the owner of that contract. The [build plan](../product/plan/README.md) requires each
screen's entry to be expanded before work on that screen starts.

| Item | Boundary |
| --- | --- |
| ARC-104 | Stage 1 specifies nodes and enrollment but has no managed-cluster screen entry. The [cluster console draft](cell-cluster-console.md) proposes the singleton detail, create and add-worker flows and their seven states. The Nodes contract still needs to describe the Kubernetes enrollment profile before the candidate and enrollment paths can consume it. |
| UX-106, UX-122 | Workspace model search already opens the Model library. Application search and the `I` action depend on the admin catalog (S-309). Its plan entry gives the archetype and action but lacks the content, click paths and empty states. |
| UX-117 | Existing Playwright covers model upload interruption, resume and retirement. The remaining production-browser scenarios include recovery from an already verified immutable object, plus the application, plugin, dataset, training and evaluation flows. The old Go harness belongs outside this lane. |
| UX-131 | Operation audience declarations already generate the reference. At the reviewed contract revision (`3f94669`), all 480 HTTP operations declare an audience and 360 lack an error declaration, including 30 in the excluded knowledge contract. Error codes belong in the versioned OpenAPI operations and must match their handlers. A browser-side list would drift from the contract. |
| DAT-105, S-503 | Stage 5 gives Datasets a summary entry only. The [screen and HTTPS handoff](dataset-console.md) records the list, import, review, publication and test download contract. Adopt the full plan entries before wiring the Training navigation to the new screens. |
| POR-101 | The shared shell can group navigation and switch workspaces. S-301 leaves the continue links for POR-102 and POR-103 and real usage for POR-111. |
| POR-102, POR-103 | The portal has conversation and run operations and a read-only published applications list. S-302 and S-303 have summary entries only. The [S-302 draft](portal-conversations.md) records the proposed surfaces, states and reusable contracts for the plan owner. The applications contract has no portal install operation. |
| POR-104 | The model catalogue already reads the published names, deployments and model metadata. The full item needs prices (TEN-107) and same-source installed description acceptance (GW-120); the description API and bilingual Web consumer are implemented. |
| POR-105 | S-305 has a summary entry only. The portal uses a same-origin Content Security Policy and the separate gateway accepts API keys. The contracts offer no session-bound portal operation to run a prompt. A browser call that carries a pasted key across origins would change the trust boundary. |
| POR-107 to POR-110 | There are no portal screen entries for these items. The plan's Stage 5 entries describe administrator surfaces. |
| POR-111 | S-307 has a summary entry only. S-301 explicitly treats usage as unavailable until the workspace-scoped usage and quota data exist. |
| POR-112 | The account page already handles profile and password. S-308 needs a full entry, and identity has a current-session read but no portal list and revocation operations for a person's other sessions. |
| POR-114 | Model, key and home reads already have workspace-scope evidence. New portal surfaces need the same refusal tests as they ship before this item can be closed. |

Keep P2 behind the requested P1 work. Stay on the existing server-scoped paths and
leave a capability unavailable until its own plan and contract support its user flow.

## Current audit consumer and installation boundary

AUD-108's explicit read recovery is published at
`124520b94be27f673bdb32a3c5d825d1d337f75d`. Its Web tree is
`8deed02cddb981a00c88d70a16bc841d4637be89`; the older a4f/e5c and be62 assets do
not contain the fix and remain immutable. A separate archive preserves all 1,230
new production asset files, their hashes and the original-author qualification.

The complete gate at `c70e9f5950f421e7bef194ae5f5ced0efaf1ca7f` passed
`make check`, `make build` and `make test`: 1,191 Console and 410 Portal Chromium
cases passed, with 28/10 existing conditional skips, no unexpected/flaky/retried
case, and 41 explicit-recovery cases. Eighteen screenshots were actually read.
The published source has byte-identical Web, API, fixture, generator, capability
and Makefile inputs. Native checks/build/tests also passed at the intermediate
integration source `4b432dbf249e01ff9af447b3e5efc354a8682403`.

The newer source's complete native combination still needs its own qualified
window. The shared Help assertion correction is now published at
`44ab52293e516128ba2efc0edc2cb2053acc3462`. Its four EN/ZH focused cases passed;
two saved-query controls rejected the old bare-URL assertion after confirming
the restored input. Both earlier MODEL full-run failures and the negative traces
remain separate. The [Help qualification](in-product-help.md#seeded-restoration-qualification)
does not relabel the prior c70 pass as a complete corrected-helper result.

A new independent client bound to that exact source passed TypeScript, eighteen
input guards, four output-preservation guards and one GW120/Help/Scope discovery
each. It reuses the byte-identical 8dee Web runtime assets; only the reviewed test
helper changes the full Web tree. Older a4f/e5c and be62 packages remain immutable.
One authorized kernel wait used the permanent carrier's same parent descriptor;
the focused/browser/client execution then took about 28 seconds and released it.
No acquisition loop, replacement carrier or new long suite ran in that narrow slot.

Actual GW120, Help, Scope and independent SQL still require DEL's new immutable
installation and fresh private fixtures. Client discovery is not installed browser
acceptance, and source schema constants are not live SQL observations. WEB's
`gw120` description browser flow precedes Help and Scope; the GW owner's independent
same-source API/permission/DB check follows WEB and the independent SQL proof.
Keep the corresponding complete items unticked.

The next independent correction is the existing UX-127 activity window: the
creation-to-now query is rejected once it exceeds configured audit bounds.
The [bounded-history research](resource-detail-pages.md#bounded-history-review)
records the source contract, eight passing isolated cache assertions, chosen
approach and product acceptance cases. Its UI correction is not implemented yet.

## Complete typed lint with independent project processes

Reviewed 2026-11-13 against the exact joint source `82c602d3`. The Linux `9be01cbb`
full attempt exhausted the wrapper's 2048 MiB Node heap in typed ESLint. That is
not proof of a product/configuration defect. The later Mac joint entry at
`9e7404f6` measured 0.350 GiB free against its 6 GiB floor and started no work
child. Both original outcomes remain; no larger-heap qualification is inferred.

### Primary sources and actual configuration

- [typescript-eslint monorepo guidance](https://typescript-eslint.io/troubleshooting/typed-linting/monorepos/#important-note-regarding-large--10-multi-package-monorepos)
  explicitly suggests one package per process using the existing configuration
  when a multi-project lint runs out of memory. The section discusses the older
  project option, so it motivates process isolation, not a guaranteed memory
  figure for this repository's Project Service.
- [Typed-lint performance](https://typescript-eslint.io/troubleshooting/typed-linting/performance/)
  explains that TypeScript type complexity affects memory and that increasing
  Node old-space raises available memory without reducing work. The
  [Project Service guidance](https://typescript-eslint.io/troubleshooting/typed-linting/)
  recommends retaining the nearest real tsconfig and compiler options.
- [ESLint CLI](https://eslint.org/docs/latest/use/command-line-interface) documents
  explicit config selection, JSON output, warning limits and unmatched-pattern
  handling. [Its Node API](https://eslint.org/docs/latest/integrate/nodejs-api)
  supplies the same calculated per-file config used by the CLI. The installed
  10.14.0 `eslint-helpers.js` expands directory inputs, applies configuration
  ignores and matches only configured files before linting; top-level `*`
  matches immediate files, not descendants.
- [Node child processes](https://nodejs.org/api/child_process.html) provide direct
  executable/argument invocation and completion events. Awaiting `close` before
  starting the next process releases its type graph and drains diagnostics.

The pinned workspace is ESLint 10.14.0, typescript-eslint 8.72.1, TypeScript 6.0.3,
React Hooks plugin 7.1.1 and jsx-a11y-x 0.2.0. The one config retains
`projectService: true`, strict/stylistic type-checked rules, TSX Hooks/accessibility
rules, locale-literal/formatting checks and existing test/i18n exceptions.
Applications include both `src` and `e2e`; shared Shell includes its testing
helpers. Config JS and all other configured root/shared files must remain covered.

### Selected implementation and equivalence obligation

Run the pinned original ESLint CLI serially from the same `web/` cwd, with the
same explicit config, cache disabled by default and `--max-warnings 0`. Discover
partition directories at invocation time, not from a maintained workspace list:

1. `*` covers immediate web-root files, including newly added configured files.
2. `apps/*` and `packages/*` cover immediate container files.
3. Every immediate directory under those two containers is checked recursively
   in its own process, including config, source, e2e and testing helpers.
4. Every other immediate root directory is also checked recursively. Hidden and
   future non-workspace directories are not excluded by this runner.

ESLint still decides extensions and ignores for every partition. The only added
discovery option permits an empty partition, such as root Markdown/JSON or an
ignored `node_modules` directory. It does not change parsing, rules, plugins,
severity or warning thresholds. The complete invocation fails if all partitions
are empty, if a process fails or returns invalid JSON, or if a file appears twice.
It aggregates errors/warnings across partitions; a warning in any one fails.
Each child inherits Node resource options; no larger heap is selected by code.
No Makefile, tsconfig, rule configuration, ignore pattern or dependency changes.

The set identity to verify is the disjoint union of those partitions equals the
original `eslint .` file set under the same configuration. Directory splitting
changes process lifetime only; imports and type resolution are still read by
Project Service through the same real tsconfigs. Root/shared/e2e files are not
special-cased out. JSON formatting supports an exact per-file result comparison.

Reject per-package cwd changes (they can change config/glob interpretation), a
fixed package list (future/root files would be missed), excluding generated type
imports from programs, disabling typed rules, a second lint tsconfig, caches,
changed-file-only lint and a larger heap as the implementation. Those either
change coverage/type information or do not lower retained multi-project memory.
Fresh processes may still exceed a small heap for one individually large project;
the benchmark must measure that rather than promise a pass.

### Verification and peak-memory plan

Node's built-in test runner will cover real small ESLint fixtures: root and
container files, source/e2e/shared/config files, new/hidden directories, original
ignores, warning failure, syntax failure, duplicate/empty protection and child
failure. It adds no test framework. Tests belong beside the config runner and
join the existing config package test command.

The next authorized bounded window must retain exact source/input hashes, plan,
original/new commands, raw stdout/stderr, exits and cleanup. Under the same
inherited 2048 MiB ceiling, run the original unsplit command once and the entire
new `pnpm lint` once, never concurrently. A retained OOM is a baseline outcome,
not a reason to retry or skip its input. Compare successful JSON results by
absolute file path and complete diagnostics, including suppressed messages and
warning counts. If the unsplit process cannot complete, collect its exact
pre-parse inventory through the pinned installed ESLint discovery implementation
and compare it with all new results; do not call partial baseline output a full
result comparison. Record that private API's exact source hashes and keep it
outside production code. Calculate original/new config fingerprints for every
selected file using the public API; parser metadata/options, rules, plugins,
globals and linter options must match.

Use `/usr/bin/time -l` for per-command maximum RSS on Mac, and sample the owned
process tree's RSS to include the coordinator and active child. Node old-space
is a heap ceiling, not total RSS. Linux uses `/usr/bin/time -v` in its own future
opportunity. Record wall time as well: repeating imported types can lower peak
memory while costing time. The expected reduction is unmeasured until that run.

Inject deliberate errors outside product inputs using the original cwd/config
and real representative filenames: unsafe typed member access, an unhandled
Promise in e2e/shared/config TS, conditional React Hooks, inaccessible JSX and
untranslated product JSX. Both original and partitioned paths must reject them.
A root JavaScript undefined name and an explicit warning must also fail. Check
that existing locale/test exceptions retain their exact calculated config.

The whole 6 GiB full-profile threshold remains unchanged. No heavy lint, resource
acquisition or background queue is authorized during source preparation. This
tooling change cannot qualify POR-112, UX-127, full check/build/test or installed
offline/SQL/SSO paths without their own exact-source runs.

The implementation is prepared on `codex/web-serial-lint`, after the frozen
`82c602d3` joint candidate. Static `--print-plan` returns 12 partitions: the root
glob, both container globs, both applications, six shared packages and the ignored
dependency directory. Both new JavaScript files pass `node --check`; formatting
of the five changed Web files completed with a 128 MiB maximum Node old-space
limit. At that preparation, the seven real-CLI fixture tests, complete lint, coverage/config comparison,
deliberate typed/plugin errors and peak-RSS benchmark had not run. No resource
lock or heavy lint process was started for this preparation. It is not yet an
accepted replacement or a reduced-memory qualification.

### Actual benchmark admission boundary

The next authorized lint-only entry bound exact source
`56a19799bfec788170d71680b4f0663c23073107`, tree
`b2a8e44948370907c50b66702e3800f3a3fc4b6c`, Web tree
`c6f7153f37ff0625017823ce4a3415d641e24a21`, and verified all 5101 tracked-file
hashes plus the prepared input hashes. Its current DEV prerequisite was resolved
using that attempt's explicit, hash-verified zero-resource refusal, not an older
exit, chat activity or an empty lock listing. No fictitious cleanup of resources
that had never been created was required.

The fresh workstation preflight at 2026-11-13 09:44:13 UTC measured 17,673 free
16 KiB pages (0.2697 GiB) and 152.816 GiB disk free. The lint-only profile required
3 GiB free memory and 15 GiB disk, within a 600-second total including at least
60 seconds for cleanup. It refused before opening the carrier or starting any
lint work child, exit 75 after 0.597 seconds. Only source/receipt verification
and the memory/disk probes ran. No negative file, process group, lock FD, tool
output, browser, build or service was created. No queue, repeated resource probe
or retry followed. The complete 6 GiB gate profile was not changed.

That refused entry left all seven real-CLI regressions, actual file/config equivalence,
original and serial complete lint, typed/plugin negative controls and both peak-RSS
measurements NOTRUN. No performance improvement, passing lint, full
check/build/test or product qualification is claimed. The entry's source remains
frozen; this documentation does not transfer a result to a different input.
The next measurement requires a new resource opportunity with a fresh sample
meeting the admitted floors. The existing OOM, earlier refusals and prepared
comparison inputs remain intact.

### New-epoch fixture result and private reporter failure

The first independently authorized WEB job on epoch `ownsolid-2-20261113-e1`
verified the immutable epoch manifest and the path/same-FD identity, then took
one nonblocking lock. Fresh raw Free was 5,407,391,744 bytes, with
166,387,277,824 bytes disk free. This selected only scope B: exact source
`56a19799bfec788170d71680b4f0663c23073107`, tree
`b2a8e44948370907c50b66702e3800f3a3fc4b6c`, Web tree
`c6f7153f37ff0625017823ce4a3415d641e24a21`. Scope A's original four complete
make targets were NOTRUN. No unverified tooling was mixed into that frozen full source.

The real CLI regression command exited 0 after 2.998 seconds. All seven cases
passed, with zero failed, cancelled, skipped or todo cases. They verified exact
original/new fixture results across root/container/workspace/e2e/shared/config,
hidden and future directories, original ignores, warnings, syntax errors,
all-ignored failure, malformed config, terminated children and directory symlinks.
The malformed-config and warning messages in its raw output are intentional
negative-control diagnostics; they are not failed test cases. No test assertion
was removed, rule weakened or input cached.

The private benchmark wrapper then failed its own summary assertion: it expected
TAP `# tests`/`# fail` lines, while this pinned Node invocation emitted its default
spec reporter with the same actual seven-pass counters. That is a wrapper defect,
not a failed fixture or product lint. The original output, private input hashes
and actual parent exit 1 are retained. A separate data-only repair explicitly
selects the supported TAP reporter and checks all six counters. Its syntax was
checked; it was not executed and no second admission was attempted.

The fixture command's `/usr/bin/time -l` maximum RSS was 94,142,464 bytes; the
250 ms sampler reported 262,094,848 bytes including the guard and runner. That
sampler was later found to be limited to the original process group; the next
section records the resulting scope limitation. These measure only the small
fixture suite, not original/new complete lint, and establish no reduced-memory
performance claim. Node old-space was
capped at 2048 MiB, commands were serial and core dumps were disabled.

The original entry exited 1 after 4.229 seconds and confirmed cleanup. Parent and
runner were gone, owned groups and previews were empty, no negative fixture had
been created, the source was clean and no carrier FD remained at readback. The
new carrier retained its exact identity, mode, link count and zero size; its
creation-only manifest was unchanged. The 600-second/60-second cleanup limits,
old closed entries and previous refusals remain intact. No queue or retry ran.

Actual complete-file/config equivalence, original/new whole lint, real typed/plugin
negative controls and comparative peak measurements remain NOTRUN. The seven
fixture passes do not qualify the full tooling replacement, a complete gate or
POR-112. The remaining benchmark work needs another explicit job opportunity;
the reporter repair and the completed fixture evidence must stay source-bound.

### Source-bound full lint and real negative qualification

A separate explicitly authorized B-only continuation used the same exact
`56a19799` source/tree/Web tree and all 5101 source hashes. It first parsed the
previous frozen actual Node output successfully and rejected nine offline
derived missing, duplicate, failed, skipped, empty or abnormal-output controls.
The candidate runtime, fixture tests, configuration, pinned inputs and original
CLI arguments were byte-identical. The original seven actual pass results and
raw output hashes were therefore retained without rerunning those cases. The
previous parent's reporter failure and exit 1 were not reset or overwritten.

The fresh entry measured 5,296,308,224 bytes raw Free and 166,389,809,152 bytes
disk free. It took one NB lock on the exact new-epoch carrier and retained that
same open description through the runner and cleanup. The 600-second total,
60-second cleanup reserve, 2048 MiB maximum old-space, serial commands and zero
core-dump limit applied. A's four original make targets stayed NOTRUN.

The actual pre-parse discovery and public calculated-config comparison passed
for all 372 original ESLint files. Their disjoint partition union is identical,
all 372 effective configurations match, and five generated/dependency/build/test
ignore probes remain ignored. The breakdown was Console 127, Portal 34, API 31,
Config 14, Help 5, i18n 14, Shell 107 and UI 40. Root/container globs and the
ignored dependency directory correctly selected zero current files; the real
fixture and negative runs separately exercised newly introduced root/dot files
and hidden/future directories. No configured input was removed.

| Actual command | Exit | Seconds | `/usr/bin/time -l` maximum RSS, bytes |
| --- | --- | --- | --- |
| Complete original `eslint .`, same configuration and 2048 MiB heap | SIGABRT (-6), explicit heap OOM | 15.766 | 2,566,897,664 |
| Complete new `pnpm --silent lint --format json` | 0, zero errors and warnings over 372 files | 28.147 | 2,075,901,952 |
| Complete new lint with seven real negative controls | 1, expected rejection | 28.117 | 2,056,159,232 |

The original OOM is retained as a bounded-wrapper outcome, not a product defect,
old lint success or proof of complete positive diagnostic equality. That command
did not finish. The new command did finish and its exact result file list matches
the independent original inventory. The observed command times do not establish
a throughput comparison with the unfinished original command.

Seven exclusively created controls exercised root and dotfile undefined names,
conditional React Hooks, untranslated product JSX, unsafe typed member access,
missing image alternative text and unhandled Promises in e2e, shared testing and
Config TS. Each original stdin check exited 1; the complete new negative lint
also exited 1. All expected rules fired, and messages, suppressed messages,
error/warning/fatal counts matched for every control. Its exact 379-file set was
the original 372 plus those seven files, not merely an equal count. Original
typed/plugin rules, ignores and zero-warning threshold were preserved.

### Memory evidence limit and clean continuation boundary

The reported `/usr/bin/time -l` maximum-RSS field decreased by 19.128% between
the unfinished original and successful new commands. These raw observations
support a complete lint succeeding within the 2048 MiB heap; they do not measure
the maximum simultaneous RSS of the entire owner process tree.

Post-run review found a private instrumentation defect: the sampler selected
only the runner's original PGID and guard PID, without following descendant
PPIDs across process groups. Its new-command sampled value, 73,629,696 bytes,
omits large descendants and must not be presented as a complete owner-tree peak.
The raw `peakActuallyLower` flag was derived from that incomplete sample and is
not accepted as a tree-peak conclusion. All raw samples and flags remain intact;
no missing historical process topology was invented. The separate `time -l`
outputs above retain their own measurement scope.

A data-only private repair follows transitive PPID descendants, records start
identity and retains previously observed reparented descendants while rejecting
reused PIDs and unrelated processes sharing a PGID. Seven offline topology
assertions passed. No actual memory measurement using that repair has run. The
remaining metric qualification is a focused owner-tree comparison in a new
explicit opportunity; unchanged coverage, fixture and negative cases need not
be rerun to inflate counts.

The B continuation actually exited 0 in 82.484 seconds, with cleanup confirmed.
All seven owned negative files were removed by exact recorded identity; the
tracked source was clean, guard/runner and attributed cross-PGID descendants
were gone, previews were empty and no carrier FD remained at readback. The
carrier retained its device/inode/UID/mode/link count/zero size and the epoch
manifest hash was unchanged. No new frontend asset, browser, build, service,
native test or installed verification was involved. There was no second NB,
automatic retry, queue or activation of old closed entries.

Complete-file/config coverage, real negative behavior and the candidate's whole
lint are qualified on the exact source above, together with the unchanged prior
seven-case evidence and their explicit cross-attempt boundary. Full owner-tree
peak qualification remains open. These are not original A check/build/test,
installed or POR-112 acceptance. The full 6 GiB floor remains unchanged, and
neither this candidate nor the unqualified strict-JSON generator is merged into
the frozen `9e7404f6`/`82c602d3` full-gate source. The concrete lint observation is
28.147 seconds and the reported 2,075,901,952-byte maximum-RSS field; neither is
a guaranteed future wall-time or total-tree memory cap.

### Native tree sampling and exit-boundary rejection

A subsequent single admitted memory-only job again selected exact source
`56a19799bfec788170d71680b4f0663c23073107` and its unchanged tree and Web tree.
Fresh raw Free was 4,864,524,288 bytes and disk free was 165,640,785,920 bytes.
The same epoch carrier/open description covered execution and cleanup, with a
600-second total, final 120-second cleanup reserve, 2048 MiB Node old-space
ceiling and zero core-dump limit. The previously qualified seven real fixtures,
372-file/config proof and seven real negatives were retained, not rerun or counted
again. The conditional same-FD continuation into A required valid completed B;
that prerequisite was not met, so A was never entered.

The private sampler now uses Apple's native process API rather than a PGID
filter. It records PID, PPID, PGID, UID, start time in seconds/microseconds and
resident bytes, follows transitive child ownership, retains proven reparented
lifetimes and rejects reused or unidentifiable lifetimes. It samples the guard,
awake helper, controller, worker and command descendants. RSS sums count shared
pages once per process; reads are non-atomic, and a 50 ms interval cannot promise
the continuous maximum or observe every shorter-lived process. A failed sample,
unattributed lifetime or interval over 250 ms rejects the full-command metric.

The original complete CLI again reached an explicit heap OOM, exiting SIGABRT
(-6) in 16.673 seconds. Its 313 timed snapshots plus one final cleanup snapshot
were internally consistent; the maximum saved owner-tree RSS was 2,600,534,016
bytes, with no sampling failure. Its separate `time -l` maximum-RSS field was
2,560,540,672 bytes. This is a bounded failed baseline, not successful original
lint or a continuous-peak measurement.

The new command retained 544 internally consistent snapshots over 21 distinct
lifetimes, including two command-descendant edges crossing PGIDs. Offline review
checked unique PID lifetimes, UID, exact parent/child start identities, transitive
origin, RSS sums and saved intervals; none of those saved frames failed those
checks. This confirms the earlier PGID omission is addressed in the saved data,
not that the whole measurement completed. Its maximum saved RSS, 2,197,012,480
bytes, is only a partial-run observation and must not be compared as a complete
peak. The final sample failed on known PID 74118: task information returned zero
bytes with ESRCH, the BSD fallback did not establish a same-lifetime zombie,
and signal-zero did not establish PID absence. The retained evidence cannot
prove the exact exit boundary. Treating that uncertainty as zero RSS would be
incorrect; the sampler raised `SampleGap` and refused the result.

The new stdout contains complete JSON for 372 files with zero errors/warnings,
and `time -l` reports 28.97 seconds and 2,038,513,664 bytes. The interrupted
measurement wrapper did not capture the command exit, so neither output is
promoted to a new successful run or used to repair the missing lifecycle proof.
The actual parent exited 1 in 46.728 seconds with cleanup confirmed. Its owned
groups, parent/controller and carrier descriptors were absent at readback; the
carrier identity and creation-only manifest hash were unchanged. Original input
hashes, raw results and failed conclusion remain immutable. No second NB,
automatic retry, new queue or A execution followed.

The next private correction must collect detailed return/errno values for every
fallback and bind a kernel lifetime identifier while the task is readable.
Apple's [libproc wrapper](https://github.com/apple-oss-distributions/xnu/blob/main/libsyscall/wrappers/libproc/libproc.c)
and [resource definitions](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/resource.h)
provide `proc_pid_rusage` / `RUSAGE_INFO_V0` with kernel start and exit timestamps;
the installed SDK documents support for live processes and zombies. A prototype
verified the local 96-byte ABI against reads of the reviewer process, and 15
explicit synthetic lifecycle decisions passed. It emits retirement events only
after same-lifetime kernel exit/zombie or confirmed PID absence, never a made-up
RSS sample; changed identity, permission failure and unresolved live-task reads
still reject. These checks did not observe a real exit transition, integrate the
prototype into the executed sampler or start another workload. Historical frames
lack that kernel-start/exit evidence, so this prototype cannot retroactively
validate them. Another actual comparison remains necessary for the owner-tree
peak claim; existing functional qualification and all A/installed/POR-112 limits
remain unchanged. No dependency or upstream implementation is copied into the
product by this private instrumentation research.

### Licences

No dependency, model, dataset or external runtime request is added. Existing
ESLint, typescript-eslint and both ESLint plugins are MIT; TypeScript is
Apache-2.0; Node uses its existing MIT distribution and bundled notices. These
licences permit commercial use, redistribution and offline execution under their
notice requirements. Only the installed pinned tools and Node standard library
are used; no upstream code is copied into the product.
