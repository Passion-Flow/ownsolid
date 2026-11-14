# Native Dameng nodes and components

This is an implementation and target-check contract, not a native acceptance
receipt. The repositories select explicit `transaction.Dameng` statements through
an admitted `transaction.FromDameng` store. PostgreSQL/MySQL SQL stays independent;
there is no runtime translator or fallback. The whole-platform engine selector
is unchanged. Legacy `pgx.Tx` compatibility entrypoints remain PostgreSQL-only.

## Schema contribution and dependency order

The single owned contribution is
[`contribution_nodes_components.go`](../../platform/database/dameng/migrate/contribution_nodes_components.go):
`nodesComponentsFragment(current)` / `nodes_components_v2`, requiring
`foundation_v1`. It returns five real tables, eight indexes and one trigger.
The coordinator must integrate the domestic `evolution.go` interface and register
this function; that interface is not present in this lane's c3a0c8ff baseline.
No central registration, legacy step1..31 hash, step numbering or existing schema
file is changed here. New tables are authored from current domain contracts;
their source is not copied/replaced vendor DDL.

| Native table | Retained state and exported keys |
| --- | --- |
| `MANAGED_NODES` | Identity, tenant/Workspace, name/status/version, credential hash, protocol, facts, heartbeat, execution profile/capabilities, inference fence. `MANAGED_NODES_PK(ID)`; `NODES_SCOPE_IDENTITY(TENANT_ID,WORKSPACE_ID,ID)`. |
| `NODE_JOIN_TOKENS` | Single-use token hash, target name/profile/scope, actor, created/expiry/consumed/revoked times and enrolled node witness. Scoped FKs to Workspace and managed node; actor FK. |
| `NODE_TASKS` | Immutable request identity and payload, actor/delegated epoch, state/attempt, lease/completed/result hashes, deadlines and terminal time. `NODE_TASKS_PK(ID)`; `TASKS_IDEMPOTENCY_IDENTITY(NODE_ID,IDEMPOTENCY_KEY)`; `TASKS_SCOPE_IDENTITY(TENANT_ID,WORKSPACE_ID,NODE_ID,ID)`. |
| `MANAGED_COMPONENTS` | Desired and observed state remain separate; observed/applied configuration, prior version, current task and verification time. PK(node,component); both scoped node and task FKs. |
| `COMPONENT_LOG_SNAPSHOTS` | Only the latest bounded snapshot, exact scoped node/task, component and receipt time. PK(node,component); scoped node/task FKs. |

All identifiers use canonical UUID checks; hashes remain 32-byte VARBINARY.
Timestamps are TIMESTAMP(6), generated in UTC. CLOB checks require the expected
JSON type and bounded bytes where the existing contract has a bound: task32768,
observation8192, configuration512, logs16384. Profiles and task operations retain
the current full M03–M10 allowlists; this does not authorize their execution.
Derived tasks retain required delegation and the existing immutable command
guard. Its CLOB comparison uses `DBMS_LOB.COMPARE`; the target schema owner needs
the supported package privilege and must execute a trigger check.

The live-name unique index uses `(LIVE_TENANT_ID,LIVE_NAME)` with **both** virtual
columns NULL after removal, preserving multiple retired identities under DM's
composite NULL rules. The one-inflight unique index uses a nullable virtual
`INFLIGHT_NODE_ID`. No PostgreSQL partial index is substituted. Foreign keys are
restrictive; there is no schema cycle or required second-phase ALTER in this
contribution. Model/training/preparation/event owners can reference the exported
scope keys after this fragment, without duplicating the five tables.

## Transaction and native type rules

- Keep actor/source/session, Workspace, installation/tenant, node and task fences
  in their existing order. Native fences use the stronger `FOR UPDATE`; all
  locking callbacks must use readOnly=false. A callback that already holds a node
  must have acquired the exclusive Workspace fence first. Account/model/event
  composition must reconcile this before concurrency qualification.
- Native component upserts read under the already held node lock, then insert or
  update. A SQL error immediately escapes the transaction; never reread/retry a
  poisoned connection. Unique conflicts require rollback and a deliberate fresh
  transaction using the original operation/idempotency identity.
- Unknown commit takes precedence over concurrent cancellation. No enrollment
  credential, lease, success DTO or page escapes a failed `Within`. Independent
  token/node/task reads support explicit reconciliation; no automatic replay.
- JSON parameters are text (or SQL NULL), never binary; binary credentials and
  hashes are unchanged. CLOB CASE branches explicitly use `TO_CLOB(?)`.
- Exact `time.UTC` Location, years1970..9999 and microsecond precision are mandatory
  for bound native timestamps, matching the ODBC boundary. A custom zero-offset
  Location is rejected even when named UTC. Native cursors outside that representation fail with INVALID_CURSOR;
  no cursor truncation occurs. Training/preparation callers must supply an already
  representable authoritative deadline. Lease milliseconds use BIGDATEDIFF,
  FLOOR and an explicit BIGINT result to avoid decimal-to-integer scan ambiguity.
- Pagination binds every business value. Only a validated 1..100 limit produces
  a literal limit+1 through strconv.Itoa; LIMIT bind qualification is not assumed.
- Registry intent uses JSON_VALUE plus the documented JSON_CONTAINS_PATH=1,
  preserving a present JSON-null marker as retained intent.

## Required integration

The account lane must provide native `Identity.Repository`, session issue/auth,
`RecheckOrderedWithin`, Workspace `ReadScopeWithin`/`RecheckScopeWithin` and machine
fences using this same store. A source adapter alone does not close this graph.
Production observer hooks for inference, training, preparation, registry and task
events must also use native SQL and their actual schema contributions. This lane's
test deliberately attaches only nodes/components; it proves no GPU, model worker,
broker, image registry or physical systemd operation.

Coordinator updates belong in the current architecture/capability/change maps,
STATUS/VALIDATION and exact retained source manifests after integrated checks.
Keep product README statements synchronized only with actually exposed behavior;
there is no new whole-platform Dameng deployment choice in this batch.

## Executable checks (not executed by this lane)

After integrating the domestic schema interface and contributions, admit one
bounded job under the existing storage/resource rules. First run focused ordinary
tests and the schema owner's pure catalog gates with the pinned toolchain:

```sh
GOMAXPROCS=1 GOMEMLIMIT=512MiB GOTOOLCHAIN=local go -C services/control test -p 1 ./internal/modules/nodes ./internal/modules/components ./internal/platform/database/dameng/migrate
```

For the real native case, use a lawfully available supported Linux DM installation,
pinned native ODBC input, unixODBC build prerequisites and a disposable schema
matching `^PO_NODES_[A-F0-9]{16}_TEST$`. Record driver/server build, native manifest,
source hashes, schema plan hash, UTC/case-sensitive session and storage admission.
Apply/readiness/check-runtime through the existing
[schema owner](../../../cmd/dameng-schema/README.md); it must include this
contribution. The test never creates/drops a schema, installs a dependency or
creates paid resources. It refuses nonempty product tables and initialized state.
Native setup also seeds an explicit Workspace membership authority UUID.

Prepare private operator-selected paths; all output binaries stay outside Git.
These shell variables are local command inputs, not server configuration:

```sh
# DM_NODES_TEST_BINARY: absolute owned output path for this compiled test; required,
# no default; local executable file only; no secret; consumed by -o/launcher;
# changing it requires a new build or selecting the retained matching binary.
# DM_CHECK_BINARY: absolute owned pinned connector-check executable; required,
# no default; nonsecret; must match the same retained source/native build;
# used only to derive typed loader references, without connecting.
# DM_RUNTIME_CONFIG: absolute private ConnectionConfiguration JSON reference;
# required, no default; existing loader validates ownership/mode/schema/policy;
# credentials remain in protected password_file; read afresh at process start.
# PROJECT_ONE_DAMENG_NODES_CONFIGURATION: test-only string path, required/no
# default; populated from DM_RUNTIME_CONFIG below, sole fixture config input;
# no literal credentials or alternate engine; reload requires a new test process.
CGO_ENABLED=1 GOMAXPROCS=1 GOMEMLIMIT=512MiB GOTOOLCHAIN=local go -C services/control test -c -p 1 -tags 'odbc_manager,repository_integration,dameng_repository_integration' -o "$DM_NODES_TEST_BINARY" ./internal/modules/components
python3 -B - "$DM_CHECK_BINARY" "$DM_RUNTIME_CONFIG" "$DM_NODES_TEST_BINARY" <<'PY'
import json, os, pathlib, stat, subprocess, sys
checker, config, binary = sys.argv[1:]
for path in (checker, binary):
    p = pathlib.Path(path)
    info = p.lstat()
    assert p.is_absolute() and str(p.resolve(strict=True)) == path
    assert stat.S_ISREG(info.st_mode) and info.st_uid == os.geteuid()
    assert not info.st_mode & (stat.S_ISUID | stat.S_ISGID | 0o022)
    assert os.access(path, os.X_OK)
prepared = subprocess.run([checker, '--config', config, '--print-launch-environment'],
                          env={}, capture_output=True, timeout=30, check=False)
assert prepared.returncode == 0 and not prepared.stderr and len(prepared.stdout) <= 16384
env = json.loads(prepared.stdout)
assert isinstance(env, dict) and set(env) == {'DM_SVC_PATH','ODBCSYSINI','ODBCINI','ODBCINSTINI','LD_LIBRARY_PATH'}
assert all(isinstance(v, str) and v and '\0' not in v for v in env.values())
env.update(PROJECT_ONE_DAMENG_NODES_CONFIGURATION=config, GOMAXPROCS='1', GOMEMLIMIT='512MiB')
os.execve(binary, [binary, '-test.run=^TestActualNativeDamengNodeComponentRecovery$',
                  '-test.count=1', '-test.timeout=240s', '-test.v'], env)
PY
```

The scenario covers actual native enrollment/discovery, session-scoped component
reads, foreign-Workspace refusal, task intent replay/conflict, claim/renew, disable
and passive rollback observation, stale result refusal, explicit reenable, result
replay without duplicate audit, deadline reconciliation, log insert/update,
pagination, removal and fresh same-name identity, then explicit unknown-commit
orphan reconciliation. Enrolled facts/journals are synthetic. Unknown commit uses
an outcome double after a real transaction; physical acknowledgement loss remains
a separate target fault test. Unit executors exercise type selection, timestamp
bounds and no SQL after an error; they do not prove native SQL execution.

Run existing PostgreSQL/MySQL repository profiles separately, with one fresh
fixture per leaf. Do not point their generic `repositoryfixture.New` at DM; this
native case constructs its own admitted store and explicit seed SQL.

Expected success: selected native identity, all scenario assertions, and a closed
manager with zero physical owners/cleanup jobs. Missing native inputs fail rather
than skip. On any uncertain mutation, stop and retain the original schema/journal,
redacted output and exact source; do not rerun to obtain a passing receipt. Cleanup
closes the owned manager with bounded drains, but never equates process exit to
successful release. The operator inventories and removes only this disposable
schema/binaries after required diagnostics and independent reconciliation are
retained. No automatic global cleanup or shared daemon changes are included.

Native fresh/upgrade/readback, nullable JSON, FK rejection, uniqueness races,
derived-task immutability, Unicode case-sensitive names, same-timestamp pagination,
concurrent lock order, native cancellation and real commit loss remain target
qualification until their corresponding evidence exists.

## Official syntax basis

Reviewed 2026-10-24: [queries / LIMIT / FOR UPDATE](https://eco.dameng.com/document/dm/zh-cn/pm/check-phrases.html),
[UTC and date functions](https://eco.dameng.com/document/dm/zh-cn/pm/function),
[JSON and CLOB operations](https://eco.dameng.com/document/dm/zh-cn/pm/json),
[CASE CLOB type consistency](https://eco.dameng.com/document/dm/zh-cn/faq/FAQ_FUNCTION),
[virtual columns and unique NULL semantics](https://eco.dameng.com/document/dm/zh-cn/pm/definition-statement.html),
[DBMS_LOB comparison](https://eco.dameng.com/document/dm/zh-cn/faq/faq-sql-gramm),
[row trigger errors](https://eco.dameng.com/document/dm/zh-cn/faq/faq-db-check).
Documentation establishes syntax choices; only the pinned target can qualify them.
