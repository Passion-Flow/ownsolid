# Signed built-in tool capabilities

This module owns signed built-in tool versions, installation state and current Workspace grants. The implemented executor is a bounded built-in capability path; it does not load arbitrary downloaded plugin code.

[imports.go](imports.go), [service.go](service.go) and [model.go](model.go) admit trusted version capsules, digest/key identity, ABI, executor and capability declarations. Grant/reference/list paths manage visibility and workflow use. [tool.go](tool.go) checks the same identity/Workspace/model owners, current actor, trusted installed version and current grant before invoking the actual tool. Recovery and retention preserve incomplete imports and referenced versions.

Migration 000014 stores versions, grants, mutation intents and workflow references. Imported, installed and uninstalled states are distinct; a cached signature result does not replace current grant or installation checks. Workflow publication and execution must agree on the version identity they reference.

The [explicit tool-only workflow profile](../../../../../docs/milestones/m14-plugins/tool-only.md) uses the existing model-directory SQL executor without a knowledge encoder or gateway. Workflow startup verifies this module's current selected repository and lifecycle. Import/install/current exact grant, actor/Workspace/model checks, atomic invocation/terminal audits and history rechecks remain the same. This introduces no arbitrary plugin executable and does not change the bounded directory-result contract. Shared configuration and deployment source integration is present; current cumulative execution has its own source-bound evidence; historical platform receipts remain separate.

Use the control Plugins configuration and policy for protected trust/input stores and limits. Synchronize [plugin contracts](../../../../../contracts/plugins), [frontend](../../../../../apps/web/packages/plugins), [M14](../../../../../docs/milestones/m14-plugins), workflow tool nodes and [quality harnesses](../../../../../tools/quality/plugins). Broader plugin ecosystems belong to separately admitted variants; do not add an unrestricted executor as a shortcut.

Focused native check: `go -C services/control test ./internal/modules/plugins`. Real SQL import/grant/revocation races, signature failures, retained references and actual built-in invocation require their selected fixtures. Successful capsule parsing alone does not establish current execution authority.

[dameng.go](dameng.go) owns the explicit native SQL contracts selected at every
repository call. Capsule bytes and request MACs remain binary; JSON manifests use
CLOB strings. Lists preserve tenant/Workspace/current installed-grant visibility,
reject unrepresentable native cursor timestamps and render only validated bounded
integer limits. Locked native reads use `FOR UPDATE`; the caller retains the
existing identity/Workspace/version/grant lock order and commit authority.

Native uninstall and offline restore invalidate grants one locked key at a time
with a new Go-generated UUID per row. Any SQL execution error exits immediately;
no query is attempted in the poisoned transaction. Native reference scans inspect
all three node positions in the versioned schema2 start/tool/end contract using
`JSON_VALUE`, retaining draft, publication, live conversation and active-run
references and their deterministic digest. A future graph family must extend this
query with its contract. The target must support the documented JSON_VALUE
semantics; no compatibility fallback or alternate result is substituted.

Unknown commits retain their typed outcome even when joined with cancellation.
Imports/install/grants/uninstall reconcile the original operation key in a new
transaction; the repository does not retry after a uniqueness or native SQL error.
Exact-row audit/intent/version/grant/publication writes are checked before commit.
[dameng_test.go](dameng_test.go) contains binding/error/fail-closed cases; the
[workflow native harness](../workflows/dameng_integration_test.go) executes the
real signed plugin and model-directory path with the same repository owner.
See its [run requirements](../workflows/README.md) and test environment example.
These sources have only formatting/static verification in this lane: target SQL,
compilation and PostgreSQL/MySQL regression gates are not recorded as passed.

## Current cumulative M14 source boundary — 2026-10-27

The explicit `plugins` profile composes this existing signed capsule/current
caller SQL authority with the same identity, Workspace and model owners. The
server starts Plugins before Workflows and joins them in reverse order. Routes
and readiness require the selected enabled owner; M15 applications and later
owners remain closed. No executor, broker, key service or downloaded code is
added. The real builtin model-directory executor supports a successful empty
current-caller result. Knowledge=false opens no encoder/gateway; Knowledge=true
retains mandatory actual processor admission. Current grant/trust/model checks,
audit atomicity, exact version pins and private provenance/history stay owned
here. Manufacturer private keys stay outside runtime artifacts.

Implementation and current local execution evidence are recorded separately in the
[current M14 handoff](../../../../../docs/execution/mvp/m14-closeout-current.md).
Physical qualification and real manufacturer approval remain separate.
