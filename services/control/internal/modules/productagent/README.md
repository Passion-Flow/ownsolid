# Product assistant command service

`New` binds the deterministic command service to the control plane's existing
application projection and session owner. `Run` takes a server-authenticated
identity, a complete command and an optional cursor for the current read. Its
navigation results contain fixed local paths scoped to the current entrance.
Application and conversation reads take the workspace from the authenticated
context and reuse the projection owner's current session and membership checks.

The English and Simplified Chinese aliases are compiled into the binary.
`Commands` returns exact phrases from those same tables for `en` and `zh-CN`
after checking the current session. It filters by entrance and omits workspace
reads when there is no workspace in the authenticated context. `Run` still
checks current resource permissions before reading. A command must match one complete alias after case and space normalization. A
model, an external client, a shell, and write adapters are absent from this
service. The browser and HTTP entry points are integration work tracked in the
[APP lane boundaries](../../../../../docs/research/app-lane-questions.md).

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
The fixture seeds published metadata and never starts the workflow executor or
an inference gateway.
