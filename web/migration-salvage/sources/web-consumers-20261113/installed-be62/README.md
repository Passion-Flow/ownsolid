# Installed Web acceptance inputs

This private client is outside the product checkout. It starts no server and builds no
image. It is a candidate bound to be62f58e for type checking and test discovery only.
Rebind a copy to DEL's new clean frozen worktree and full source SHA before a real run.
Never relabel, edit, rebuild or reuse the historical 4463 installation as this window.
Only the original OBS7 runs after the ordered browser, SQL and CLI steps.

## Source and installation preflight

DEL must first verify the running immutable image against its new frozen source and
query the live installation for schema 60 and 120 stable tables. Browser input checks
are not image inspection or SQL verification. The ready JSON must contain the frozen
source (full SHA or its explicit hexadecimal prefix of at least eight characters)
and the same immutable image digest. Its complete byte hash must match the fixture.
The client verifies these facts about its inputs, a clean bound checkout, private
credential references and required pinned TLS; it cannot prove the live database state.

Use `fixture.template.json` to construct a mode-0600 owned regular JSON file. Replace
all placeholders; unknown keys are rejected. Password files are mode-0600 regular
owned JSON files with a `password` string. Never place the password in an environment
variable, command argument, report or this fixture. The Portal member is not a platform
administrator and its default workspace is the prepared workspace. Prepare an active
published alias, whose name starts with the explicit owned fixture prefix, and its
real deployment. The description test intentionally changes this owned alias and
ends with an empty description; do not point it at an existing user's alias.

## Bind, selftest and run

`bind.py WORKTREE FULL_FROZEN_SHA` requires HEAD equality, a clean tree and installed
Web dependencies. It replaces only its recorded dependency symlink. It generates
all imports from that worktree; no overlay or product file is changed. Rerun
`checks.sh CLIENT_DIRECTORY` under the shared Mac flock after rebinding.

For an actual run, DEL sets `OWNSOLID_GW120_FIXTURE_FILE` to the protected normalized
fixture, `OWNSOLID_LIVE_SPKI` to the verified comma-separated SHA-256 SPKI values and
`OWNSOLID_LIVE_HOST_RULES` to the approved private resolution rules. Use the shared
Mac-window wrapper around each invocation of `python3 run.py STEP`:

1. `gw120`: two real Admin browser sessions and one ordinary Portal session. English
   desktop and Chinese 390px flow: edit, interrupted-before-dispatch retry with exactly
   the same UUID and request-body hash, conflicting editor 409 VERSION_CONFLICT,
   explicit latest-text review, save, clear, Undo, explicit empty Portal description,
   foreign-workspace refusal and unchanged routing/deployment revision. An aborted
   request before dispatch is not proof of an unknown committed outcome. All contexts
   close and the shared Admin/member language preferences are restored to English.
2. `help-admin`, then `help-portal`: real sign-in and entrance-bound workspace GET,
   opposite-entrance RFC 9457 refusal, bilingual filtered empty recovery, no-parameter
   password operation, required UUID path parameter, Admin cutover errors, keyboard
   walk, offline discovery of the contract link and a real native JSON download with
   entrance verification. No preview-only failure cookie is used on the installation.
3. `scope`: set `OWNSOLID_LIVE_PORTAL_SCOPE_FILE` to DEL's private prepared Scope
   fixture. The runner copies it into this invocation's private directory so the
   product test's `scope-proof.json` cannot overwrite a previous run. The test is
   imported directly from the bound product source. It uses real Home/Models reads,
   actual foreign-workspace refusals, navigation/reload/query isolation, two ordinary
   users, desktop/mobile screenshots and request IDs. DEL must separately establish
   the foreign canary exists and nonmembership/no accepted-success audit via SQL.
4. Independent SQL, then CLI, then the original OBS7 quiet interval. Those steps are
   outside this Web client. No new Linux installation window has been started here.

`run.py` creates a fresh mode-0700 directory in `runs/` for every invocation, preserving
an invocation manifest, exact client hashes, report, attachments and exit status.
Trace/video/automatic screenshots are off; explicit screenshots are signed-in content
only. Reports are private. No request/response credential recording is installed.
Tests block requests outside the entrance origin and never fake a server API response.
Read every resulting screenshot before recording installed acceptance.

## What was actually selftested

At candidate be62f58e: TypeScript no-emit passed; 18 input guard cases passed (shape,
unknown keys, schema/table/origin/UUID/owned-prefix rejection, fixture file mode,
source/hash/stale-4463-ready rejection and password file/symlink protections); four
output-preservation cases passed (fresh distinct directories, 0700 mode, unsafe parent
rejection and symlink parent rejection). Playwright discovered one GW120 test, one
Help test and the bound product Scope test. These checks used no browser, server or
real credential. No installed GW120/Help/Scope test was run by these selftests.
