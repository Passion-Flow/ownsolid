# Versioned bounded workflow execution

This module owns validated graphs, drafts/publications, conversations, runs and node traces. The current graph families are explicitly bounded: a retrieval/answer path and a signed built-in tool path. They are not a general unrestricted DAG scheduler.

[graph.go](graph.go) validates the graph families; draft/publication paths produce immutable version/digest bindings. [configuration.go](configuration.go) exposes current availability and policy. Executor, execution-lease, tool and finish paths borrow [knowledge](../knowledge/README.md), [plugins](../plugins/README.md) and [distribution](../distribution/README.md), retaining current identity/Workspace authority at the execution boundary. Cancellation, recovery and retention have explicit state transitions; [lifecycle.go](lifecycle.go) owns joined worker shutdown.

Migration 000013 stores workflows, versions, conversations, runs, traces and mutation intents. A published graph is not permission to ignore a later revoked grant or disabled source. Lease expiry, interruption and unknown terminal commit must retain their existing reconciliation rather than produce a second answer as an automatic retry. Caller-private conversations and application projections use this same owner.

Configuration enters through the control Workflows and dependent Knowledge/Plugins policies. Synchronize [M13](../../../../../docs/milestones/m13-workflows), [frontend](../../../../../web/README.md), [HTTP contracts](../../../../../contracts/http), graph consumers and [quality harnesses](../../../../../tools/quality/workflows). If this module is split later, preserve draft/version ownership separately from mechanical executor transport without splitting atomic run/terminal state.

[profile.go](profile.go) selects the explicit tool-only mode when Knowledge is disabled and Plugins is enabled. Startup verifies the same selected SQL/identity owner and the running plugin lifecycle; new drafts contain start/tool/end with no preselected grant. Question limits use the Workflow policy in this mode. Enabled Knowledge still requires its actual processor and Distribution dependency; an unavailable configured encoder never triggers fallback. Old schema1 graphs remain stored, cannot publish/execute while Knowledge is disabled, and their successful private outputs are withheld with `KNOWLEDGE_UNAVAILABLE`. Configuration projection retains its atomic read path without a database-to-lifecycle mutex edge. See the [tool-only contract and integration gate](../../../../../docs/milestones/m14-plugins/tool-only.md).

[tool_only_repository_integration_test.go](tool_only_repository_integration_test.go) covers actual signed SQL execution and explicit cancellation/recovery/history fixtures without a knowledge processor. The shared repository fixture now owns the canonical MySQL migrations; this module must not replay already-promoted candidate DDL. The [bounded runner](../../../../../tools/quality/workflows/tool_only_repository.py) retains the existing resource admission, per-leaf database isolation and cleanup owners. Test source and syntax checks do not establish executed SQL acceptance.

Focused native check: `go -C services/control test ./internal/modules/workflows`. Real SQL publication/lease/revocation, interpreter/process failures, browser conversation flows and cancellation/restart need their distinct fixtures. Future graph variants require their own complete admitted contract and tests.

The native Dameng repository now owns explicit SQL in [sql.go](sql.go), recovery
and retention queries, plus CLOB/limit/cursor binding in [dameng.go](dameng.go).
It targets the frozen `applications_v2` contribution; it does not select a
compatibility mode or translate PostgreSQL/MySQL text. UTC microsecond cursors
outside the native timestamp range are rejected. Current identity, membership,
workflow, conversation and lease fences still precede mutations. Exact-row writes
and their audit/intent must all succeed in the same transaction. Unknown commit
outcomes survive simultaneous cancellation; the worker does not perform another
tool/model operation or a compensating terminal write. Reconcile the original key
in a fresh transaction; abandoned durable runs follow bounded lease recovery.

[dameng_integration_test.go](dameng_integration_test.go) is an actual native SQL
workflow/plugin service harness, gated by `cgo`, `odbc_manager` and
`dameng_integration`. It requires a merged native identity/Workspace/model owner,
the complete verified schema catalog and an exclusive already migrated empty
Linux native target. Supply the two documented test inputs from
[dameng_integration.env.example](dameng_integration.env.example); use the supported
pinned connector configuration with at least 32 result columns. The harness
creates only synthetic data, joins its service workers and closes the kernel.
It leaves that exact disposable schema for the operator to retain diagnostics,
verify no active consumers, and remove via the existing native schema owner.
Do not rerun against a nonempty fixture or discard an unknown outcome. There is
no implicit fixture reset, driver install, server startup or cloud provisioning.

Coordinator commands after integration:

```sh
go -C services/control test ./internal/modules/workflows ./internal/modules/plugins -run '^TestDameng' -count=1
go -C services/control test -tags 'odbc_manager dameng_integration' ./internal/modules/workflows -run '^TestActualDamengWorkflowPlugin$' -count=1 -timeout=150s
```

The actual harness covers signed import/install/grant, schema2 execution and
traces, caller privacy, pagination, immutable publication, original-key replay,
commit-acknowledgement loss, service and unique-index one-active-turn fences,
durable cancellation, explicit expired-lease recovery, terminal notice production,
revocation withholding, uninstall references, retention and joined offline restore.
It rejects schema1 execution without Knowledge; it does not qualify a native
knowledge backend or real inference. These commands were **not executed** in the
source-only lane. Native compilation, SQL/driver behavior, fault injection and
complete PostgreSQL/MySQL regression execution remain separate coordinator gates.

## Current cumulative M13 source integration

The explicit `workflows` profile extends accepted knowledge through forward
account19, six schema1 workflow tables and `upgrade-workflows`. KnowledgeRuntime
retains knowledge plus all inherited SSO/lifecycle/four-store owners. Current
workflow SQL uses the original schema1 fields; later plugin/tool result columns
are absent and schema2 is refused. Startup owns the joined workflow executor;
readiness and exact HTTP routes use this owner. Common restore appends workflow
invalidation last in its existing transaction. The expected80table/19entry
catalog, role-preserving interrupted upgrade and late-owner rollback require
actual current SQL evidence. See [the current integration](../../../../../docs/execution/mvp/m13-cumulative-integration-20261026.md); source availability is not acceptance.

## Current cumulative M14 source boundary — 2026-10-27

`WorkflowsRuntime` composes schema1 and explicit schema2 in the bounded `plugins`
profile. Account20 enables the retained canonical schema2 SQL adapter; the
independently usable M13 `workflows` profile keeps its schema1 adapter/catalog.
Tool-only startup requires a joined real Plugins owner and no encoder/gateway;
it starts Start→Tool→End with no implicit version grant. Saved schema1 data stays
unchanged and its model results are withheld while Knowledge is disabled.
Mixed schema1 model execution retains real Go/Rust/vector/CPU evidence. Crash,
lease expiry, exact replay, shutdown and common restore never auto-reinvoke.

Implementation and current local execution evidence are recorded separately in the
[current M14 handoff](../../../../../docs/execution/mvp/m14-closeout-current.md).
Physical qualification and real manufacturer approval remain separate.
