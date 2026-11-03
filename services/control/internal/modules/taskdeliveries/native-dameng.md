# Native Dameng task delivery repository

Wave6 source contribution, 2026-10-24. This extends the selected transaction
repository; it does not select a database, start NATS, grant privileges, run DDL,
or establish native qualification. Central composition and acceptance remain
separate. PostgreSQL/MySQL SQL and their task transport semantics are retained.

## Ownership and behavior

`repository.go` selects an explicit third native statement. UTC microsecond time
comes from `SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))`; native cursor years, location
and precision match the ODBC boundary. The cursor is rejected without rounding.
History uses the existing validated pagination policy and a checked literal LIMIT;
all business values remain parameters. Native locks use `FOR UPDATE` in writable
transactions. Binary canonical event bytes and SHA-256 bytes stay binary; native
broker/consumer sequences use canonical decimal text so uint64 values do not
overflow signed BIGINT. No sequence ordering relies on textual ordering.

`RecordTaskQueuedWithin` still borrows the node owner's actual transaction and
current source scope. Publishing/consumption retains real JetStream, NKey/TLS,
immutable delivery identity, receipt readback, current task authority and the
existing operator-controlled retry. A SQL error immediately exits its callback;
the native driver owns poisoned connection cleanup. Unknown commit fences all
later worker stages. Concurrent cancellation preserves commit and other retained
failures. No success DTO, positive ACK or automatic mutation replay follows an
unknown result. Explicit transport recovery reconciles original event/delivery
identity; it never reopens or executes the physical node task.

`contribution_task_middleware.go` contributes `taskdeliveries_v2` through
`taskDeliveriesFragment(current)` with dependencies `foundation_v1` and
`nodes_components_v2`. It creates three tables, three indexes and nine native
triggers. Conditional broker-coordinate uniqueness makes all three virtual key
columns NULL when the sequence is absent. Exact ASCII canonical event bytes,
source task state, SHA-256, immutable receipt/state transitions, original retry
identity and retained-history deletion remain database-enforced. Runtime reads
the same trigger definitions via a read-only migration projection, compares
status/type/event and full `DBMS_METADATA.GET_DDL`, and refuses drift. It does
not apply migrations or accept readiness from column existence alone.

## Configuration and central integration

Keep the existing taskbus typed configuration/env-file mapping and owning
connection configuration. There are no new production environment variables or
native compatibility switches. The central owner must register both source
fragments in dependency order, retain exact runtime grants, activate the real
native Identity/Workspace/node owners, and retain its maintenance/epoch admission
around serving startup, worker lifecycle and isolated restore. A module dialect
branch does not replace current grants, catalog verification or that shared
admission. The fragments add no new global epoch owner or session lock.

Required native packages are `UTL_RAW` and `DBMS_CRYPTO` with SHA-256 for schema
guards, plus `DBMS_METADATA` for exact readback. The operator provisions approved
packages/privileges; runtime neither creates them nor grants itself access.
Current catalog/grant verification must reject a missing capability. Native
driver row/cell/result/SQL caps must cover this module's bounded statements and
the retained-state integrity scan; a cap violation is failure, never a partial
successful readiness result. Full retained-state verification currently uses
the existing complete scan and remains subject to the native result budget.

## Prepared verification, not executed

Pure tests in `native_repository_test.go` cover exact statement/parameter
selection, full native time bounds, abort after a scripted unique failure, and
unknown/cancelled worker outcomes. Migration contribution tests cover real
fragment shape/dependencies, complete conditional NULL keys and non-weakenable
trigger definitions. They do not qualify SQL execution.

`TestActualDamengTaskDeliveryRecovery` requires a fresh provisioned full native
catalog and a real bounded NATS fixture. Its private connection file comes from
`PROJECT_ONE_DAMENG_EVENTS_CONFIGURATION`; schema must match
`PO_EVENTS_[A-F0-9]{16}_TEST`. This test-only selector has no default, is read once
at test entry, and must reference a protected native connection document accepted
by `dameng.LoadConnectionConfiguration`. Never put secret values in the variable.
The original `taskbusfixture` owns its explicit native binary/hash, private TMPDIR,
resource, evidence and shutdown inputs. No production broker is admitted.

The test seeds synthetic identities in an empty schema, issues real sessions,
enrolls a node, records an outbox in the node transaction, loses only the scripted
SQL commit acknowledgement during cancellation, checks original SQL/broker state,
recovers explicit publication, restarts the real broker, consumes/ACKs, verifies
physical task independence, fences restore, checks retry idempotency, paginates,
rejects immutable receipt mutation and revoked access. Actual DB commit and
readback are real; the acknowledgement-loss wrapper is not real wire-loss proof.
Failed native connections are read back only through fresh transactions.

After coordinator resource admission and full central integration, from
`services/control`:

```sh
GOMAXPROCS=1 GOMEMLIMIT=512MiB go test -p 1 ./internal/modules/taskdeliveries ./internal/platform/database/dameng/migrate -run 'TestNativeDelivery|TestTaskMiddlewareNative|TestTaskDeliveryNativeTrigger' -count=1
GOMAXPROCS=1 GOMEMLIMIT=512MiB CGO_ENABLED=1 go test -p 1 -tags 'odbc_manager,repository_integration,dameng_repository_integration,task_events_native_integration' ./internal/modules/taskdeliveries -run '^TestActualDamengTaskDeliveryRecovery$' -count=1 -timeout=4m
```

The native test drains service, broker and physical SQL owners; it retains the
exact synthetic schema and minimal fixture evidence for coordinator-controlled
cleanup. It does not erase database failure evidence, purge shared caches, or
claim cleanup when a native physical owner remains. Fresh/upgrade DDL, current
runtime grants, maintenance epochs, actual lost-wire/cancellation recovery,
concurrent native contention, native retention/poison extremes and full serving
HTTP/worker composition remain integration/target gates. No tests, Go tools,
builds, services or Git commands were run in this source-only lane batch.

Native syntax was checked against official [DM functions](https://eco.dameng.com/document/dm/zh-cn/pm/function.html),
[UTL_RAW](https://eco.dameng.com/document/dm/zh-cn/pm/utl_raw-package),
[DBMS_CRYPTO](https://eco.dameng.com/document/dm/zh-cn/pm/dbms_crypto-package.html)
and [DDL](https://eco.dameng.com/document/dm/zh-cn/pm/definition-statement.html).
Documentation review is not executed vendor qualification.
