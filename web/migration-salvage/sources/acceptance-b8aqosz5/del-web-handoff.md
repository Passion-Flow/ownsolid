# Web acceptance inputs for the shared installation

## Immutable original candidates

Apply in this order, preserving authors:

1. db8fe58c38ce02503f22e2a254dfab01eb9bf7ab
2. 40fa3f5b49371d2b7d2dad1aec469ed99b659189
3. 54614882d91f56d3c1bb02cbd8857b459f2818f1
4. 342c556a6d5d2bd2c133205912e02ca928d60ed1

All four authors are Passion. <279058513+Passion-Flow@users.noreply.github.com>.
The original bundle and integration-inputs manifest already supplied remain authoritative.

## Necessary follow-up fixes

Focus bundle: /root/lane-rel-tools/web-focus-ce92c4d2.bundle
SHA256: 96c9b37fd75d825e06528c9eab7a84b764ea8092f26f772f6cf18059da843693
Requires: 106ca52dd867bcb38f142948a52a1f4af41d1d8e
Apply only these commits, after the original four:

1. fa63032637239ba0ce2b11075eadd1a182ae44c5
2. ce92c4d264071c0444526fd02cb4aa30b8bfea3d

Both have the same author as above. The bundle also contains rebased equivalents of the original four; do not apply those duplicates.
Three targeted Chromium checks passed: delayed viewport reveal, permanent offscreen rejection, and the original Portal key drawer. The old walker failed the controlled delayed-reveal fixture as expected; its recorded error still reports indicated=true and visible=false. Full shared-installation acceptance is pending.

Additional Help locator repair: 5d5954691dad2c5dce8d5545f62efada371243af
Bundle: /root/lane-rel-tools/web-help-exact-5d595469.bundle
SHA256: 8b9c1b3926911783282bdbf602f2fdae08725b0420abe1756ecf3992e564e54a
Requires: ce92c4d264071c0444526fd02cb4aa30b8bfea3d
This anchors the accessible operation ID so getAdminWorkspaceOperation cannot also match getAdminWorkspace. Initial Console Help run had 11 passes and these two strict-locator failures. Lint, format and shell type checks pass after the repair. The repaired source passed all 13 Console and 17 Portal Help cases with one worker and no retries. All ten screenshots were read. This Web tree is identical to frozen combination 1389de28. Original and rerun logs are retained separately; these are mocked-authentication built-reference checks, not installed login.

The separate GW-104 three-key locale bundle is excluded from this combination.

## Scope preparation and independent authority

Reference source: /root/lane-rel-tools/web-scope-fixture-reference.go.txt
This is the unrun temporary fixture prepared before the unified-installation arrangement. It was never started. It is a reference for data preparation and independent queries, not an installation harness and not evidence of a successful run. Do not start its separate server.

Preparation fragment: its first loop creates two ordinary users using the existing Passwords.Hash implementation, creates A/B memberships, then creates three enabled published workflow projections with distinct names. The graph is an empty projection fixture and does not demonstrate runnable workflows. The two accounts have no Admin authority. The first belongs to A and B; the second belongs only to B. The foreign tenant/workspace has neither membership and a unique published marker name. Adapt this preparation to the actual installed schema and own the fixture records under the joint installation.

The browser JSON shape is in /root/lane-rel-tools/web-integration-inputs-342c556a.json. No real credential file is provisioned in this handoff. Generate credentials at runtime and put the JSON in a protected file; pass only its path through OWNSOLID_LIVE_PORTAL_SCOPE_FILE. The browser writes scope-proof.json beside that file.

Independent queries are in the final select branch of the reference source. They must be run against the installed PostgreSQL after the browser writes its proof, not inferred from the browser:

- Exactly one foreign enabled workflow still has its original tenant, workspace, unique marker, current published version and version number 1.
- Neither user has a membership in the foreign tenant or workspace.
- Neither of the two refused request IDs has any success/accepted audit event.
- Neither ordinary user has any success/accepted audit event scoped to that foreign tenant or workspace.

Validate both request IDs as UUIDs. Preserve the installed revision, schema, workflow/version IDs and query results with the browser proof. Do not expose the credentials or the raw browser fixture in reports.

The existing live Help spec uses real login and TLS but currently checks English only. The regular Help spec uses mocked authentication and the actual generated index; its bilingual results cannot stand in for real installed authentication.

## Standalone installed bilingual Help acceptance, Mac only

Directory: /Users/passion./.local/state/ownsolid/web-scopes/acceptance-b8aqosz5/installed-help
Spec: parameters.spec.ts
Config: playwright.config.ts

The script has passed test discovery and TypeScript checking, but has not run against an installation yet. No API routes or assets are replaced. Traces, videos and automatic screenshots are disabled to keep login credentials out of artifacts; explicit screenshots are taken only after login. It signs into an English fixture account, checks parameter and error semantics in English, changes the saved account language through the real menu, reloads into Chinese at 390 pixels, repeats the checks, and restores English in finally. The account must start in English and be a disposable account owned by this acceptance environment.

Run from that directory, holding the shared Mac gate lock:

    ./node_modules/.bin/playwright test parameters.spec.ts --workers=1 --reporter=line,json --output <private-report-directory>

Required environment references:

- OWNSOLID_LIVE_AUDIENCE: admin, then portal in a separate invocation
- OWNSOLID_LIVE_ORIGIN: https://admin.ownsolid.test:8443 or https://portal.ownsolid.test:8443
- OWNSOLID_LIVE_EMAIL: disposable installed account's email
- OWNSOLID_LIVE_PASSWORD_FILE: protected JSON file with a password field, same shape as the established live helper
- OWNSOLID_LIVE_SPKI: exact installed leaf public-key pin
- OWNSOLID_LIVE_HOST_RULES: map the entrance hostnames to 127.0.0.1
- PLAYWRIGHT_JSON_OUTPUT_FILE: separate admin/portal report paths

The config launches Chromium against the actual TLS installation and starts no web server. Its node_modules is a link to the already installed local Portal toolchain. The imports of the established live helper and the two expected locale catalogs point to the Web worktree. This script is not intended to run on the Linux server. After each locale's workspace parameter screenshot, it runs the existing expectFocusVisible helper on the real page, without registering mocked authentication. This covers installed Help focus, not a populated key drawer; the original drawer regression remains part of the complete browser gate and its three focused local tests.

Current Mac script checksums:

- parameters.spec.ts: 605e42c025ef3114ca4ffd9d7a83fd260cd2d6f455f1f7b4536b3590ac8f7276
- playwright.config.ts: e0787bea09defd380565bed9836b29b271331b6d9b909354ee16d741ea7b01b7

The same directory also contains scope.config.ts. Set OWNSOLID_LIVE_WEB_SOURCE to the exact installed source checkout and run:

    ./node_modules/.bin/playwright test --config scope.config.ts --workers=1 --reporter=line,json --output <private-scope-report-directory>

This configuration selects that source's existing live-workspace-scope.spec.ts and starts no preview server. Keep the Portal origin, SPKI, host rules, protected scope JSON and separate JSON report environment references from the original manifest. Test discovery and config type checks pass. The browser and independent database verification remain pending.

The current final installation input is 4463bbe628f60486bbdb92c15f72311fb53d076a. Its entire Web tree still matches the qualified 5d595469 tree. Both real Help and Scope harnesses now record and abort any HTTP(S) request outside the active installation entrance origin. Each test requires the outside-origin list to stay empty. The exact SPKI configuration remains in place; ignoreHTTPSErrors is not set. The updated Help script passed discovery and type checking after this guard was added.

The latest exact-source readiness manifest is /root/lane-rel-tools/web-acceptance-readiness-4463bbe6.json. It supersedes earlier source paths: both harnesses now import the established helpers and expected locale catalogs from the clean detached Mac worktree /Users/passion./Documents/Passion's WorkSpace/Development/lane-web-acceptance-4463bbe6 at 4463bbe6. source.ts checks the frozen source identity and records it in the JSON report metadata. The complete original commit/author/patch-id mapping is /root/lane-rel-tools/web-source-preservation-4463bbe6.json. These documents are readiness and source-comparison records, not installed acceptance results.

The resumed installed Help client additionally verifies the authenticated audience and a successful read of the account default workspace before refusing the opposite entrance path with real 404/no-store/request-id checks. Its bilingual tour now records the excluded-operation filtered empty state and Clear search recovery. The product remains pinned to 4463bbe6; only the private client changed. The current parameters.spec.ts SHA256 is 605e42c025ef3114ca4ffd9d7a83fd260cd2d6f455f1f7b4536b3590ac8f7276. Discovery and type checks pass; no installed test has run. A nonempty old ready metadata file is not proof that the expired installation window is still alive. Await the stable current-instance handoff and private TLS/account references.

Use /Users/passion./.local/state/ownsolid/web-scopes/mac-window.py for all subsequent Mac windows. It opens the resident lock with O_NOFOLLOW, verifies a regular file with the current uid, acquires LOCK_EX|LOCK_NB and keeps that FD open until the foreground command ends. It never truncates, replaces or removes the carrier. Busy means no command starts; there is no background retry. Older mkdir wrappers are superseded.
