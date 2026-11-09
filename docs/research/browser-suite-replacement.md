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
| `TestM20ModelImportRecoveryBrowser` | S-201 Model library: upload, lost replies, resume and retirement have Playwright flows in `models.spec.ts` and `live-models.spec.ts`. The Playwright cancellation case holds the first chunk and verifies the browser aborts it before discarding. The old harness also reads scoped audit rows and retained file hashes directly, which still needs an installation-level check. |
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
