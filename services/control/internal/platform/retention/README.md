# Installation retention maintenance

Retention owns bounded maintenance of the installed identity realm. It is not a filesystem cleaner, object-store garbage collector, backup mechanism or caller-selectable tenant purge. Domain artifact cleanup remains with its domain owner.

## Selected store and lifecycle

[PassSelected](repository.go) accepts a context, the actual selected `transaction.Store` and `config.RetentionPolicy`. It supports the explicitly named PostgreSQL/MySQL dialects and rejects missing context/store, unsupported dialect and invalid policy. All selected keys, mutations and audits belong to one real transaction.

The pass locks and reads `installation_state`, resolves the installed tenant/Workspace and checks the tenant is active. A valid unbootstrapped installation is a no-mutation pass; a missing or contradictory installation is an error. Cutoffs use the database clock. Tenant-bearing invitations and audit rows are scoped to the installed tenant; user/session/reset/rate/idempotency records belong to its global identity realm rather than an invented per-request tenant.

[RunSelected](worker.go) executes on the configured interval and returns maintenance failures to its owner. It treats only genuine cancellation-only outcomes as normal shutdown, preserving uncertain commits. [Server composition](../../../cmd/server/runtime_lifecycle.go) first runs an admitted startup pass, then owns and joins the periodic worker. Legacy `Pass`/`Run` wrappers retain their PostgreSQL compatibility contract; new composition should use the selected-store entrance.

## Data order and recovery

The repository expires pending invitations, revokes obsolete reset secrets and cancels obsolete mail while clearing encrypted payload/lease material. It then removes old terminal mail before unreferenced reset/invitation records, ended sessions, expired rate buckets, expired idempotency records and aged audit events. Accepted invitations and live authority are not indiscriminately deleted.

Each operation selects bounded ordered keys with `FOR UPDATE SKIP LOCKED`, then mutates only those keys and rechecks eligibility. The configured batch bound applies per operation, not as one shared cap across every table. Individual invitation/mail audits and the summary `system.retention` event commit with the actual changes; an audit failure rolls back the pass. Contended rows can be considered by a later pass.

An uncertain commit is retained as uncertainty. Do not automatically replay that transaction or fabricate rollback. A later independently admitted pass evaluates current eligible state. Recovery must also respect foreign-key order and preserve live reset/session/outbox evidence.

## Partitions

[partitions.go](partitions.go) keeps the monthly partitions of append tables on PostgreSQL ([ADR 0029](../../../../../docs/decisions/0029-time-partitioned-append-tables.md)). Each worker pass, whether or not the installation is bootstrapped, it calls `manage_time_partitions` for each table in its own transaction: partitions reach three months ahead and a month past its retention is dropped whole. A lock it could not take is retried at the next pass. The tables are `audit_events`, `training_events`, `model_preparation_events` and `node_task_history`. MySQL keeps row deletion.

## Policy and maintenance

[RetentionPolicy](../config/policy.go) supplies interval/timeout in seconds, batch size, mail/reset/invitation/session/audit ages in days and rate grace in seconds under `runtime.retention`. The [control YAML example](../../../../../config/examples/control/control.example.yaml) is the deployment input; the configuration owner validates ranges. The package does not independently read environment or spawn a daemon.

Changes to retention eligibility require paired dialect statements, table constraints, current identity behavior and transactional audit assertions to move together. Never substitute host-clock strings or path deletion for this selected-store maintenance contract.

## Verification boundary

[repository_integration_test.go](repository_integration_test.go) contains `TestActualPairedRetentionRuntime` behind `repository_integration`; it needs explicitly prepared product-schema databases. The [paired retention runbook](../../../../../docs/milestones/m20-mysql/retention-runtime.md) and [runtime driver](../../../../../tools/quality/mysql/retention_runtime.py) own the fixture and readback procedure, including nondefault bounds, rollback, commit-ack uncertainty, contention and worker joining. An untagged package build has no equivalent retention SQL test and must not be reported as paired-engine acceptance.
