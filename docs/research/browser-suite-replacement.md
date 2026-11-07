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
| `TestM20ModelImportRecoveryBrowser`, `TestM20ModelRecoveryTailBrowser` | S-201 Model market                                                          |
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
