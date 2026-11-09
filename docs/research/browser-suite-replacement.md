# Replacing the inherited browser suite

Research behind UX-117. `services/control/internal/testing/browser` is a Go harness that started a
real server and ran Node scripts from `tests/acceptance`. Those scripts did not move to this
repository, so the suite cannot run, and it never ran in a gate here.

## What survives of it

The scripts are gone, so each test is known only by its name, its fixture and what its Go side
checks. That is enough to name a scenario, not to port a script. Each scenario is written again as
a Playwright flow against the page that now serves it, in the pattern every page already uses:
mocked flows in `web/apps/*/e2e` and `@ownsolid/shell/testing`, and a `live-*.spec.ts` against a
real installation.

## Where each test went

| Inherited test                                                  | Scenario                                                         | Replaced by                                                                                           |
| --------------------------------------------------------------- | ---------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `TestM16LocalizationBrowser`                                    | The interface follows the chosen language                        | `languageFlows`, `localizationFlows`                                                                  |
| `TestM16InstalledStaticLocaleBrowser`                           | A language package installed offline                             | None: the console ships two catalogs and does not load language packages, so the scenario has no page |
| `TestM20SSOStartAdmissionProductionBrowser`                     | Starting sign-in is admitted, and a refusal is explained         | `sessionFlows`: offers single sign-on; explains a refused start                                       |
| `TestM20SSOBindingRecoveryProductionBrowser`                    | Linking and unlinking survive a retry and a lost session         | `accountFlows`: link, refused link, unlink with one key, unlink ending the session                    |
| `TestM20SSOConnectorRecoveryProductionBrowser`                  | Connector writes happen once, across retries and stale revisions | `sso.spec.ts`: one key across a retry; stale revision; `live-sso.spec.ts`                             |
| `TestWorkspacePolicyProductionBrowser`                          | The configured workspace limit is enforced and explained         | `workspaces.spec.ts`: as many workspaces as allowed                                                   |
| `TestM20WorkspaceReconciliationBrowser`                         | A page catches up with a change made elsewhere                   | `workspaces.spec.ts`: someone else changed it first                                                   |
| `TestM15PriorActorThemeMutationResponseCannotOverrideNextActor` | One person's late save never reaches the next                    | `sessionFlows`: a save still on its way                                                               |
| `TestM20ModelRecoveryTailBrowser` | An immutable object recovers without upload chunks, its model opens and retires, and an empty import can be discarded | [Live model recovery](../../web/apps/console/e2e/live-model-recovery.spec.ts) against HTTPS and PostgreSQL |
| `TestM20ModelImportRecoveryBrowser` | An interrupted real upload resumes from the committed offset, rejects a different file, publishes once and retires with retained files | [Live model upload](../../web/apps/console/e2e/live-models.spec.ts) with scoped PostgreSQL and file proof, including an empty first-chunk discard. |

The last flow was checked by removing what it protects: without the account in the preference's
cache key it fails with the next person on the dark theme.

## What waits for its page

These stay in the Go suite, unable to run, until the page exists. Each is replaced with the page
and removed with it, so a scenario is not lost by deleting its only record.

| Inherited test                                                         | Waits for                                                                   |
| ---------------------------------------------------------------------- | --------------------------------------------------------------------------- |
| `TestM15PortalBrowser`                                                 | Portal applications and conversations (POR-102, POR-103)                    |
| `TestM14PluginBrowser`, `TestM14PluginToolOnlyBrowser`                 | Plugin pages                                                                |
| `TestM08DatasetBrowser`                                                | S-503 Datasets                                                              |
| `TestM09TrainingBrowser`                                               | S-501 Fine-tuning                                                           |
| `TestM10HoldoutBrowser`                                                | S-504 Evaluation                                                            |
| `TestM12KnowledgeBrowser`                                              | Never a page of ours: knowledge is DeepParser's. Removed when the module is |

## Rejected

- **Restoring the scripts from `project-one`.** They drive the old page structure, which UX-005
  removed.
- **Deleting the whole suite now.** Its fixtures are the only record of those scenarios.
- **Running Playwright from a Go harness against a real server in every gate.** The live specs
  already do this against a real installation, with a real agent and a real provider.

## Licences

No new dependency.

## Failure modes and tests

A flow that cannot fail is worse than none: this item's first version of the late-save flow passed
with its protection removed, because its scripted answer was refused as malformed. A replacement
that claims a race or a recovery is checked once by removing the code it protects.

## What the finished item must do

The Go suite is gone, and every scenario it named has a flow on the page that serves it. The item
stays open until the pages above ship.

## Verified-object recovery in the Model library (UX-117, S-201)

The [existing S-201 entry](../product/plan/README.md#stage-2--serve-a-model)
already specifies per-file verification and a link to the model. The
[MLflow registry](https://mlflow.org/docs/latest/model-registry/) likewise
links a version to its producing artifact and gives it a stable address.
Use the normal Admin status and Files pages for the browser evidence.
[Playwright's network events](https://playwright.dev/docs/network) let the
test detect an upload or a request outside the fixture origin without
substituting an API response.

The inherited recovery case seeds a committed import intent and an
immutable object with the exact approved bytes, with no upload chunk
records. That is the fixture boundary. Queue the real import and use the
production worker, selected database, HTTPS dispatcher and browser.
A normal complete upload would create a chunk history and miss this
case. A scripted ready response could make the page pass while file
verification or publication failed.

The browser opens the known import address, follows the recovered model
and reads its approved files and source import. It reloads the import
and verifies the same model remains linked. The browser retires the
model and discards a separate, empty fixture import by typed
confirmation. Check PostgreSQL after the browser run for one attempt
and one published identity, no upload chunks, the retired version,
the cancelled empty import, scoped audit events and all retained file
digests. A failed file, a changed identity, duplicate publication,
re-upload or a missing audit must fail the run. Test the browser through
its private HTTPS origin and keep the fixture network disconnected.

The already-cached
[Qwen3-0.6B revision](https://huggingface.co/Qwen/Qwen3-0.6B/blob/c1899de289a04d12100db370d81485cdf75e47ca/LICENSE)
uses Apache-2.0, which permits commercial use, redistribution with its
notices, and offline use. The test binds every file to the repository's
approved catalogue. No new tool, dependency, model download or
training dataset is required or bundled.

### Browser fixture and verification

Use the [installation browser variables](../development/environment.md#browser-test-against-an-installation).
In a disposable installation, use the precondition in the inherited
[`seedModelRecoveryTail`](../../services/control/internal/testing/browser/model_recovery_tail_test.go).
Seed only the import and verified object, queue it and allow the production
worker to make it ready. Do not set the model version or final job state
in the fixture. In a second authorized workspace, create an empty import
with the same approved catalogue. The two workspaces keep the fixture
within the server's one-active-import and ready-version rules.
Write a private fixture JSON containing `recovered` and `empty` objects
with their `id` and `name`, plus a `files` array of approved `path` and
`sha256` pairs. Pass its path with `OWNSOLID_LIVE_MODEL_RECOVERY_FILE`.
Use the Admin credentials for the actor that owns the fixture. Run from
`web/apps/console` after building the served bundle.

```sh
OWNSOLID_LIVE_MODEL_RECOVERY_FILE=<fixture.json> \
  pnpm exec playwright test e2e/live-model-recovery.spec.ts
```

The run used local Chromium with the fixture's TLS public-key pin,
Control on an isolated Docker network and PostgreSQL 18.6 with pgvector
0.8.7. The browser reopened the same recovered import after reload,
followed its model and verified all nine file hashes shown in Files.
It retired that model and discarded the empty import by name. It sent
no chunk writes or external requests. A separate database and storage
check after the browser confirmed two imports, one version, one
verification attempt, zero chunks, eight correctly scoped audit events
and nine matching retained file hashes. The recovered version was
retired and the empty import was cancelled. No training or inference
ran. UX-117 remains open for the other inherited browser scenarios.

## Interrupted upload and retained audit (UX-117, S-201)

Use the existing Model library import, status and retirement pages from
[S-201](../product/plan/README.md#stage-2--serve-a-model). The
[Playwright network guide](https://playwright.dev/docs/network) and
[route abort API](https://playwright.dev/docs/api/class-route#route-abort)
allow a deterministic interruption while other requests reach the
real server. Keep the existing
[model fixture licence and archive format](model-library.md#making-an-import-bundle-mod-101-mod-119).
The test adds no dependency, model or dataset.

Let two full chunks commit. Abort later chunk requests on the original
tab, leaving the authenticated status reads live. Closing after a
displayed progress number alone races the next committed chunk and
does not identify which offset was durable. Use the real chunk response
and a status read after reopening to identify the checkpoint. Preserve
the original request failure events and the real refusal body for a
same-size file whose bytes differ. Do not record credentials, session
headers or request bodies in the proof attachment.

The upload contract intentionally replays the last stored chunk to
verify a file selected in a new tab. Assert that its success keeps the
same committed offset, and then that the next write begins at that
offset. Starting with an empty upload would miss the protected replay.
Scripted chunk responses cannot prove durability. An independent
PostgreSQL check reads the two contiguous chunk rows while the transfer
is interrupted. The published import retains its total byte count after
the [cleanup worker](../../services/control/internal/modules/models/import_cleanup.go)
removes the chunk rows. After retirement verify one ready import, one
verification attempt, one version and one scoped success audit each
for start, queue, verification, publication and retirement. Re-read the retained model files after retirement and
compare their byte counts and digests to the approved catalogue.

Run the [installation HTTPS recipe](../development/environment.md#browser-test-against-an-installation)
from `web/apps/console` with the configured Admin TLS and credential
variables. Use a clean workspace and the approved USTAR archive. The
impostor has the same byte length and a different stored chunk.

```sh
OWNSOLID_LIVE_MODEL_BUNDLE=<approved-ustar> \
OWNSOLID_LIVE_MODEL_IMPOSTOR=<same-size-impostor> \
  pnpm exec playwright test e2e/live-models.spec.ts
```

During the interruption, read the import and its ordered chunk rows
in the fixture database. Bind the observation to the workspace, actor
and import ID printed by the browser evidence. After retirement verify
the scoped import, version and audit rows and hash the files in the
retained object directory. The three browser proof attachments contain
the raw failed request outcomes, the real server refusal and the
resume receipts. The attachments omit request headers and model bytes.

The workstation run used the approved Qwen archive (1,519,216,640
bytes), private HTTPS with local Chromium and an isolated PostgreSQL
18.6 fixture. PostgreSQL observed two committed 8 MiB chunks while
Chromium's later requests failed with `net::ERR_CONNECTION_RESET`.
A fresh tab read 16,777,216 committed bytes. The wrong file received
HTTP 409 `MODEL_CHUNK_CONFLICT`. The original replay at 8,388,608
left the checkpoint unchanged. The next write began at 16,777,216
and committed through 25,165,824. The browser opened the published
model and retired it by name. The database had one import, one version,
one verification attempt, and five exact scoped success audits. All
nine retained files matched their approved digests after retirement.
The verified upload ledger had already been cleaned up. The browser
sent no external requests. UX-117 still tracks the browser scenarios
that depend on other pages.

### Discard before the first accepted chunk

The Model library already has a scripted first-chunk discard flow.
Replace its installation evidence with a live browser check. Open a
fresh import after the recovered version is retired, then hold the
first chunk in the browser with [Playwright routing](https://playwright.dev/docs/network#handle-requests).
Use the existing typed Discard action. Observe the browser abort the
held request and wait for the authenticated import record to say
`cancelled`. Let the cancellation request reach the real server.
An HTTP response fixture could show Discarded while an upload continued
or the server accepted a chunk. The independent PostgreSQL check must
find zero uploaded bytes, zero attempts, no chunk or version for that
import, and the three scoped start, cancellation-requested and
cancelled audits. The previously retired version and its files stay
in the fixture for the earlier readback. No new dependency or source
archive is needed.

In the combined run, Chromium reported `net::ERR_ABORTED` for the
held request at byte zero after typed confirmation. The import
readback reported `cancelled` with zero bytes and zero attempts.
PostgreSQL confirmed no chunk rows, no version and three scoped
audits for the discarded import. The resumed import still produced
a single version and five scoped audits, and its nine retired files
still matched. The original interruption and wrong-file evidence
is retained alongside the combined discard proof.

## Portal model to personal key (UX-117, S-304 and S-306)

[S-304 and S-306](../product/plan/README.md#s-304--models-for-users-models-in-the-portal)
have separate live model and key cases. The catalogue case reaches the key
form and cancels. The key case issues from its own page. Start the existing
key lifecycle at the catalogue drawer so it proves that the model shown
there is the model the issued key can call. Use the real key and gateway
admission/outcome contracts, including the existing gateway process stand-in.
Check that the owner can read the three persisted requests for the key and
that a different session cannot read those calls. A working selector or a
scripted call log alone cannot prove that ownership is enforced.

During the catalogue read, interrupt only the workspace models request
using [Playwright routing](https://playwright.dev/docs/network#handle-requests).
Leave the aliases and deployments on real HTTPS. The callable name must stay
in the table while the unavailable facts say they are unknown. Release the
route, use the visible retry, and read the recovered facts. Returning a fake
alias could hide a broken join, so the test does not substitute API responses.
Switch to a second workspace where the same member has access and no model
is published. Check the catalogue and key list there, then return to the
original workspace. Separately verify that the server refuses direct reads
for a third workspace where the member has no access. A 403-only check
would miss a UI cache that retained the previous workspace's rows.

The case uses the existing local PostgreSQL and HTTPS fixture profiles,
approved catalogue metadata, and the gateway protocol fixture. There is no
new dependency, model download or dataset. Keep API key secrets out of
command arguments, traces and screenshots. Evidence from this flow covers
publication metadata, key ownership and call history. Inference, a portal
price source and an administrator description remain separate capabilities.

Run the [Portal installation recipe](../development/environment.md#browser-test-against-an-installation)
from `web/apps/portal` with its Portal origin, the gateway command and a
member fixture that can switch workspaces.

```sh
pnpm exec playwright test e2e/live-models.spec.ts e2e/live-keys.spec.ts --workers=1
```

The local Mac run used Chromium, private TLS and PostgreSQL 18.6 in an
internal container network. The server verified the previously approved
Qwen files at startup. The scripted node supplied deployment freshness,
and the admin publication, member sign-in, key writes and call reads used
the real HTTP contracts. The browser saw three successes with the initial
key, restored their history after switching away and back, and found no
records for that key in the administrator's portal session. The replacement
worked alongside the old key during the overlap and was refused after
revocation. PostgreSQL found two published aliases and five reported
calls, all owned by the member in the original workspace. The separate
catalogue run recovered the real model facts with the visible retry after
the forced read failure. No browser request went beyond the fixture origin.

### Native gateway for the Portal key flow

Keep the existing S-304 to S-306 browser scenario and choose the native
path explicitly with a private HTTPS gateway URL. Use the real Rust
gateway, the Go control plane's authenticated machine endpoint, and
PostgreSQL. Reuse the
[native gateway test profile](../../services/control/internal/modules/distribution/gateway_integration_test.go)
from GW-112, including the loopback tokenizer and text-completion
upstream. Its responses are synthetic. This run qualifies the gateway's
client TLS, routing, admission and durable outcomes for a key created in
the Portal. It does not require a GPU, a cluster or a model download.

Use the [Chat Completions request shape](https://platform.openai.com/docs/api-reference/chat/create)
from the existing endpoint sample. Send serial requests with the key
kept in process memory. Trust the fixture's public CA with
[Node's extra certificate store](https://nodejs.org/api/cli.html#node_extra_ca_certsfile)
and verify the advertised host. Match each gateway response's request ID
to a reported row with the same key, actor, alias, workspace and token
usage. Poll the real read for the asynchronous receipt before inspecting
the browser's call table. The rotated key must have its own history. A
different alias and the revoked replacement must receive their exact
gateway errors and add no receipt. The inaccessible workspace must
refuse both the key list and call history reads.

Keep the protocol fixture available for installations with no native
gateway listener. A successful protocol stub is not a substitute for
this native run. No new dependency is added. The code uses the existing
Node and Rust toolchains, and the already approved Qwen files retain
their Apache-2.0 licence. The loopback upstream supplies test responses
and uses no third-party data.

The local run used the native arm64 macOS Rust gateway built offline
from `9c72e93c`, with the same revision's Go control plane and
PostgreSQL 18.6 in a private container network. The gateway listened
on loopback TLS and verified the dedicated machine endpoint against
its own CA. The first fixture attempt used the root as the endpoint
certificate and Rust refused it. The rerun used a separate signed
leaf with the loopback IP in its SAN and verified both TLS hops.

Chromium issued the initial key, confirmed the member's history and
switched workspaces, then rotated and revoked the replacement. All
five serial completions returned nine tokens each. Their IDs matched
the synthetic loopback upstream dispatch and the scoped Control
receipts. PostgreSQL counted five reported rows with capacity released.
The wrong alias returned 404 `model_not_found` and the revoked
replacement returned 401 `invalid_api_key`, with no extra upstream
completion or persisted call. The replacement still showed its one
success in Portal. Another Portal session saw no calls with the
original key, and both reads for the inaccessible workspace were refused.
