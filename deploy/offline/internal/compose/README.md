# Constrained local Compose client

`client.go` opens the explicitly configured Docker CLI and Compose plugin,
checks their immutable version/identity, creates a private per-command client
configuration and runs bounded children against the one pinned Unix Engine.
It does not consume ambient contexts, registries, credentials or proxy settings.
`model.go` owns the closed service/volume/resource/security/transport model;
`runtime.go` independently compares observed images, networks, mounts, peers and
resources. `process.go` performs one journal-authorized process submission and
bounded observation. Scope owns intent, current effective identities, recovery
and acceptance; this package does not decide that an installation is healthy.

`control_replacement.go` creates only the selected absent Control while preserving
stopped peers. `cumulative_control.go` changes only a signed target image, the
unique private generated Control configuration and exact read-only release-view
mount, preserving peers, resources, stores and transport. `control_readiness.go`
calls the existing native probe twice through pinned noninteractive Compose exec,
fencing the exact running process before and after. No shell/program/user/
privilege/stdin/TTY option is caller-controlled. Native probe success is required
by the upgrade owner before activation; ordinary process observation retains its
limited meaning.

Configuration comes from `internal/configuration.Compose` and its protected
operator/env mapping; never add ambient reads. A lost command result requires
scope reconciliation and never permission to repeat create/start automatically.
Private client temporary files are removed only after exact content/ownership
checks; unexpected files remain retained. Run focused package controls with
`go test ./internal/compose`; tagged actual CLI journeys require an explicitly
admitted Engine/scope and remain distinct from metadata/child-process doubles.
See [upgrade contract](../../../../../docs/milestones/m17-offline-compose/cumulative-upgrade.md)
and [operator owner](../../README.md).

`ordered_process.go` orders multi-service ordinary lifecycle by dependency:
PostgreSQL acceptance before Control/gateway start, and application stop before
PostgreSQL stop. It uses the existing typed process budgets, pinned read-only
`pg_isready`, exact process/resource fences and one owning journal intent.
Partial effects never grant whole-sequence replay. See the
[contract](../../../../../docs/milestones/m17-offline-compose/ordered-process-lifecycle.md).
