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

# Native DM foundation migration owner

This package executes a fixed account/Workspace schema through an already
admitted `dameng.Store`. It owns native DDL, ordered hashes, the dirty journal,
session lock, structural readback and exact runtime grants. It imports its parent
connector only in `cgo && odbc_manager` files. It does not select a platform
database, own business transactions or import PostgreSQL/MySQL migration assets.

`schema.go` contains14 native table definitions and16 explicit indexes. A final
installation seed completes the immutable31-step prefix. account_license.go adds
37 explicit steps: versioned journal capacity, license deployment/history,
preferences and current SSO/session structure/guards. The retained account slice has 21 tables and 68 steps. The composed domain
catalog currently has 106 tables and 459 steps: account mail, artifact stores,
nodes/components, datasets, models/deployments/distribution, applications, the
deferred artifact-ingestion foreign keys, then knowledge, middleware, MongoDB
dataset imports, task delivery, training, model preparation, image registry and
private clusters, followed by AWS resource observations and immutable import receipts. The retained 454-step prefix and both earlier prefixes stay byte-identical. `catalog.go` derives deterministic SQL and
metadata hashes from those manually authored native definitions. `Plan` returns
copies; `JournalSQL` also exposes the bootstrap statement for offline review.
`engine.go` owns the interrupted-operation state machine. Its private backend
fixture exercises uncertainty without pretending to be a DM server.

`native.go` pins one actual native connection across profile/projection checks,
DBMS_LOCK acquisition, journal operations, DDL and metadata readback. It requires
case-sensitive identifiers and DDL_AUTO_COMMIT=1. DDL is never wrapped in an
ordinary business transaction.

Read-only expression admission also requires JSON_MODE 0/2, exact JSON_VALUE
ERROR ON EMPTY ERROR ON ERROR syntax/results, valid/invalid CLOB JSON rejection
and equal/unequal CLOB DBMS_LOB.COMPARE before lock/journal creation. Domain
admission also probes JSON_OBJECT_KEYS and JSON_CONTAINS_PATH with present,
missing and JSON-null members. The node capability array bound uses absence of
index 11, including JSON-null entries, rather than an undocumented length helper.
These expression forms follow the official [DM JSON reference](https://eco.dameng.com/document/dm/zh-cn/pm/json);
read-only probes still require the selected server to produce the exact results. Restricted
runtime grant/readiness paths repeat those checks; no automatic mode/grant change
is allowed. capabilities.go and its pure tests retain this fail-closed boundary.
A failed SQL acknowledgement retains a dirty row;
`reconcile` acknowledges only a matching existing object, and `retry` dispatches
only an absent dirty operation after fresh locked observation. Recovery stops
after that one operation. Ordinary `apply` cannot replay dirty work. Source drift,
extra objects, missing keys, disabled checks or incomplete journal entries fail.
An empty exact bootstrap journal may be adopted because it cannot journal itself.
Journal timestamps are database wall-clock observations and may move backward
after clock correction or recovery. Their nonempty/state relationship is checked;
ordering comes from the contiguous step, source hash and observed schema rather
than timestamp comparison. `journal_readback_test.go` exercises the production
decoder through in-memory `database/sql` rows and the recovery state machine;
those tagged tests do not establish actual DM execution.

`access.go` uses two independently authenticated, same-socket/same-native-inventory
Stores for `GrantRuntime`. The pre-existing runtime user owns no schema objects;
only the composed catalog's closed composed-table CRUD, REFERENCES on actual FK targets, and journal SELECT
set is granted. No users, passwords,
roles, server parameters or unrelated permissions are changed. Direct/system/role/
column grants are inspected through the runtime session before and after grants.
The fixed PUBLIC/SVI profile and GRANT_SCHEMA=0 requirement are described in the
[contract](../../../../../../../docs/milestones/m29-domestic-platform/schema-foundation-contract.md).
Unknown grant completion stops the pass; a later explicitly requested invocation
first rereads grants and adds only missing exact privileges.

`Report` is typed and retains applied/dirty counts. `FoundationReady` concerns
this slice only; `RuntimeReady` additionally requires the real restricted account
check; `PlatformReady` stays false. Owner readiness acquires the lock
and changes no schema/data; runtime readiness reads metadata without taking an
owner lock and is a point-in-time observation. Static extensions use complete ALTER before/after projections and a separate
version2 hash; the original Scope/31 checksums remain unchanged. withOwner serializes
DDL and WithReadyRuntime retains the same lock across trusted operator callbacks. Both operations need an absolute caller
deadline. Native cancellation joins actual calls; bounds do not force-free a C
handle. `Run` does not close its caller's Store. The executable closes every
returned Store, even a failed Open, and refuses success while cleanup is pending.

Schema policy is `dameng.SchemaConfiguration`, loaded through protected JSON and
an explicit env-file mapping. Metadata row/cell caps must exactly match the native
connector policy so the driver enforces them before materialization; SQL/column/
time/result limits must also admit the fixed queries. No ambient configuration or
dynamic SQL catalog is accepted. See [inputs](../../../../../../../config/examples/domestic-platform/README.md)
and [operator commands](../../../../../cmd/dameng-schema/README.md).

Coordinator checks: pure package tests and tagged compilation/race/vet are listed
in the command guide. Actual DM dictionary fields/representations, virtual-column
index behavior, JSON/LOB checks, NULL uniqueness, lock persistence, native errors,
restricted grants and restart remain unqualified. Missing dictionary capability
is detected before journal bootstrap; it requires an explicit compatibility repair,
not a waived check. The complete serving graph remains [pending](../../../../../../../docs/milestones/m29-domestic-platform/repository-integration.md).

Full trigger metadata includes DBMS_METADATA.GET_DDL token comparison; body-only
checks cannot admit WHEN/UPDATE OF/REFERENCING bypasses. Native FK triggers are
allowed only through already-verified constraint/table/reference-index identities,
SYSCONS actions and exact reserved names derived from constraint IDs. No prefix
wildcard admits a user trigger. Unknown dictionary representations fail closed.
See the [contribution interface](../../../../../../../docs/execution/mvp/parallel-wave5-domestic-schema-interface.md).

ALTER decoder fixtures cover dirty before/after, unknown partial shape, superseded
applied shapes and stopping after native metadata failure. The fixtures do not
qualify actual ALTER, automatic FK triggers or full GET_DDL formatting. New code
has lane syntax checks only; coordinator compilation and actual target runs are
still required. Older receipts retain their original source scope.

## Current Linux continuation — 2026-11-03

`contribution_cloudinventory.go` appends five steps after the retained454-step
prefix: two native tables, two indexes and the immutable receipt trigger. The
Workspace foreign keys retain both tenant and Workspace identity; observed
resources retain provider/account/region/instance uniqueness, CLOB JSON objects
and a native BIT false default. Receipt identity includes the original actor and
idempotency key; its completed interval and nonnegative count are checked. Runtime
CRUD grants are derived from the closed catalog; neither new table receives
REFERENCES or DDL authority. Existing owner lock, dirty-journal recovery and
structural trigger readback apply unchanged. This does not activate the AWS
business repository or the server selector for DM. Official current x86 native inputs and terms are now retained for authorized
isolated development use. Actual DM execution remains pending the admitted
non-root socket installation and its complete native evidence.

`cloneTable` uses explicit nested-slice copies for repeated schema projections.
It preserves nil/empty shapes and exact serialized metadata bytes, retains
independent caller ownership and introduces no cached readiness or database state.
The original JSON round trip in every projected step caused the retained full
tagged-race600-second timeout; the current full migration package passes20.028
seconds on the supplied1CPU profile. The old failed invocation remains failed.

Borrowed maintenance integrations use SchemaLockID(schema) for the exact existing
installation-lock identity and CheckCatalogWithin(ctx, connection, configuration)
for same-connection owner/expression/dictionary/full-readiness checks. The caller
retains an already admitted connection and the maintenance fence. These functions
never acquire/release locks, start/end transactions, close the loan, grant access
or set platform readiness. Invalid loans/configuration/deadlines refuse before
constructing the fixed catalog. The recovery owner supplies the actual maintenance
lifecycle and whole-row/LOB/grant custody; these APIs do not fabricate it.

## Complete compiled serving contract

`RequireControlServingCatalog` independently checks the closed 108-relation
inventory and a versioned SHA256 witness over every native SQL step and checksum,
all ALTER before/after shapes, seed/backfill probes, indexes, triggers, deletion
rules, fragment registration, final metadata and the exact restricted runtime
grants. It performs no I/O, caches no readiness and establishes no installed
state. Same-count SQL, shape, checksum or privilege drift refuses. The real
serving owner must still run native `CheckRuntime` and verify its existing
installation lock and identity.

This contract targets the retained 459-step prefix followed by the independently
reviewed `c54ac8d9` inference contribution, yielding 476 steps, 108 business tables
and 109 grant objects including read-only journal SELECT. The coordinator owns
its append-only registration and final cumulative acceptance. The unregistered
459-step branch intentionally refuses serving; source10 positive checks use an
explicit frozen candidate with that exact registration and contribution. They do
not silently register a second catalog or adopt historical uncommitted sidecars.
The version-1 complete witness is
`5638884b122cea0c2f414d143bb0f3a165b27bf647f2113b3ff1e619a62dc726`.
A future reviewed migration changes the explicit inventory/witness together with
its meaningful refusal checks. No runtime input can supply a replacement hash.

`InstallationLockID` delegates to the validated original `SchemaLockID` namespace.
It returns -1 for invalid input, outside the valid nonnegative lock IDs. Native
callers validate the complete schema configuration before borrowing a session;
callers requiring an error can use `SchemaLockID` directly. Neither function
acquires a lock or grants migration/maintenance authority.
