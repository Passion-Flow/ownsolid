## Current central native catalog — 2026-11-03

The coordinator registers `inference_cutovers_v2` after `cloudinventory_v2`: the
current compiled plan has 476 append-only steps, 108 business relations and 109
restricted grant objects including the SELECT-only migration journal. The original
459-step cloud prefix and independent 454-step inference contribution tests remain
unchanged historical boundaries. `RequireControlServingCatalog` rejects incomplete
or same-size drift before serving; its closed source witness does not replace real
runtime-principal, installation-lock, vendor SQL or recovery checks.

This central registration follows reviewed source-bound helper controls and real
CGO1 amd64/arm64 linking evidence. Final current-main checks and actual vendor SQL
remain separate gates; no M29 or target qualification is inferred from registration.

# Explicit native DM foundation operator

This optional command operates a pre-existing, isolated Linux UNIXSOCKET DM
installation and one application schema. It neither starts a server nor creates
an account, database, OS user or platform serving configuration. All SQL comes
from the [native catalog](../../internal/platform/database/dameng/migrate/README.md).
The complete business graph and target qualification remain pending.

The command requires an explicit mode. JSON results contain fixed error codes,
scope/applied/dirty counts and cleanup state, never private input paths/passwords
or raw vendor diagnostics. `success=true` means the requested operation completed;
`reconcile` may succeed for one step while `foundation_ready=false`. The report
always retains `platform_ready=false`. On an uncertain mutation stop and retain
the original journal/schema and failed output. No automatic drop, replay or retry
daemon runs. `cleanup_state=closed` requires zero physical owners/cleanup jobs;
native calls ignoring cancellation can still delay process exit.

## Build and prepare

The coordinator must use the existing admitted Go/C11/public unixODBC toolchain
and native source-build recipe; this lane did not run compilation. After resource
admission, use the existing protected CGO flags and offline dependency inputs:

```sh
go -C services/control test -p 1 ./internal/platform/database/dameng/migrate ./internal/platform/database/dameng ./cmd/dameng-schema
go -C services/control test -p 1 -race -tags odbc_manager ./internal/platform/database/dameng/migrate ./internal/platform/database/dameng ./cmd/dameng-schema
go -C services/control vet -tags odbc_manager ./internal/platform/database/dameng/migrate ./cmd/dameng-schema
go -C services/control build -tags odbc_manager -o "$DAMENG_SCHEMA_BINARY" ./cmd/dameng-schema
```

`DAMENG_SCHEMA_BINARY` is an operator-selected private absolute output path, not
a product setting. Default/non-cgo builds can export the plan but native modes
return `DAMENG_SCHEMA_NATIVE_BUILD_REQUIRED`. A successful macOS public-manager
build is not a Linux `libdodbc` runtime. Never execute these commands concurrently
with another heavy coordinator job without resource admission.

Prepare lawfully available native files and connector references according to the
[connector contract](../../../../docs/milestones/m29-domestic-platform/local-connector-contract.md).
Use the existing Linux UID, private socket and empty native configuration directory.
The existing non-DBA owner must own the selected empty schema and have CREATE TABLE,
index, constraint and DBMS_LOCK capability. A different pre-existing non-DBA
runtime user needs CREATE SESSION and the fixed PUBLIC/SVI profile. It must own
no objects and must have no extra roles, delegable grants, creation/ANY DML/admin
authority or direct grants outside the fixed schema. The tool does not grant
these prerequisites. Server case sensitivity must be1, DDL_AUTO_COMMIT1 and
GRANT_SCHEMA0. It never changes server parameters or weakens authorization to
make admission pass. The grant mode uses the same native configuration directory,
manifest and original socket identity for both connector configurations.

Copy and adapt the [schema inputs](../../../../config/examples/domestic-platform/README.md)
into private0700 directories/0600 files. Both owner/runtime connector examples
deliberately require actual paths, same existing UID and real pinned vendor files.
No inline password appears. `--config` is the owner for apply/readiness/reconcile/
retry/grant and the runtime connector for check-runtime. `--runtime-config` is
required only for grant. Each connector supports its matching explicit env-file;
`--schema-config` supports `--schema-env-file`. These are operator CLI mappings,
not an active Compose/Helm database selector.

## Execute only in an authorized disposable target

The plan is pure and needs no configuration or driver:

```sh
"$DAMENG_SCHEMA_BINARY" --mode plan
```

It prints journal CREATE SQL plus459 exact step statements/hashes, including the retained31-step foundation prefix for review.
The config-only `--config "$DM_OWNER_CONFIG" --print-launch-settings` operation
returns exactly `format_version`, the original five-key `environment`, and the
derived `working_directory`. Manifest 1 uses an empty cwd; manifest 2 requires the
private vendor `bin` cwd. The legacy environment-only operation refuses manifest
2. A compatible launcher must apply the typed environment and cwd to its child
before executing the real operator; the library never changes the process cwd.
Run the real operator through that launcher so only validated native loader
references enter its environment. The shell variables below are private
path references supplied by the operator; they are not new ambient product keys.

```sh
python3 -B tools/quality/odbc/schema.py --binary "$DAMENG_SCHEMA_BINARY" --config "$DM_OWNER_CONFIG" --schema-config "$DM_SCHEMA_CONFIG" --mode apply
python3 -B tools/quality/odbc/schema.py --binary "$DAMENG_SCHEMA_BINARY" --config "$DM_OWNER_CONFIG" --schema-config "$DM_SCHEMA_CONFIG" --mode readiness
python3 -B tools/quality/odbc/schema.py --binary "$DAMENG_SCHEMA_BINARY" --config "$DM_OWNER_CONFIG" --runtime-config "$DM_RUNTIME_CONFIG" --schema-config "$DM_SCHEMA_CONFIG" --mode grant
python3 -B tools/quality/odbc/schema.py --binary "$DAMENG_SCHEMA_BINARY" --config "$DM_RUNTIME_CONFIG" --schema-config "$DM_SCHEMA_CONFIG" --mode check-runtime
```

Expected: apply/readiness/grant/runtime check each succeeds with459 applied steps,
dirty_step0 and foundation_ready true. Owner operations additionally report
lock_released true. Only grant/check-runtime may set runtime_ready true, after
actual runtime authority verification. Runtime check takes no owner lock. Grant records both closed
Store states. Every report remains platform_ready false. The runtime operator
does not prove all service repositories can use this schema.

After an interruption, run owner readiness to observe refusal. `reconcile` checks
the dirty object exists with its exact definition before completing that journal
row. If it is absent, reconcile refuses with EXPLICIT_RETRY_REQUIRED; after
review, explicitly invoke `--mode retry`. Both recovery modes stop after that
single step. Then an explicit new `--mode apply` can continue the clean prefix.
Drift, source mismatch, unexplained objects or missing applied objects are never
repaired by these commands. Retain failed evidence and use an independently
validated database-aware restore or an approved compatibility repair.

The [case catalog](../../../../tools/quality/odbc/schema-cases.json) distinguishes
prepared pure tests from native target acceptance. On a disposable target, also
verify journal tamper/drift refusal before writes; simultaneous owner lock
contention; interrupted DDL/journal acknowledgement; restart/readiness; malformed
UUID/JSON, BIT/NULL, Unicode and pending-email uniqueness; and runtime refusal of
CREATE/ALTER/DROP/journal modification/GRANT. Do not simulate those as passed DM
cases. Record actual driver/server build, schema plan hashes, protected profile
fingerprints, raw redacted output, actual grants and clean shutdown. Preserve
schema/journal on failure. Dispose only the inventoried fixture after any needed
database-aware export and after all native Store owners are closed; this command
has no destructive cleanup mode.

The wave5 operator patch adds explicit bootstrap-admin-file,
reconcile-bootstrap-file, resume-bootstrap-file and check-license operations.
They require owner+runtime connector references and a protected standalone
operator policy; they never start cmd/server. The offline operator-policy-template
mode returns the existing typed runtime defaults for authoring. Follow the
[operator procedure](../../../../docs/milestones/m29-domestic-platform/operator-wave5.md).
Check-license preserves ordinary signature/history/admission checks; an unlicensed
installation returns LICENSE_MISSING, rather than a fabricated licensed pass.
