# Web acceptance handoff for the next frozen installation

## Published source and provenance

- Published fast-forward main: `be62f58e846dcc654dafee65cba6cc7480890570`.
- Original-author series: 19 commits after main base `b87f3388`, all authored by
  Passion.; the manifest lists each full hash, author, email and subject. No squash,
  force push or rewrite of remote history was used.
- Original pre-integration source is retained at local archive ref
  `archive/web-description-20261113` (`96975ee50cbd1a0e97c60f2da61cf598f3c39f9e`).
- Exact complete-gate source: `60b85e037aa24fc0d30ae0195e6dffe8f59a6946`, retained at
  `archive/web-gate-60b85e03`. Its Web and OpenAPI trees are byte-identical to published
  be62f58e. Upstream scheduling changes and documentation were integrated afterward.
- The historical detached 4463 tree, ready file, clients and source guard remain
  unchanged. Its exited instance and schema 58/118 are historical only. They are not
  evidence for the new schema 60/120 window.

## Product changes

Console S-205 edits the model alias description independently of routing revisions,
retains drafts through retry and competing edits, reviews current text before saving,
and clears with a revision-safe Undo. English/Chinese, mobile/dark, explicit disabled
reasons, late-answer scope, backdrop protection and plaintext rendering are covered.
Portal S-304 reads the authorized description and removes a stale drawer on denied
refresh. Help S-107/S-108 preserves static contract download addresses offline, explains
saving/retrying in both languages and verifies published parameter/error semantics.
No new dependency or backend product path was introduced by this lane.

## What actually passed

The complete gate ran `make check`, `make build` and `make test` successfully at 60b85e03.
Go modules, Rust and Web unit suites passed; Console Chromium passed 1112 cases with
28 existing installation-conditional skips; Portal passed 410 with 10 existing skips.
There were zero unexpected/flaky results and no test retries. A private external Make
overlay limited workers to Console 4/Portal 1 and reused the same just-built production
assets; it did not select tests or introduce skips. No real GPU/installed integration
fixture was configured for those conditional tests.

After rebasing onto b87f3388, `make check` and three scheduling packages passed again.
The initial scheduling attempt under macOS's default symlinked TMPDIR failed strict
physical-directory validation; a short owned physical TMPDIR passed. The full gate's
first attempt also exposed a too-long Unix-socket fixture path and umask-dependent
model fixture permissions. Those failed attempts remain preserved, and the final run
used a short private APFS mount and standard fixture umask. Product backend code was
not altered to hide these environment failures. All owned volumes were detached and
removed; the published worktree is clean.

Focused qualification passed 52 relevant unit tests and 29 browser cases (8 description
flows, 12 Console state checks, 9 Portal state checks). All final nine description
screenshots were opened and read. The eight Help screenshots were also opened and read.
The preview download fault was an actual isolated HTTP 503, followed by native browser
download failure and successful recovery/entrance JSON checks in both languages and
both entrances. It is not injected into an installed server.

## Screen review answers

| Screen | Archetype and fixed structure | Seven states and empty copy | One primary action |
| --- | --- | --- | --- |
| S-107 article | Reference detail: back link, title, contents and sections | Built-in content; unknown article, loading, failure/retry and auth have explicit outcomes; workspace-data emptiness is inapplicable | Read the answer |
| S-108 API reference | List with inline operation detail: search, grouped operations and expansion | Initial built-in reference, distinct filtered no-match/Clear search, loading, partial, error/retry, permission and not-ready are defined in the Help research note | Find an operation; Save contract is secondary |
| S-205 description | Deployment detail with a short two-field native form; no stacked dialog | Loading, first empty description, parent list filtered empty, retry, conflict, denied and legacy/not-ready outcomes are explicit; conflict never discards the draft | Save description in the dialog; Scale remains the page primary |
| S-304 models | Filtered list with read-only preview drawer | First empty and filtered empty copy differ; loading, partial/transient retry, denied, failure and unavailable Try model are explicit; state includes words | Try model, visibly unavailable with a reason until its dependency ships |

Exact state tables, research sources and product qualification are in
`docs/research/gateway-model-description.md`, `docs/research/in-product-help.md` and
`docs/research/portal-model-catalogue.md`. Screenshots and hashes are attached below.

## New input client and exact selftest scope

Use `installed-gw120/README.md` and `fixture.template.json`. The folder now includes
GW120, Help and Scope configs plus a shared protected fixture/source/ready guard and
fresh-output runner. `bind.py` requires a new clean worktree with the exact full frozen
SHA and regenerates all imports from it. The current be62f58e binding is a candidate
used only for type checking and discovery; do not represent it as DEL's final freeze.
No file in the historical 4463 folder was edited.

Selftests actually run: TypeScript no-emit; 18 input guards; four evidence-output
preservation guards; discovery of one GW120 test, one Help test and the directly bound
product Scope test. These checks opened no browser, contacted no server, used no real
credentials and queried no SQL. Current `typecheck.log`, `input-selftest.log`,
`output-selftest.log`, `discovery.log`, `help-discovery.log`, `scope-discovery.log` are
hashed in the manifest. Earlier compiler-path and strict-array typecheck failures are
retained separately and were corrected before the successful checks.

For every real step `run.py` creates a fresh private result directory and records its
client hashes/binding/exit status. Scope receives its own private fixture copy to keep
its proof file from overwriting an earlier run. GW120 closes all three browser contexts
and restores the two shared actors to English before Help/Scope. Help restores English
in finally. No fake server API success is used. GW120 deliberately aborts one POST
before dispatch and verifies retry has the exact same UUID and request-body hash; this
does not claim proof of an unknown committed outcome.

## Required ordered installation window

1. DEL freezes new clean formal main source and binds immutable image + live schema
   60/120. It prepares owned active GW120 alias/deployment, ordinary Portal account,
   Scope canary/nonmembership fixtures and protected credential files. No new real
   installation window has been started by WEB.
2. Rebind a copy of the bundled clients to that exact frozen source; run the client
   selftests again under the permanent shared Mac lock.
3. Run GW120 UI, Help Admin, Help Portal, then Scope; read every actual screenshot.
4. Run independent SQL against that installation, checking the scoped canary,
   memberships, refusal request IDs and absence of accepted-success audits.
5. Run CLI acceptance, then the original OBS7 quiet window only. Do not create OBS8
   or compete for the Linux installation.

The browser's ready/hash/source checks establish input consistency only. DEL must
inspect the actual running image and live schema separately. Installed GW120/Help/Scope,
independent SQL, CLI and OBS7 have not been run by this handoff. POR-104 stays open for
installed description acceptance and TEN-107 pricing. POR-114 remains open for the real
browser and independent SQL evidence. No new roadmap completion box is checked.

## Artifacts

- Machine manifest: `/Users/passion./.local/state/ownsolid/web-scopes/download-discovery-6gj4rt_h/gw120-edit/del-web-manifest-20261113.json` (SHA-256 `c4cd15d999b92eab52fb1e9b05ab51163db4667a0c8a26fe09453112cbec9336`).
- Client bundle: `/Users/passion./.local/state/ownsolid/web-scopes/download-discovery-6gj4rt_h/gw120-edit/installed-web-client-candidate-be62f58e.tar.gz` (SHA-256 `49368bd7dcfa0a9b1c72843bab8cdba9686739e6fdc60a2b4548669c20f70c72`).
- Original-author patches: `/Users/passion./.local/state/ownsolid/web-scopes/download-discovery-6gj4rt_h/gw120-edit/web-original-author-series-be62f58e.patch` (SHA-256 `900dcb3874ad4af8dd55062994ae6ca8ed6c63c32945c58dc49e03cbce0199a9`).
- Complete gate directory: `/Users/passion./.local/state/ownsolid/web-scopes/download-discovery-6gj4rt_h/gw120-edit/full-final-NNbVbXbw`; `exit-status` is 0.
- Post-rebase check: `/Users/passion./.local/state/ownsolid/web-scopes/download-discovery-6gj4rt_h/gw120-edit/publication-check.log`.
- Post-rebase scheduling: `/Users/passion./.local/state/ownsolid/web-scopes/download-discovery-6gj4rt_h/gw120-edit/publication-scheduling.log`.

### Screenshots actually read

- `/Users/passion./.codex/visualizations/2026/11/08/01a1bc5a-6881-7790-88c0-62ef0b189235/gw120-description/qualified-60b85e03/console-description-conflict-en.png` — SHA-256 `438891bfc681a112468be2602913b7d134e5c39c4a91463bd55533102b642957`.
- `/Users/passion./.codex/visualizations/2026/11/08/01a1bc5a-6881-7790-88c0-62ef0b189235/gw120-description/qualified-60b85e03/console-description-conflict-zh-CN.png` — SHA-256 `7e8e9d455c1c23716886070d19d7dcbfee64147ff8729e55bdfd122beb4f4189`.
- `/Users/passion./.codex/visualizations/2026/11/08/01a1bc5a-6881-7790-88c0-62ef0b189235/gw120-description/qualified-60b85e03/console-description-dark.png` — SHA-256 `b1cabda76513ef8371784bd8c0c5d0488a35ced430242c10b1cb1f59dd8bd7cc`.
- `/Users/passion./.codex/visualizations/2026/11/08/01a1bc5a-6881-7790-88c0-62ef0b189235/gw120-description/qualified-60b85e03/console-description-edit-en.png` — SHA-256 `5b98e85fa4820e7ab37826f86b594eb71ac3d47a0b58127c28507b0aa5ade91f`.
- `/Users/passion./.codex/visualizations/2026/11/08/01a1bc5a-6881-7790-88c0-62ef0b189235/gw120-description/qualified-60b85e03/console-description-edit-zh-CN.png` — SHA-256 `5b514161ebbc02a37a47b4dfc27c92f74c6d9d80737d83e67ed268c6140734d3`.
- `/Users/passion./.codex/visualizations/2026/11/08/01a1bc5a-6881-7790-88c0-62ef0b189235/gw120-description/qualified-60b85e03/portal-description-denied-en.png` — SHA-256 `097cca2dabf81e2d34e44007f97083c9004df5ba8705852b67710124d0206b42`.
- `/Users/passion./.codex/visualizations/2026/11/08/01a1bc5a-6881-7790-88c0-62ef0b189235/gw120-description/qualified-60b85e03/portal-description-denied-zh-CN.png` — SHA-256 `24db13baa1833932bc69716b2086318ae94853f9b77a6fe95c480c43e0105af2`.
- `/Users/passion./.codex/visualizations/2026/11/08/01a1bc5a-6881-7790-88c0-62ef0b189235/gw120-description/qualified-60b85e03/portal-description-read-en.png` — SHA-256 `2797c53e84fa8792e43f14a2948febead11a2489a60ee1bac45c3d5f1fa1fe9c`.
- `/Users/passion./.codex/visualizations/2026/11/08/01a1bc5a-6881-7790-88c0-62ef0b189235/gw120-description/qualified-60b85e03/portal-description-read-zh-CN.png` — SHA-256 `f045f80579c797bb941fc8d3b6718aaf25d54938744aee33137a6fbe494f488a`.
- `/Users/passion./.codex/visualizations/2026/11/08/01a1bc5a-6881-7790-88c0-62ef0b189235/help-contract-discovery/qualified-b49514bf/console-contract-discovery-offline-en.png` — SHA-256 `97396a2f9e378dbd66c00032c51cd6b7e957bcdabcd0e4ed50532c4102de7bf1`.
- `/Users/passion./.codex/visualizations/2026/11/08/01a1bc5a-6881-7790-88c0-62ef0b189235/help-contract-discovery/qualified-b49514bf/console-contract-discovery-offline-zh-CN.png` — SHA-256 `660a14b21723b3e4b784338c60eac7c1ef7476845bb40eedb81ce9b6f1fbe342`.
- `/Users/passion./.codex/visualizations/2026/11/08/01a1bc5a-6881-7790-88c0-62ef0b189235/help-contract-discovery/qualified-b49514bf/console-contract-help-en.png` — SHA-256 `6a29f890e8378219a2938b684b3cb317c81f5960639c74a5f8ac4218bae44362`.
- `/Users/passion./.codex/visualizations/2026/11/08/01a1bc5a-6881-7790-88c0-62ef0b189235/help-contract-discovery/qualified-b49514bf/console-contract-help-zh-CN.png` — SHA-256 `67dfd107e678e672bd0243457474e4e70d7822a626845b8014cac8a7c772c4ac`.
- `/Users/passion./.codex/visualizations/2026/11/08/01a1bc5a-6881-7790-88c0-62ef0b189235/help-contract-discovery/qualified-b49514bf/portal-contract-discovery-offline-en.png` — SHA-256 `e22da9835d84d94b305b6e2eed6695952e239984a539939e9e4aa58cd880e7a9`.
- `/Users/passion./.codex/visualizations/2026/11/08/01a1bc5a-6881-7790-88c0-62ef0b189235/help-contract-discovery/qualified-b49514bf/portal-contract-discovery-offline-zh-CN.png` — SHA-256 `660a14b21723b3e4b784338c60eac7c1ef7476845bb40eedb81ce9b6f1fbe342`.
- `/Users/passion./.codex/visualizations/2026/11/08/01a1bc5a-6881-7790-88c0-62ef0b189235/help-contract-discovery/qualified-b49514bf/portal-contract-help-en.png` — SHA-256 `0f4c8f007cff939c8f53525faab8ecee68a4959fa5bf38c7592648057d230616`.
- `/Users/passion./.codex/visualizations/2026/11/08/01a1bc5a-6881-7790-88c0-62ef0b189235/help-contract-discovery/qualified-b49514bf/portal-contract-help-zh-CN.png` — SHA-256 `1193bf43e05be40eb97e1fb4f701e8f93fb3a25a3b3a1ff33b2693e5ae76df58`.

### Original-author commit order

- `f3a487d93f5299806ac458313cc884a9590123d9` — POR-114: Prepare live scope recovery across Portal navigation (Passion.).
- `3986ce38baa31123a010e3d17f070850786dcb1b` — S-108: Check published parameters and apply-cutover recovery (Passion.).
- `cc30e928522f8b86e4ef3569b59d6bea27ae6bf0` — POR-114: Capture selected scope and narrow recovery views (Passion.).
- `1d9fd9e17691d5d61731b8ef1bd609fb386e9501` — POR-114: Observe refused scope responses before checking their status (Passion.).
- `c2a098402bdf81454f04eccfff00d3ccfc28b425` — UX-112: Wait for focused control geometry before sampling (Passion.).
- `69a76e4d33de608fa39da1c452e5ba279de6a541` — UX-112: Record controlled focus-sampling regression evidence (Passion.).
- `d448879ad235c725f60ba3843e10330f39006e58` — S-108: Match the complete workspace operation in consumer checks (Passion.).
- `8cd632dee8d5a75a5c30b14f515ff73ae28ab30d` — S-108: Record bilingual parameter and recovery screenshot review (Passion.).
- `5d6e69b6f960fa46c93bfc7aab3372ba46d1e649` — UX-111: Research resilient discovery of contract downloads (Passion.).
- `1c025fb526e51552b898c112c5c3adf2d4c767a6` — UX-111: Keep contract download addresses available without a lookup request (Passion.).
- `9f96ab7aa1e2487ec495895501271651f96c0c40` — UX-111: Inject isolated preview download failures over HTTP (Passion.).
- `5fef33014f3a5afc286dc69bab9674aec533f0a0` — S-108: Document installed entrance boundary acceptance (Passion.).
- `8664098abaf42ef58e4cba4dbd43b60b26fea1d9` — UX-111: Record native download recovery and bilingual screen review (Passion.).
- `45aca40ce90cdcdc0c7c767295fe26d4996d465a` — GW-120: Research revision-safe Console description editing (Passion.).
- `f2f6a8f37d9b10af52d4067ec79fa12f2f73b7cf` — GW-120: Add revision-aware Console description editing (Passion.).
- `6f184564937f9df0983a76dcbdb9de08d71459f9` — GW-120: Preserve UI scope across late saves and refused catalogue refreshes (Passion.).
- `557a866e6c29d068aeea52a3f701533eb81a20e5` — GW-120: Record bilingual Web consumer evidence and installed boundary (Passion.).
- `459c07bf8ccd9c1abc91a53e866e0a5c49ebb36f` — GW-120: Reconcile Undo outcomes and protect description drafts (Passion.).
- `be62f58e846dcc654dafee65cba6cc7480890570` — GW-120: Record the complete Web gate and final screen review (Passion.).
