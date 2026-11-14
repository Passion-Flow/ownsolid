# Native local connector and retained ODBC ownership

The optional [native migration owner](migrate/README.md) and
[dameng-schema command](../../../../cmd/dameng-schema/README.md) add a fixed
account/Workspace foundation catalog, durable dirty journal, pinned native lock,
metadata readback and restricted runtime grants. `schema_configuration.go` owns
its protected typed operator inputs. `Store.ValidateSchemaUse` checks actual
admitted identity and exact native metadata limits; `SameSchemaTarget` compares
original socket/native-inventory bindings without exposing private inputs.
Source-bound public-manager race/vet, offline catalog/CLI and Linux amd64/arm64
compilation have passed in separately retained local profiles. Actual DM
connection, dictionary, catalog, grants and recovery remain unqualified; the
[current diagnosis](../../../../../../docs/milestones/m29-domestic-platform/native-runtime-diagnosis-20261103.md)
records their exact boundaries. Its typed readiness never enables whole-platform
serving. See the [full graph checklist](../../../../../../docs/milestones/m29-domestic-platform/repository-integration.md).

The current connector increment adds [strict configuration](connection_configuration.go),
[pinned native input](connection_input.go), actual native session/driver identity,
and [Store](store.go) over the existing real manager. It supports an explicitly
selected Linux UNIXSOCKET profile only. The
[shared Dameng adapter](../transaction/dameng.go) borrows that admitted Store;
[dameng-connector-check](../../../../cmd/dameng-connector-check/README.md) provides
real read-only and opt-in disposable-table transaction checks. Standalone checker
results do not authorize the main Control business graph. Default and native public-manager
compilation, race tests and vet pass for this increment; the actual proprietary
driver/database profile remains separate. See the
[local contract](../../../../../../docs/milestones/m29-domestic-platform/local-connector-contract.md).

`Open(ctx, ConnectionConfiguration)` returns a retained Store whenever a manager
was created, including failed connection attempts. Callers must close that owner
even when the returned error is non-nil. `BorrowPool` requires successful open;
`Within` owns actual transaction completion; `OperationContext` applies the typed
request bound. Idle connections remain disabled. [session.go](session.go) now lends one pinned
physical connection across multiple transactions. Successful SQLEndTran precedes
autocommit-on and exact readback; each next transaction explicitly selects
READ ONLY/READ WRITE and isolation. A native failure never clears poison or
reconnects the session. Logical Close is checked against the physical-owner ledger;
failed cleanup remains retained. The target session test is prepared but unrun. `Close` may return pending resources and must never be mistaken for a
completed native release. Signed native int32 diagnostics and unknown-COMMIT
precedence are exposed only through the safe error boundary.

The original manager-only constructor and evidence remain available below.

`VerifyNativeInventoryForValidation(ctx, NativeManifest)` checks static SDK inputs
before metadata discovery. Its three unobserved DriverVersion/DBMSName/DBMSVersion
fields must all be empty; any partial or complete asserted identity refuses this
entry point. It shares the ordinary file/hash/ELF/dependency/private-layout/cwd
checks and creates no native handle, connection, Store or serving authority.
Normal manifest files, their JSON Schema, `LaunchSettings` and `Open` still require
all observed identity fields. The validation value is not a normal runtime
manifest. This static check supplies no environment, socket, credential or native
metadata validation; the explicit metadata tool owns its separate launch and
retained manager lifecycle.

This isolated source slice calls the real standard ODBC manager behind
`cgo && odbc_manager`. It needs admitted public `sql.h`/`sqlext.h`/`libodbc`, the
specified 64-bit ABI and a C11 compiler. Its only external first-party Go package
is `projectone/releasecontract/v1`. No proprietary SDK, vendor kernel,
domesticplatform package, shared transaction adapter, foundation repository or
application database selector belongs in this compile boundary.

`NewODBCManagerForValidation(ODBCConfig)` returns a retained `*ODBCManager`.
`BorrowPool()` returns a borrowed `*sql.DB`; closing or discarding that pool does
not discharge Manager ownership. The composition root must retain Manager until
`Close(ctx)` returns state `closed` with zero physical owners and cleanup jobs.
The new format 2 configuration requires an independent physical limit, including
failed connections and cleanup in progress. Manager reserves that budget before
native allocation and keeps every connection/statement/binding rooted until
native release and tracked cleanup completion. No global registry, finalizer or
retry daemon hides ownership.

Connections serialize genuine native calls, with typed bounds, parameter binding,
eager bounded results, exact native attribute/isolation readback and native READ
ONLY SQL. A read-only attribute is not an authorization guarantee. Cancellation
joins both the original call and SQLCancel before cleanup and suppresses late
results. A driver ignoring timeout/cancel can delay this join; this slice has no
forced-free deadline. Unknown COMMIT outcomes publish no successful receipt and
must never authorize automatic business replay.

The native type boundary now binds Go `bool` as `SQL_C_BIT` / `SQL_BIT` and
returns BIT results as Go `bool`; it rejects noncanonical native bytes or lengths.
`sql.NullBool` uses the standard `database/sql` conversion. Nil binary slices bind
SQL NULL; non-nil empty binary slices retain an owned address and zero length.
Every allocated binary byte is counted, and both input and result binary values
are copied across the native boundary. Integer columns retain their integer type.
The private `odbcDecodeCell` helper is called after successful native SQLGetData;
it does not replace the driver, execute SQL or grant connection success.

The nullable changes passed the original29-root/84-entry public-manager gate
before this connector increment. Those receipts retain their exact source;
changed source needs a new run. See the
[nullable type contract](../../../../../../docs/milestones/m29-domestic-platform/nullable-types-contract.md)
and [current gate](../../../../../../tools/quality/odbc/README.md). Original
source-bound receipts below remain historical for changed code. Actual DM
round trips, transactions, cancellation and transport still require their target.

`Status()` reads redacted copied metadata without waiting for native connection
locks. `RetryCleanup(ctx)` attempts eligible failed/retired resources once or
joins an existing attempt. `Close(ctx)` fences new work, joins its single pool-close
operation and invokes explicit cleanup. These methods require deadlines; an
expired caller wait may return pending ownership while a tracked native cleanup
continues. Retry/join it explicitly and keep Manager alive. Borrowed connections,
transactions and request operations must be closed/joined by their callers.

Configuration uses the strict loader or explicit validated constructor. Secret
connection strings come only from protected file references. The unchanged local
input boundary requires effective-UID-owned 0700 parents and 0600 single-link
regular files, pins `os.OpenRoot`, uses no-follow/nonblocking open, and verifies
size, mtime, inode and both named/pinned parent identities before publishing data.
Raw paths, handles, private strings and driver diagnostic text are redacted.

The original private repair overlay and acquisition map remain historical
source-only records. Mainline now has actual cgo compilation/vet/private library
binding, real public-manager handle/buffer/missing-driver checks and explicit
cleanup controls accepted across retained source scopes; see
[exact evidence](../../../../../../docs/execution/mvp/m29-calling-layer.md).
Integration fixes add explicit SQLPOINTER casts and defer exact timeout readback
until connection success; per-statement synchronous verification remains active.
Synthetic cleanup controls exist only in explicit test files and do not qualify
native DM behavior. Lawful supported `libdodbc`, native cancellation/cleanup and
platform operation remain unqualified prerequisites. Remote authenticated native
encryption and CA/DNS-IP peer verification remain outside the explicit local IPC
implementation. This is not whole-platform Dameng
serving support or target qualification.

See the [ownership repair contract](../../../../../../docs/milestones/m29-domestic-platform/manager-repair-contract.md)
and `manager-repair-files.json` for the exact selected source/configuration/docs.
The original 17-path overlay and `manager-minimal` source/receipts remain retained
unchanged. Integrate only the reviewed repair selection into the real module;
never import the old untagged kernel or foundation files for this compile gate.

## Versioned long-value transport and retained memory

Connector format2 / manager format3 explicitly adds three typed capacities;
legacy versions retain their inline limits. See the
[configuration mapping](../../../../../../config/examples/domestic-platform/README.md#explicit-bounded-long-values)
and [wave6 source handoff](../../../../../../docs/execution/mvp/parallel-wave6-domestic.md).
`odbc_manager_native.go` uses real SQLParamData/SQLPutData for large text/binary
bindings and repeated SQLGetData only for native long types. It admits only01004
continuation warnings, tracks known/unknown remaining native lengths and rejects
unknown terminal lengths, malformed Unicode, unexpected status or truncation.
Text is UTF-8 to callers, UTF-16 at the native boundary; binary is copied.

`odbc_owner.go` reserves driver-owned binding, scratch and eager materialization
memory before allocation. Query leases survive statement disposal and count
against Manager closure until Rows.Close (or canceled Rows disposal). Failed
SQLFreeHandle retains native scratch/bindings and their leases. Cancellation joins
both the original operation and SQLCancel before releasing statement-owned buffers;
there is no forced-free timeout or business replay. All statements still poison on
native failure. Status exposes only retained byte/count/limit facts. This admission
budget covers a conservative allowance for Go conversion and scan copies, not
vendor allocations, allocator/GC RSS or application values retained after Close.

`Store.Capacity()` returns the immutable, nonsecret `ODBCCapacity` projection for
repository admission and paging. It neither reserves capacity nor grants SQL
rights. Format3 long payloads have separate single-value and retained-memory
limits; ordinary query metadata/scalars retain the16MiB ceiling. Native columns
reported as ordinary VARCHAR/VARBINARY retain the ordinary cell path and fail if
they exceed it. No SQL rewrite, domain-policy reduction or selector change occurs.
The real CLOB/BLOB target test is opt-in; this source increment has not yet passed
coordinator compilation, native execution or whole-domain capacity acceptance.

## Native library dependency admission

`native_library.go` inspects the actual Linux PT_DYNAMIC/PT_LOAD mapping for
every inventoried library, including support libraries. Architecture/type and
bounded dependency/string metadata are checked before native allocation. The
existing manifest1 fields remain unchanged, with stricter artifact admission:
only its declared license file may be non-ELF. Exact dependency names resolve to
the private flat inventory or the base glibc ABI names and architecture-specific loader documented in
the [input guide](../../../../../../config/examples/domestic-platform/README.md).
Private copies cannot shadow those base names. SONAME aliasing, external search
paths, dynamic audit/filter/config authority and invalid mappings fail closed.
No loader, system configuration, native binary or target identity is modified.

Inert metadata controls exercise wrong architectures/dependencies and malformed
loader tables; the Linux inventory regression keeps hashes valid while changing
a support library's ABI so checksum validation cannot hide a missed ELF check.
The explicit public_manager_elf_integration tag requires an actual retained Linux
unixODBC library through -dameng-public-manager-library and checks its real loader
metadata. That check loads no DM driver, opens no SQL session and never skips a
missing required fixture. Native runtime dlopen/ABI/security and complete OS/vendor
dependency qualification remain distinct.

`Store.VerifyConfiguration(ctx, configuration)` compares the complete admitted
principal, secret-file references and policy before revalidating the original
native binding. It checks the owner remains admitted/accepting before and after
the readback; it neither borrows another connection nor grants commit authority.

Native manifest2 adds the explicit dm_relative_release_v1 input profile for the
actual vendor202607 loader path. Native admission checks the child cwd, private
parent/bin and empty release/dependencies lookups as well as every original
inventory/session/socket guard. LaunchSettings supplies a typed child environment
and cwd; environment-only preparation refuses that profile. Neither the library
nor schema/connection checks change the process cwd. See the owning input guide
for the exact two permitted vendor path lists and all unchanged dependency gates.

The explicit manifest 2 loader profile also supports the single documented
`libodbcinst.so` / `libodbcinst.so.2` installer pair. Admission binds equal size and
SHA256, separate private single-link files and the canonical installer SONAME.
No arbitrary aliases or symlinks are accepted, and manifest 1 stays strict. The
[input guide](../../../../../../config/examples/domestic-platform/README.md)
records the actual missing-dependency observation and remaining native SQL gate.
