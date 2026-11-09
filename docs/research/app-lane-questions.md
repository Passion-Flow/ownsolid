# APP lane integration boundaries

The application module owns portal projections. Its existing reads borrow the
workflow owner for configuration and use the workflow tables as projections. A
second owner for workflow execution or conversation mutations would bypass the
existing publication, lease and authority fences.

| Items | Required integration | Safe scope in this lane |
| --- | --- | --- |
| APP-101, APP-102 | A browser editor and a versioned workflow graph and runtime that admit the same nodes and edges | Preserve the two inherited graph schemas and their behavior. |
| APP-103, APP-104, APP-105, APP-107, APP-111 | Workflow state, execution, tool invocation and run history in `modules/workflows`, with browser controls | Keep one workflow owner and no parallel execution state. See [durable human approval](workflow-human-approval.md). |
| APP-106, APP-108, APP-109, APP-110 | Workflow version and conversation writes, publication, gateway or portal UI, depending on the item | Keep application projections read-only. Use their current authority for assistant reads. |
| AGT-101, AGT-109 | The console and portal command bar, panels and route integration | Keep command destinations aligned with the currently shipped routes. |
| AGT-102 to AGT-108, AGT-110 to AGT-112 | A session-authenticated assistant entry point, audited write adapters and gateway admission | Implement owned components where they can be tested independently. Enable a write only when the same transaction can enforce current permission, idempotency and audit attribution. |
| APP-201 to APP-207, AGT-201 to AGT-203 | Workflow execution or a shipped product-assistant entrance and the preceding P1 authority guarantees | Leave release items open until their P1 prerequisites are in place. |

The narrow integration option is additive ownership of workflow, tool and UI work
by their respective lanes. Moving those responsibilities into this lane would
require changing the explicit path boundary. Replacing them with an application
projection side channel would risk competing ownership of the same rows and
would still leave the release without a usable browser path.

## Workflow editor handoff for APP-101

The Web lane owns the editor entry in `web/apps/console/src/router.tsx` and
`navigation.ts`, plus the portal entry if the screen plan enables it. It can
start from the generated `web/packages/api/src/gen/workflows.ts` contract.
The existing workflow resource has an admin and a workspace-scoped portal
route in [the workflow OpenAPI](../../api/openapi/v1/workflows/openapi.json).

- Read the editable graph and revision with `adminGetWorkflow` or
  `portalGetWorkflow`. Save through the matching `PATCH` with `UpdateWorkflow`
  containing `graph`, `expected_revision`, `name`, `enabled` and `reason`.
- Use the scoped `/validations` and `/publications` operations against the same
  saved revision. Map node and edge issues onto the graph and preserve the
  local draft when the server refuses a stale revision. Publication keeps the
  existing CSRF and idempotency headers.
- The workflow owner must extend
  [graph.go](../../services/control/internal/modules/workflows/graph.go) and
  [publications.go](../../services/control/internal/modules/workflows/publications.go)
  alongside the versioned contract before the editor can ship free connections.
  Current schemas 1 and 2 admit bounded linear pipelines. Keep old published
  graphs executable and specify directed edge endpoints, valid port bindings
  and executable topology for the new schema. Use the same validation on save,
  validation and publish within their respective draft and executable limits.

Acceptance needs an editable graph that persists on reload, backend validation
of invalid connections, a visible revision conflict that preserves unsaved
changes, and publication and execution of the saved graph without egress.

## Approval handoff for APP-103

The workflow runtime owner starts at
[executor.go](../../services/control/internal/modules/workflows/executor.go),
[execution_lease.go](../../services/control/internal/modules/workflows/execution_lease.go),
[lifecycle.go](../../services/control/internal/modules/workflows/lifecycle.go)
and [run_history.go](../../services/control/internal/modules/workflows/run_history.go).
The existing run contract has no waiting state or approval decision. Propose a
versioned graph node and an additive contract in
[workflow OpenAPI](../../api/openapi/v1/workflows/openapi.json). The run read
exposes a pending approval summary and the caller's decision eligibility. An
authenticated `POST /runs/{run_id}/approvals/{approval_id}/decisions` under
the same admin or workspace workflow prefix takes a decision, expected
approval revision and reason with the existing CSRF and idempotency headers.
A retry with the same key returns the recorded decision. A conflicting
decision returns a revision conflict.

A new migration must add a nonterminal waiting state, its bounded checkpoint
and approval record. The one-active-turn constraint must still cover a waiting
conversation. A paused run relinquishes its worker lease, retains its pinned
graph version and authorizing principal, and resumes through durable work
claiming with a fresh lease. The worker must recheck the retained run authority
and current scope after restart without persisting browser credentials. The
approver authorizes the decision, and the executor preserves the initiating
principal's authority on downstream actions. Recovery, explicit cancellation
and offline restore must settle pending approvals within their existing
transaction boundaries. The Web lane wires the decision controls to the
versioned API and shows waiting, denied, cancelled and resumed states.
See [restart and retry acceptance](workflow-human-approval.md).

## Portal conversation and playground handoff

APP-109 search has a registered backend path on PostgreSQL. DEV added `q`
to `ListApplicationConversations` and its generated client in `9cb3d16a`.
Portal still needs the search field with cursor reset on term, session and
workspace changes. Rename and
export still need workflow-owned title storage, revision and permission
checks, and versioned operations. See [conversation search](application-conversation-search.md).

The current Portal router has no S-302 conversation, S-303 application or
S-305 playground route. The build plan reserves S-305 but does not define
its flow. To resume APP-110, the Portal owner defines and wires the run
screen, the gateway owner provides scoped admission for the model run, and
the workflow owner exposes the pinned publication flow for the app run.
Reuse that execution path from the existing application projection. AGT-109
can add result links to conversations and applications once their routes
and resource identifiers are fixed.

## Assistant catalog handoff for AGT-103

The [applications OpenAPI](../../api/openapi/v1/applications/openapi.json)
references `PortalSession` on `ListApplications` and
`ListApplicationConversations`. The assistant generator requires that scheme's
declared cookie type, location and name. The generated metadata contains only
the two reviewed reads. It does not carry cookies or perform HTTP calls. See
[catalog verification](product-assistant-tool-catalog.md).

## Audit handoff for AGT-107 and AUD-101

The scoped command service uses SCI's
[PostgreSQL writer](product-assistant-audit-writer.md) inside navigation and
application read transactions. The [assistant HTTP adapter](../../services/control/internal/modules/productagent/http/handler.go)
accepts the authenticated identity and the same server request ID returned in
`X-Request-ID`. The identity HTTP owner registers the route after the shared
CSRF and license admission. The API owner publishes the operation after that
registration. The opt-in Admin audit response exposes the verified assistant
actor. The offline export needs separate attribution acceptance.
Workspace-free navigation requires a shared global-scope decision. See the
[registration handoff and acceptance cases](product-assistant-audit.md#authenticated-http-handoff).

## Repository test gate

The previous console run on isolated ports had 18 failures, 1013 passes and 23
skips. The failure locations, relative to `web/apps/console`, were as follows.

| Test | Failed cases |
| --- | --- |
| `e2e/account.spec.ts` | Shared account menu and command menu test at `web/packages/shell/testing/account-flows.ts:31` |
| `e2e/models.spec.ts` | Line 82, failed model read and retry |
| `e2e/node-page.spec.ts` | Lines 67, 106 and 140, tasks, activity and missing node retry |
| `e2e/nodes.spec.ts` | Lines 47 and 94, server-side list filters and unavailable figures |
| `e2e/overview.spec.ts` | Lines 156, 181, 215, 228, 240 and 259, setup, quick actions, partial failures and idle session |
| `e2e/resource-pages.spec.ts` | Lines 74 and 128, missing person and connector retry |
| `e2e/services.spec.ts` | Lines 78 and 233, failed service read and concurrent revision |
| `e2e/users.spec.ts` | Line 107, failed user list retry |

Run representative browser cases after the console build from
`web/apps/console`.

```sh
pnpm exec vite build
pnpm exec playwright test e2e/account.spec.ts e2e/models.spec.ts \
  e2e/node-page.spec.ts e2e/overview.spec.ts e2e/services.spec.ts \
  --workers=1 --grep 'reaches the page from the account menu|shows a failed read and retries it|Tasks says when there are none|a new installation is walked through setup|two people acting at once'
```

For current revisions, set `OWNSOLID_PLAYWRIGHT_PORT_OFFSET` to move ports
4310 and 4311 onto an unused loopback pair. Keep browser comparisons serial
with one worker. The historical baseline predates that setting and used a
temporary Playwright configuration with the same settings on both revisions.

All 18 previously failing cases passed on both `609d5198` and `356b241b`
with one worker and the same isolated port. A gateway case shared one failure
title and also passed in each 19-case selection. The Web, API and package trees
were identical. The previous failures did not reproduce under that comparison.
This does not establish the cause of the original run or a green full suite.

Run the two Agent training cases from the repository root on macOS.

```sh
export TMPDIR="$(cd "${TMPDIR:-/tmp}" && pwd -P)/"
GOPROXY=off GOSUMDB=off go -C services/agent test ./internal/training \
  -run '^(TestShutdownLeavesTheContainerAndTheNextProcessAdoptsIt|TestARevokedAttemptStopsItsContainer)$' \
  -count=3 -parallel=1 -v
```

The shutdown case failed twice on `609d5198` and passed three times on the
lane. The revocation case failed once on the baseline and twice on the lane.
The observed failure signatures included `TRAINING_OUTPUT_CHANGED` before
container start and cancellation returning a receipt. Both cases failed
without the APP commits. The Agent, API and Go workspace trees were
identical at the time. After syncing `aa5e712f`, the same cases each passed
five serial repetitions. The complete Agent training and model-preparation
packages passed with one test worker. This resolves the two reproduced
failures. The repository-wide test gate remains separate from these checks.

The AGT-107 HTTP adapter passed its disconnected PostgreSQL and HTTPS test,
`make check` and `make build`. The full browser suites passed on isolated ports
with three workers. The first test run inherited the SQL fixture's private
umask and restored a model at mode `0400` instead of `0444`. Under the normal
`022` umask, the installer case passed three times and the remaining Go suites
passed. The Web unit suites also passed.

The earlier macOS gate was blocked by
`backend_tests::actual_connection_refusal_opens_only_its_backend_circuit`,
introduced by `1b293a01`. On the unchanged gateway tree from `origin/main`,
both the full suite and a serial focused run returned `upstream_timeout`
instead of `upstream_unavailable`. A separate local socket probe on the same
machine timed out when connecting to a loopback socket held by `bind()` without
`listen()`. Releasing the socket gave immediate `ECONNREFUSED`. The remaining
66 Rust tests passed, with six ignored. The Gateway lane fixed the fixture in
`6f4e45b0`. It closes a reserved listener before testing refusal and checks
recovery on the same address. The assistant HTTPS and PostgreSQL fixture
passed again after syncing that fix.

The macOS rerun passed `make check`, `make build` and the full `make test`
with the normal `022` umask, isolated browser ports and three browser workers.
Chromium passed 1,046 Console and 387 Portal tests, with 28 skipped. The
gateway suite passed all 67 enabled tests. After syncing the concurrent
model-preparation and Nodes changes, the affected builds, HTTPS fixture,
Web checks and 84 Nodes-related browser cases passed.
