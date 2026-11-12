# Product assistant command service

`New` binds the deterministic command service to the control plane's existing
application projection, session owner and trusted local execution policy.
`Run` takes a server-authenticated
identity, a complete command and an optional cursor, and generates a request
ID. `RunForRequest` accepts the ID assigned by the trusted HTTP dispatcher.
Navigation, application reads and conversation reads resolve the authenticated
workspace again in their owning transactions. A successful PostgreSQL commit
records the built-in assistant actor, real user subject, authorized workspace,
request ID and registered command ID before returning the result.

The English and Simplified Chinese aliases are compiled into the binary.
`Commands` returns exact phrases from those same tables for `en` and `zh-CN`
after checking the current session. It filters by entrance and is empty when
the authenticated context has no workspace. Command execution needs a
workspace until the shared audit owner supports global scope. A command
matches one complete alias after case and space normalization. The service
works offline with fixed routes and typed reads. See the
[entry-point and audit handoff](../../../../../docs/research/app-lane-questions.md)
for the remaining browser, HTTP and confirmed mutation work.

Every admitted command reads the installation decision before dispatch.
An explicit disable returns `ACCESS_DENIED`. A missing source or failed read
returns `SERVICE_NOT_READY`. Both leave the result empty and produce no success
audit. Later commands read policy again. Production binding requires the
[typed configuration and dispatcher handoff](../../../../../docs/research/product-assistant-execution-policy.md).

The reviewed tool overlay selects only the typed read adapters. The generated
catalog derives HTTP metadata from the applications OpenAPI contract and checks
the exact reviewed paths and portal cookie scheme. Regenerate
it with `go -C services/control/internal/modules/productagent generate` and run
the tests to detect contract drift. Tool admission never builds an HTTP request.

Command discovery and execution share the server's risk check. Navigation
uses a fixed route for the current entrance. A read requires the matching
reviewed policy and the existing workspace and resource checks. Sensitive
reads and reversible writes are denied by this dispatcher. See the
[risk tiers](../../../../../docs/research/product-assistant-risk-tiers.md)
and [write confirmation](../../../../../docs/research/product-assistant-write-confirmation.md).

The two assistant reads remove Unicode directional controls and mask known
model keys and gateway credentials in display names. The stored names and ordinary Portal projections
retain their existing values. See [result redaction](../../../../../docs/research/product-assistant-result-redaction.md).

## Offline model qualification

The [qualification runner](testdata/qualify_model.py) uses the existing
evaluation replay to check retained safety evidence. It emits a review
decision with the evidence fingerprints and prerequisite status. Production
qualification still requires a live authenticated gateway route and
positive capability evidence. See the
[replay instructions and acceptance](../../../../../docs/research/product-assistant-model-qualification.md).

## Execution budget reservation candidate

`NewExecutionBudgets` borrows this service's current session, workspace and
installation policy. Its separate trusted policy source supplies an explicit
user decision, stable window and finite execution and token ceilings.
`Reserve` charges installation and global-user counters atomically on
PostgreSQL. Only a successfully committed `BudgetNew` permits the trusted
adapter to start work. `BudgetHeld` means work may already have started;
reconcile it rather than executing again. An error returns no reservation,
including when the commit outcome is unknown.

`Settle` needs the original authenticated scope and trusted usage receipt.
It returns unused tokens once, keeps the execution charged and binds retries
to the same usage and receipt digest. Timeouts and restarts retain holds.
Neither method performs a product action or substitutes for the action's
permissions, success audit or human confirmation.

The [candidate DDL](testdata/execution-budget.sql) is installed only by the
isolated repository test. It is not a production migration. Current command
routes do not bind this budget candidate. A production migration and restore
catalogue, authoritative typed policy source, bounded gateway adapter,
run-loop guard, telemetry and both browser flows must be integrated before
AGT-112 can ship. Do not accept a browser's or model's maximum token estimate.
See the [contract and acceptance](../../../../../docs/research/product-assistant-execution-budgets.md).

Select `PROJECT_ONE_REPOSITORY_ENGINE=postgresql`, set
`PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE` to the protected fixture DSN file,
and select an unused `PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA` of the form
`m20_repository_<32 lowercase hexadecimal characters>` in a disposable
database whose name ends in `_test`. The fixture creates and cleans up that
schema. See the repository fixture prerequisites below, then run:

```sh
GOPROXY=off GOSUMDB=off go -C services/control test -p=1 \
  -tags repository_integration -run '^TestDurableExecutionBudgets$' \
  ./internal/modules/productagent -count=1
```

## Tests

```sh
GOPROXY=off GOSUMDB=off go -C services/control test -race ./internal/modules/productagent/...
```

The `repository_integration` test exercises the same service with real sessions,
application pagination and private conversation projections on PostgreSQL. It
uses the [repository fixture](../../testing/repositoryfixture/README.md) and the
[database prerequisites](../../../../../docs/development/environment.md#database-integration-tests).
The fixture uses a non-default workspace, checks metadata-only audit events,
and forces append and commit refusals. It verifies event and chain rollback,
ordinary portal route compatibility, and cross-tenant denial. The test runs
with seeded publications and no inference gateway.

With the repository fixture variables set, run its exact entry point:

```sh
GOPROXY=off GOSUMDB=off go -C services/control test -p=1 -tags repository_integration \
  -run '^TestOfflineCommandsWithCurrentSessionAndWorkspace$' ./internal/modules/productagent
```

The [authenticated HTTP adapter](http/handler.go) is ready for the identity
dispatcher to mount. The staged route accepts only navigation and reads. See
the [registration handoff](../../../../../docs/research/product-assistant-audit.md#authenticated-http-handoff)
for the constructor, authentication and request ID wiring.

Set `PROJECT_ONE_TEST_DSN_FILE` to the protected DSN of a disposable loopback
PostgreSQL fixture with pgvector. The file needs mode `0600` in a private
directory with mode `0700`. Exercise the real dispatcher with a temporary
registration in a Go build overlay:

```sh
assistant_overlay=$(mktemp -d "${TMPDIR:-/tmp}/assistant-http.XXXXXX")
python3 services/control/internal/modules/productagent/http/testdata/dispatch_overlay.py "$assistant_overlay"
GOPROXY=off GOSUMDB=off go -C services/control test -p=1 \
  -tags 'repository_integration assistant_http_dispatch' \
  -overlay "$assistant_overlay/overlay.json" \
  -run '^TestAuthenticatedAssistantHTTP$' ./internal/modules/productagent/http
```

The fixture uses a synthetic whole-platform license, real TLS, separate
entrance cookies and the standard CSRF route. It checks that an application
cursor cannot cross workspace boundaries and a failed audit append suppresses
the response data. It correlates real command requests with the Admin audit
list in its legacy and opt-in forms, and checks that denied commands leave no
audit event. It also exercises an enabled, explicitly disabled, absent and
unreadable installation policy on both entrances and retries after recovery.
Once the shared dispatcher mounts the adapter, exercise the real policy source
without the overlay.
