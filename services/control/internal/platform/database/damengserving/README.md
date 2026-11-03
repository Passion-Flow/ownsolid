# Restricted native control serving owner

This package composes the existing protected Dameng connector, selected transaction
adapter and central migration/runtime verifier. `contract.go` is the untagged
composition interface. `native.go` requires `cgo && odbc_manager` and a real admitted
native driver/IPC target; `unavailable.go` refuses every native selection otherwise.
The same build still supports existing PostgreSQL/MySQL selection.

This is a source contribution with explicit integration prerequisites. The central
migrate owner must export `InstallationLockID(schema)` using its exact existing lock
namespace and `RequireControlServingCatalog()` enforcing the complete business
catalog/grants. Neither API existed at the pinned read baseline. Native compilation
is pending those implementations. A bounded foundation-only operator callback does
not own an indefinite serving process, and no local success or target qualification
is claimed. See the [handoff](../../../../../../docs/execution/mvp/parallel-wave7-serving.md).

## Configuration and authority

`OpenProtected` accepts a deadline-bearing startup context and two existing private
JSON references from [typed control configuration](../../config/README.md).
`dameng.LoadConnectionConfiguration` and `LoadSchemaConfiguration` remain the sole
connector/schema policy parsers. Credentials, actual native-manifest/library identity,
socket identity and connection/session invariants stay in those owners. Runtime user
must equal the schema's restricted runtime user and differ from the owner. No owner
credential is loaded; no DDL, grant, account creation, privilege fallback or implicit
reconnect runs here. The actual connector pool/physical limits must admit one retained
installation session plus business/readback access; selected composition raises that
minimum for configured MongoDB concurrency. Serving requires at least32 projected
columns; actual parameter/cell/row/result/SQL budgets remain owned and checked by the
connector, schema and business owners. This minimum is not full business capacity
qualification. Connector cleanup must fit the schema cleanup budget.

The lock ID comes only from the central catalog owner. One physical native session
holds `DBMS_LOCK.REQUEST(id,6,wait,FALSE)` until every serving worker has joined.
Only result0 succeeds. `DBMS_LOCK.CONVERT2(id,6,0)` verifies the retained same-session
exclusive lock; result4 means absent ownership and fails, never triggers reacquisition.
`CheckRuntime` separately audits actual exact grants, expressions, full metadata and
ledger. The deployment UUID, schema/principal and installation-state object identity
are retained and compared on every audit. These readbacks do not implement database
restore orchestration. [Official DBMS_LOCK contract](https://eco.dameng.com/document/dm/zh-cn/pm/dbms_lock-package.html)
defines the lock modes, return values and session lifetime.

`Verify` and `Close` require deadlines. `cmd/server` serializes readiness and joins
workers before release. Failed lock SQL poisons that session; cleanup sends no new
SQL to an uncertain session. A confirmed session uses `DBMS_LOCK.RELEASE`, closes
its lease and then the actual native owner. Failed pool closure retains ownership
for cleanup reconciliation; successful cleanup does not retroactively pass startup,
readiness or an unknown transaction. No business mutation is automatically replayed.

## Validation and operation

On the coordinator's admitted build host, first complete central APIs, full catalog,
all domain repositories/readiness and protected driver capacity policy, then run:

```sh
go -C services/control test ./internal/platform/config ./internal/platform/database/damengserving ./cmd/server
go -C services/control test -tags odbc_manager ./internal/platform/database/damengserving ./cmd/server
```

The first command covers default/no-manager refusal and composition tests; neither
command establishes proprietary-driver operation. Link/architecture/redistribution
rights remain the actual native artifact owner's responsibility. Do not replace the
Linux UNIXSOCKET target with macOS or a PostgreSQL fixture.

For the real native lease test, an operator provisions an exclusive synthetic
`M20_SERVING_<32 uppercase hex>_TEST` schema with the complete exact compiled catalog,
existing installation binding and restricted grants. Supply the two test-only file
references documented in [the test environment example](../../../../../../config/examples/control/dameng-serving-test.env.example)
and run the test through the existing protected native test launcher/environment:

```sh
go -C services/control test -tags 'odbc_manager dameng_integration' ./internal/platform/database/damengserving -run '^TestActualNativeServingLeaseAndFreshOwner$' -count=1
```

The test opens the real owner, audits it, refuses a competing owner, closes exact
owned resources and proves a fresh owner can audit the same installation. It creates
no server/schema/account or business rows. Retain failures for operator inspection;
only the provisioning owner may destroy the recorded disposable schema. This test
is not full-server or restore acceptance.

The existing control binary uses `serve --config <private-control.yaml> --env-file
<private-native-policy.env>` with the complete valid native database mapping.
`check-schema --config <private-control.yaml>` has no env-file support: supply the
same effective native values in protected YAML. The check opens the same restricted
native owner and audits it without starting workers. Existing early `health-probe`
and `verify-serving-transport` commands retain their own flags and do not select SQL.
All other database/operator commands explicitly refuse Dameng in this composition;
use the separately owned native schema operator. No M17 equivalent is claimed here.

Actual full serving acceptance must then prove Portal/Admin/authentication/tenant
isolation, initialized artifact stores, every configured domain worker, mail, native
boolean/CLOB projections, restart persistence, readiness failure and joined shutdown,
plus existing optional machine listener and selected transport profiles. Mutate only
an exclusive synthetic target fixture under its existing operator recovery procedure.
Package parsing, formatting and this source-only contribution establish none of those
runtime outcomes. Pending schemas/driver/interfaces stay explicit implementation or
build limitations, not a target-only deferral.
