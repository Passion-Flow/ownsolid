# Product assistant command service

`New` binds the deterministic command service to the control plane's existing
application projection and session owner. `Run` takes a server-authenticated
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

The reviewed tool overlay selects only the typed read adapters. The generated
catalog derives HTTP metadata from the applications OpenAPI contract and checks
the exact reviewed paths and portal cookie scheme. Regenerate
it with `go -C services/control/internal/modules/productagent generate` and run
the tests to detect contract drift. Tool admission never builds an HTTP request.

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
