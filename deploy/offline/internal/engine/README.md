# Isolated Unix Engine boundary

The current `native_prepared_release_*` boundary verifies the original signed
prepared tree and sibling journal, freezes their complete byte inventory and
streams them into one empty `registry-release` volume. Runtime numeric ownership
is explicit. Original source/trust bytes, kernel daemon identity and actual
target backing/capacity are rechecked; a lost PUT response permits complete GET
reconciliation, never another implicit write. The existing native PostgreSQL
archive transport remains separate. Five protocol roots with21 passing entries
cover this implementation; they do not establish a physical daemon or large
production-artifact qualification. See the [registry forward contract](../../../../../docs/execution/mvp/mvp-forward-code-20261103.md).

`native_postgres_observation.go` decodes only the original MVP native readonly
backup/restore envelopes into the canonical safe references. Caller-supplied
metadata limits and frozen operation/request/candidate/signed-source hashes stay
distinct. Unknown, duplicate, oversized or contradictory output fails with a
fixed redacted code. Private original bytes and their hash remain `json:"-"`.
Missing original restore completion preserves a readonly observation with no
complete reference; fresh observation time/hash can change without authorizing
another effect. This is a transport boundary, not another SQL, signature,
filesystem, grant or import verifier. The caller must still execute the original
native owner in an exact owned task and bind the original scope journal. Fixed native task transport is described below. Complete scope lifecycle wiring
and actual current-image qualification remain separate required checks.

`native_postgres_verifier_task.go` adds the fixed original
`verify-kubernetes-postgres-backup` task through the existing DatabaseTask
lifecycle. Its explicit MVP profile has no runtime peers and uses network none.
The exact public request is the only input configuration; artifact data and an
optional archived declaration placeholder are readonly private volume children,
and only the separate typed scratch root is writable. It mounts no live domain
store, source DSN, registry CAS, prepared release or host directory. Artifact and
temporary volumes are not domain-store namespaces. The scope retains ownership
of durable intent, signed-image admission, exact source copying and private
initializer/empty-placeholder validation before creation.

The original constrained image/UID/GID/capability/resources/mount/consumer/input
checks and one-start/no-replay behavior remain. Native output attaches before
that start and accepts only its bounded strict original JSON; stderr is redacted
and exact bytes/reference remain private. This primitive does not submit the
full native restore or synthesize a legacy restore proof. Complete scope/M26
wiring, current-image execution and installed recovery remain pending.

`native_postgres_recovery_task.go` selects only the original full MVP native
backup, restore and readonly restore verifier through the same DatabaseTask
create/inspect/start/stop/remove lifecycle. Original typed request paths and policy
remain frozen. The operator input and full runtime configuration projection are
separate private volumes. All four original domain stores keep their exact
same-scope owners and fixed order; backup/restore permit original locks/imports,
and readonly verification mounts them readonly. Artifact and restore-record
volumes mount their private parent, preserving the original exclusive leaf and
allowing later readonly observations without copying or rewriting the records.
Optional registry CAS, prepared release and scaffold are exact owned volumes;
original native checks must establish actual filesystem/device and configuration
closure. No host bind, arbitrary command, SQL marker repair, runtime-role grant
or second authority invalidation is introduced.

`native_postgres_recovery_output.go` privately retains the original backup or
restore completion, including the actual restored runtime context and archive
hash. The original completion must explicitly contain exact
`current_lock_held: false`; a missing or case-aliased flag cannot inherit the Go
boolean zero value. The later original readonly observation remains a different result.
`ReadNativePostgreSQLTaskCompletion` reads only the exact original complete.json
from a successful terminal task with current stopped peers, strict bounded single
file archive and unchanged before/after task metadata. Missing completion is an
error, never manufactured success. Attach loss does not authorize another start.
The caller must durably retain original bytes and record identity before cleanup.

The network-none backup verifier additionally accepts the explicit optional
`backup_parent` artifact layout used by the original full backup writer. It mounts
the private parent readonly while preserving the request's original BackupRoot
leaf. Omitted legacy profile bytes keep their previous meaning. See the
[fixed recovery task checkpoint](../../../../../docs/execution/mvp/native-recovery-engine-task-20261103.md).
Complete Scope wiring, current signed image/SQL recovery
and installed target qualification remain separate unfinished work.

`native_postgres_archive.go` and `native_postgres_archive_transport.go` stream
only the complete original native backup tree, describe its protected host tar,
and copy/read back a fresh target-owned private parent through the original
never-started initializer. Every directory/file name, type, original numeric
owner, mode, size and content digest is bound. Native Unicode/long names and
immutable0555/0444 descendants remain intact; links, devices, sparse/unknown PAX,
escapes, duplicate/missing parents/records and selected metadata/byte/count limits
refuse. The original host tar hash stays distinct from the sorted complete entry
inventory hash, since the daemon may recreate different tar headers on readback.
Original native candidate/signature/SQL/archive semantics remain the native
owner's responsibility. These primitives create no native completion, request
rewrite or legacy snapshot.

`RemoveNativePostgreSQLVolume` permits only the explicit native per-attempt
operator-volume catalog through original exact ownership/zero-consumer/normal
removal checks; live domain stores and registry CAS remain excluded.

The caller creates only the exact empty private parent via PutConfiguration(nil),
journals the copy once, and retains its source-bound descriptor. Copy loss is
reconciled by GET-only complete inventory inspection; an existing input is never
overwritten. Export requires the exact successful terminal original backup task
and stopped current peers. Separate Scope integration, scaffold/projection and
prepared release provisioning, actual native Control image/SQL and final signed
recovery qualification remain required. See the [native tree transport
checkpoint](../../../../../docs/execution/mvp/native-postgres-tree-transport-20261103.md).

`native_postgres_target_task.go` carries only the original target initialize and
readonly inspect commands over their current shared request/observation wire.
The exact target Scope, initialization ID, original restore reservation binding,
network and PostgreSQL container remain frozen. Initialization requires actual
pinned system/database identity; paired-zero pins are readonly discovery only.
A separate owned record-parent private mount is RW for initialize and readonly
for inspect; no four domain stores, registry, runtime role or SQL text is admitted.
The original producer owns its pristine target, pool/lease, marker transaction and
exclusive intent/completion records. This adapter never repairs a marker or
rewrites a frozen policy, target or config pool setting.

Original structured observations and exact stdout remain private, validated by
the original wire's ValidFor(request, inspection). Missing completion and unpinned
discovery cannot become initialization success. The original fixed task lifecycle,
peer/consumer, current input, resource, attach-before-start and no-replay checks
remain. Only the two named native temporary input/record roles join the existing
zero-consumer cleanup catalog. See the [target task checkpoint](../../../../../docs/execution/mvp/native-target-engine-task-20261103.md).
Scope journal integration and actual refreshed Control-image execution remain
separate required work.

`native_configuration_mounts.go` supplies the Linux kernel-mount check for the
fixed ordinary [native private installer](../../../../../docs/milestones/m17-offline-compose/native-private-configuration.md).
It reads bounded mountinfo, requires a read-only source for install, an explicit
native target (ext4/XFS/Btrfs), and no nested mounts. Inspection permits a read-only
target. It starts no process and sends no Engine request. Docker names/labels and
absence of consumers remain host-caller checks: a container cannot establish
those facts from kernel mountinfo. The synthetic parser root is
`TestNativeConfigurationMountBoundary`; actual Linux mount qualification is pending.

This package implements the approved local Unix Docker API subset for explicit
isolated scope lifecycle. Client/New load existing typed Engine configuration and
approved contracts/release/v1/engine-profile.json; scope owners must independently
check the selected Engine and hold journal authority. It does not accept arbitrary
customer endpoints, infer ownership from a name, pull images, expose credentials
or become a general remote execution adapter.

Core owners are client.go (protected Unix transport/request bounds), runtime and
control_replacement.go (exact process metadata), volume/utility/configuration
files (owned private payloads), and fixed operator task types. Existing
InspectStopped/Current/FailedControlConfiguration return observation only, with
caller-supplied expected full contents. They retain their historical behavior.

configuration_readback.go additionally owns digest-manifest-bound in-memory
ReadCurrentControlConfiguration and explicit ReadFailedControlConfiguration.
The caller supplies a trusted immutable generation index and exact scope/control/
readonly private volume destination/UID/GID. The methods freeze that index,
verify actual owned local volume and exactly one consumer before/after a bounded
GET archive, preserve runtime digest, and reject any file count/size/hash/type/
mode/owner mismatch. Contents remain json:"-" and are never extracted or logged.
Existing archive validation remains the final complete structural assertion.
No utility is created and no runtime/database/file mutation occurs.

This is a library foundation. Complete version2 TLS target selection and its
scope/journal/activation/current SQL/stores/backup integration are still pending;
no new CLI or version2 request is admitted by this increment. In particular,
original prepared bytes must not silently replace earlier effective credentials.
Current/failed process authority remains the caller's journal responsibility;
reading bytes does not authorize rotation, restart or cleanup.

See the [frozen contract](../../../../../docs/engineering/deployment-transport/owned-configuration-readback.md)
for API/limits/errors and selected eight-root source verification. Four new roots
use an explicitly synthetic Unix Engine test fixture; unchanged inspection/archive
roots are direct regressions. Compilation and test-double contracts do not
qualify the approved real Engine/native platform, NVIDIA or a full installation.
No protected configuration, sockets, generated images or live state belongs here.

Fixed training/preparation restore imports include their existing backend empty
store confirmation and the exact private child of the declared output mount.
Backend configured-root equality and empty-target admission remain required;
there is no arbitrary caller flag/path or verifier/export behavior change. See
the [store restore contract](../../../../../docs/milestones/m17-offline-compose/store-restore.md).


## Current M17 local publisher boundary

The existing local Docker Desktop profile now admits the pinned darwin operator platforms and Linux amd64/arm64 publisher containers. Linux admission rereads the same exact Engine identity and requires Docker Desktop; arbitrary Linux daemons and the native Ubuntu profile remain distinct. The release-owned version list contains29.8.2 and29.9.2, retaining API1.49. Actual Linux-amd64-emulated read-only preflight passes on29.9.2 with unchanged Engine object inventories; distinct-UID/native-volume installation is still separately required.

The cumulative account22 upgrade admits one additional fixed Control task kind,
`upgrade-releases`, on its own generated operator configuration volume. It retains
exact peer/network/image/input/process admission and the existing nonroot task
recipe. Current Control configuration readback also admits the explicitly named
`upgrade-control` generation with the same ownership/mount/private-byte guards
as TLS generations. These are narrow versioned roles, not arbitrary volume or
command authority. Complete controller and actual Engine evidence remain separate.


Fixed cumulative backup/restore operator tasks now carry the existing prepared
read-only locale-role dependency through exact same-scope ownership, mount and
consumer fences. Optional internal task metadata preserves recorded historical
plans; it adds no arbitrary mount/command or validation bypass. See the
[localized task contract](../../../../../docs/milestones/m17-offline-compose/localized-operator-tasks.md).

The [arm64 local publisher contract](../../../../../docs/milestones/m17-offline-compose/arm64-local-publisher.md) preserves the exact Docker Desktop provider and Linux-amd64 target checks. A native arm64 controller is separate from emulated application or physical-target qualification; original platform refusals stay retained.

### Native PostgreSQL scaffold directories

`PutNativePostgreSQLScaffold` and `InspectNativePostgreSQLScaffold` derive
private empty directory ancestors only from the frozen task mounts. The caller
provides the existing pinned initializer image; there is no arbitrary directory
list or host bind. A complete or partial namespace is preserved, and uncertain
PUT output requires journal-controlled GET observation without replay.
`ConfigurationPayloadVerified` with zero files and bytes describes this empty
namespace only. Actual filesystem/device, CAS, signed prepared payload, SQL and
Scope readiness still require their original owners and independent evidence.

`native_postgres_roots_task.go` transports only the original readonly four-root
inspection request. It reuses the frozen runtime projection at its original
restore-attempt owner/path and mounts the exact four existing store private
children readonly. The inspector uses network none while independently retaining
the current reservation bridge/PostgreSQL/stopped-peer/consumer checks. Its input
contains only native-roots-request.json; original configuration/environment byte
hashes and policy remain frozen, with no SQL/records/CAS/scratch mount or YAML
rewrite. Private original output must bind the exact four targets and released
locks. Scope custody and final native Restore relocking/comparison remain required.
See the [readonly roots transport checkpoint](../../../../../docs/execution/mvp/native-roots-engine-task-20261103.md).

The fixed `upgrade-mvp-postgresql-cpu-v1` database task uses the existing
cumulative configuration custody, original stopped-Control peer and once-only
SDK execution. The separate `upgrade-releases` recipe remains unchanged.
The MVP forward controller binds its schema29 transition independently.
