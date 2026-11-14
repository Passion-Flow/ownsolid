# a4f Web and installed-client preparation

Source: `a4f1120d5d90a892cca21c991f7d68f0b13c500b`. DEL preparation: `28f76a320692d7d13cf8fab8e06bedc227025608` (not frozen, built or installed by this work). Web tree `e5c48f8a13c9e552dbaa261f0873d724d9f1bd54`; OpenAPI tree `45cb100db6504b80bf5418e2ffe0e8d993ced27c`.

## Original qualification

Web/OpenAPI, generators, fixtures, capability declaration and Makefile inputs are byte-identical to original full-gate source `6a77bcdbc372a0aa7fac24b3f21a83105c246c86` and published `c864fbb10294fa36bdbf4256fe1601f8358ad1f8`. Original-author commits/patch and raw1134 Console/410 Portal reports are retained alongside this package;38 existing installation-dependent cases skipped, with no unexpected/flaky/retried browser case. This does not apply the newer audit-retry development gate to this older tree.

Both app builds were recreated offline from the clean a4f worktree with its own workspace dependency links. Every Console byte matches the archived c864 build; every Portal byte matches the retained c864 build. `qualified-a4f-web-build.tar.gz` contains 1230 production files and retains the original installer layout. Its SHA-256 is `02e3c0e7463b79a72f91bb42a28b0982e955a06941fcd0d977c2a593ccee56e5`. The input manifest records every asset hash.

`installed-entry-a4f-inputs.tar.gz` contains only the newly rebound client code/configuration and new selftest/discovery logs. Its SHA-256 is `e8f02e4410a8aae42697bb34685ba9c80a48cb1497c98721fa55d1dcc95dd2de`. No old session, cookie, storage state, credential file or prior run output was copied. New checks passed TypeScript no-emit,18 input guards,4 output-preservation guards and one GW120/Help/Scope discovery each. No browser, server or real credential was used by those checks.

## Before an installed run

The a4f and current28f preparation browser inputs also match exactly. After DEL chooses the actual full frozen source, rebind a COPY of this client to that clean worktree and rerun checks under the permanent Mac lock. Supply the new immutable image digest, live schema60/stable tables120 proof, exact ready JSON/hash, distinct verified HTTPS origins, owned alias/deployment/workspace/foreign-canary IDs, dedicated Admin/member credential-file references, TLS pins/host rules and protected Scope fixture. The template remains intentionally invalid until those inputs exist. No runtime, URL, object or credential is invented here.

Run `python3 run.py gw120`, then `help-admin`, `help-portal`, then `scope` through the shared Mac wrapper, as described in installed-entry/README.md. Each invocation creates fresh0700 evidence; read its screenshots. Scope uses the bound product test and gives that run's scope-proof.json to DEL's same existing native entry:

`/bundle/scope.test -test.run=^TestTemporaryInstalledScopeProof$ -test.v -test.timeout=60s`

Through DEL's exact fixture execution prefix it independently checks the existing foreign canary, zero foreign membership and no success/accepted audit for the two UI refusal IDs. GW separately compares same-source description intent/revision/API/permission/DB state. Keep the order GW120 -> Help -> Scope -> independent SQL -> CLI -> original OBS7. Close own browser contexts, clients and polling before the quiet boundary. Actual installed browser, SQL, CLI, OBS and new permission variants remain open.

The be62 two tar files, entry, manifest and frozen worktree remain unchanged. Do not use these a4f inputs as a new audit-retry asset build or silently advance them to another Web tree.

## Pending newer Web change

This e5c package excludes the newer AUD-108 explicit-read recovery implementation.
The old lost-response plus focus/reconnect control observes six requests in each
audit consumer without a manual retry. The newer source is under its own complete
gate and needs a separately qualified asset/client package if included in the
final freeze. Do not relabel this package as containing that fix.
