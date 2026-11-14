# Control service

The Go control service is the product's business authority. One process serves the Portal and Admin entrances, owns the selected SQL installation and runs the domain workers it constructs. It also exposes explicitly selected operator commands; those commands are not general-purpose browser privileges.

## Entrypoints and composition

The [server composition guide](cmd/server/README.md) documents process ownership, protected operator inputs and shutdown. Use the [operations index](internal/operations/README.md), [configuration owner](internal/platform/config/README.md) and [selected SQL guide](internal/platform/database/README.md) when changing those boundaries.

- [cmd/server/main.go](cmd/server/main.go) parses protected configuration and command-specific inputs, opens the selected database and dispatches serve/migration/bootstrap/storage/recovery operations.
- [selected_database.go](cmd/server/selected_database.go) owns the selected PostgreSQL/MySQL pool, installation lease and schema readiness.
- [runtime_lifecycle.go](cmd/server/runtime_lifecycle.go) constructs runtime dependencies and joins cancellation, stops, workers and closers.
- [identity/http/handler.go](internal/modules/identity/http/handler.go) currently constructs and routes most business domains. This historical location is a recorded composition follow-up, not a requirement that new unrelated orchestration belongs to identity.
- [internal](internal/README.md) distinguishes domain, platform, privileged operations and test support.

## Configuration and persistence

[platform/config](internal/platform/config) owns the main typed configuration, explicit environment-file overlays and protected component references. Start from [control examples](../../config/examples/control) and update the matching schemas, Compose/Helm and validation together. Ordinary serving does not obtain adjustable policy from arbitrary ambient environment variables.

PostgreSQL is opened through [platform/database](internal/platform/database); MySQL uses mysqlstore/mysqlmigrate and a complete activated catalog. [transaction](internal/platform/database/transaction) borrows the selected pool and accepts repository-owned vendor SQL. It does not own migration, pool lifetime or SQL rewriting.

Business rows, audit and outbox transitions share their owning transaction. Artifact bytes live in declared private domain stores with identity/digest binding; SQL rows alone do not prove bytes are present. Object storage, MongoDB sources, AWS inventory, task bus, model cache and Registry have distinct configuration and authority owners.

## Failure and recovery

Session/Workspace authority is rechecked within the transaction. Worker results are accepted only after current state, scope and input bindings are verified. Commit-unknown, ambiguous external results and revoked/stale leases retain explicit recovery paths. Readiness observes schema and runtime failures; new workers must register their stop/join obligations with runtime_lifecycle.

Serving follows the explicitly selected browser transport/address configuration. The actual explicit HTTP account slice and restricted-role composition have scoped local evidence; the complete address/transport matrix and native CLI/installed deployment remain separate gates. See the [current serving evidence](../../docs/execution/mvp/fullserve-runtime-role.md).

## Change and verification checklist

For a domain change, update its service/repository/http, exact HTTP or worker contract, generated types/errors/messages, schema/selected SQL, configuration/deployment projection and real negative/recovery checks. Retain the distinction between a controlled test, real SQL, actual browser, full server and target environment.

Focused example after pinned-toolchain/external-cache admission:

    go -C services/control test ./internal/platform/database/transaction ./internal/modules/workspaces

Integration files have explicit build tags and fixture requirements. Use the owning milestone's harness/runbook; do not treat skipped or unselected tests as acceptance. The [backend audit](../../docs/architecture/current-product/backend-audit.md) identifies current dependency exceptions; [STATUS](../../docs/STATUS.md) owns completion status.
