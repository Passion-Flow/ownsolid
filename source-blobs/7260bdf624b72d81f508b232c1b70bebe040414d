# Middleware health

Registered customer middleware gets read-only health observation. Platform-owned profiles are
refused with `MIDDLEWARE_PLATFORM_OWNED_NOT_SUPPORTED`, so backup, isolated restore and
reconcile operations are refused too: they ran through the offline operator's scope engine,
which ADR 0005 removed. REL-002 brings backup of the control plane back through
`internal/operations/postgresbackup`. Stored records of earlier operations stay in the schema.

[service.go](service.go) admits choices, registration, updates, enqueue and history.
[configuration.go](configuration.go) binds typed policy and the instance profile.
[worker.go](worker.go) and [execution.go](execution.go) run the health probe.
[postgres_observation.go](postgres_observation.go) validates PostgreSQL facts on readback, and
[mysql.go](mysql.go) adds external MySQL 8.4 health.

Focused check: `go -C services/control test ./internal/modules/middleware/...`. Real database
checks carry the `repository_integration` build tag.
