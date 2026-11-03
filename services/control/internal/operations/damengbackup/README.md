# Dameng native logical database recovery

This owner implements protected `dexp`/`dimp` invocation, original durable
execution intent, fresh-target-only import and independent database verification.
The native extension also binds the existing four file-store owners and the
existing same-transaction authority invalidation owner. The operator composition below adds matched secret/configuration closure and
a dedicated command implementation. Central dispatch and deployment wiring remain
coordinator-owned; runtime access remains a separate authorization step.

Entry points are `LoadConfiguration`, `New`, `EndpointFingerprint`, `Backup`,
`Restore` and `Reconcile`. [Configuration](configuration.go) is the sole typed
policy authority; the explicit env-file selects an entire validated replacement.
No ambient credentials or policy values are read. Examples and every field's
requirements are in the [native recovery contract](../../../../../docs/milestones/m29-domestic-platform/native-recovery.md).

[Inventory](inventory.go) verifies exact protected native ELF/tool/library/license
files and bounded `HELP` version/capability probes. An indispensable missing
vendor tool, dependency, interface or legal review remains a build/deployment gap.
The public ODBC manager supplies none of these artifacts. There is no download,
license acceptance or transport fallback. The admitted profile is non-root Linux
amd64/arm64 and an explicit protected same-host UNIXSOCKET; no host account is
created or switched. Actual vendor execution remains separately qualified.
DM_SVC_PATH explicitly selects the pinned private comments-only dm_svc.conf;
host-global aliases, wallet/autologin and active configuration overrides are refused.

[Engine](engine.go) borrows a `Custody` whose native installation owner must hold
continuous schema/installation/writer fences and provide complete canonical
database inspection. Inspection binds server, principal, schema, version, actual
endpoint and every definition/grant/applied checksum/typed row/LOB. A returned
interface value or ODBC session does not establish that implementation. The
concrete [native custody](native_custody.go) implements that borrowing through
the existing native kernel; whole-platform command/runtime composition remains
coordinator-owned.

The export process reads its connection from a generated `0600` `CONFIG_FILE`
inside a new `0700` operation root. Its argv contains no USERID/password or arbitrary
PARFILE. Export is SCHEMAS-only, includes rows/indexes/constraints/triggers/grants,
forbids dirty reads and destructive DROP, bounds concurrency and uses one archive.
The original intent and dispatch are synchronized before native execution. Process
output is bounded in memory and discarded, retaining only aggregate metadata and
a hash of retained bytes. Raw output can contain secrets and is never logged.

Restore validates the original completed backup and every archive byte, makes a
separately hashed private input copy and dispatches once into a distinctly identified
fresh server with no application objects/installation state. Schema, principal and
server version remain exact; there is no schema remapping. `IGNORE=N`, default
existing-table errors and `DUMMY=N` prevent occupied-target overwrite. Restored
catalog and every row/LOB must match the source before database completion is
published. The extension delegates authority invalidation and stores to the
existing [recovery](../recovery/README.md) and four domain owners. It grants no
runtime access.

[Process execution](process_unix.go) creates a process group, bounds output and
observes file growth/free bytes/fence/credential/socket identity. Cancellation or
budget loss terminates the group and waits before removing only the generated
credential file. Linux readback checks that the exact process group has no live
members, including inherited-pipe descendants; an unobservable or still-live group
retains the secret file and requires explicit consumer inspection. Executable
setuid/setgid bits are refused. FILESIZE/FILENUM and observer budgets do not establish a hard
filesystem quota against a defective vendor tool; admit the projected peak and
fixed reserve and retain external isolation limits. Durable writes/hashes use
private single-link descriptors, exclusive creation, fsync and parent-directory
sync. Original journal bytes are canonical and byte-bound, including whitespace;
reconciliation refuses changed original bytes. A `0644` vendor archive is confined within the private root and reduced to
`0600` before sealing; world/group-writable files are refused.

Failures after Start, nonzero exits, missing joined receipts and cancelled calls
remain unknown because an import can commit partially. [Reconciliation](reconcile.go)
never invokes export/import. It can publish a missing completion only from the
original joined-success proof and full unchanged readback; otherwise the original
target remains isolated. A retained connection file requires explicit process and
descriptor inspection before manual secret cleanup; the reconciler will not
delete a file that might still have an active consumer. Never erase an original
dispatch, replay an uncertain import or rewrite applied checksums.

Private retained inventory: `intent.json`, `dispatch.json`, `joined.json`,
`database.dmp`, `complete.json`, plus a connection file only after abrupt parent
death or cleanup failure. These can contain real data or sensitive bindings;
retain them privately and never publish them with source. Backup root paths and
directory identities remain original-bound; relocating bytes is not transparent
recovery. Keep source and target roots until the composed recovery owner releases
their exact custody. Cleanup only exact inventoried disposable roots after verified
no consumers and retained evidence. No broad pruning is used.

From `services/control`, run `go test -p 1 ./internal/operations/damengbackup` and
the same package with `-race`. Tests use an actual ELF child process and real
private files/sockets with a synthetic database protocol; they are not vendor
tools, native SQL, backup-format compatibility or Kunpeng/Kylin qualification.
Linux fixtures explicitly allocate unique private `/tmp/p1-20261103-M29-recovery-*`
roots because a runner's long TMPDIR can exceed the real UNIX socket pathname
bound. Each root's identity is captured, listeners/processes stop first, and only
that exact owned root is removed. Verify /tmp's actual backing/capacity under the
same host job admission; this does not change production path limits or storage.

## Native extension in progress — 2026-11-03

Current official x86 V8.1.5.60 inputs and actual terms are supplied under the
root-authorized private free trial. Actual dexp/dimp HELP both return0 at
version03134284604-20260707-335949-20228/file_version34. This proves tool metadata
only. Lower-case unixsocket_file is matched case-insensitively; actual session/
transport checks stay strict. No native restoration or M29 acceptance is claimed.

NativeCustody now uses the real Store.WithSession/WithinConnection, existing
SchemaLockID/CheckCatalogWithin/VerifyConfiguration, mode6 DBMS_LOCK and the
installation row on the same connection. Readback covers all typed rows and
chunk-independent CLOB/BLOB bytes. Native stores call the existing four domain
integrity/export/import owners and established private-tree protocol. Authority
uses recovery.InvalidateRestoredWithin and audit in the original real transaction.
Prepared is not complete: only a newly acquired custody after the original scope
ends can verify committed audit, full post-image and all four stores. Unknown
outcomes never replay.

The extension requires primary native API commits ace43dd0/8e9e311f/b4024069 and
the independent complete catalog witness in7d32c425;
shared owners are not copied into this branch. Local tagged/native builds and protocol checks remain distinct from actual
vendor SQL/restore and physical-target qualification. Earlier missing-header/
partial-source compile failures are retained as failures. See examples/native-context.json.example and
native-context.env.example for explicit protected input mapping. The dedicated operator files below now implement CLI/env and configuration
closure. Central dispatch, Compose/Helm/release integration and runtime access
remain coordinator work. Actual DB transport/LOB/restore and physical targets must
still be qualified.

The external Mac volume has genuine I/O errors despite mount preflight passing.
New work uses admitted internal Git and independent server inputs. External return
and cleanup remain deferred until real hardware/durability recovery; no account,
HOME/Library/Codex/credential or global setting was moved.

## Native composition inputs and lifetime

`NewNativeContext` obtains the existing connector's `LaunchSettings`, retaining
its explicit private working directory and five-key native environment. It never
changes the caller's cwd or environment and never edits vendor ELF bytes. Its
actual `LD_LIBRARY_PATH` remains the verified typed launch value; it is not
reconstructed from a different inventory's directories. The working directory
also participates in exact pinned-library shadow checks. The relative-release profile admits only the declared `./` and `../release` entries;
the private sibling `release` and `bin/dependencies` directories must stay empty.
Artifact hashes, ELF architecture/interpreter/dependency closure, private
comments-only client configuration and actual tool HELP remain required.
A versioned regular DSO may supply its actual ELF SONAME through a protected
loader alias only when that alias resolves to the exact pinned canonical file.
Different or ambiguous targets, including byte-identical unpinned copies, refuse;
no vendor executable/library is patched.

`WithNativeCustody` requires the complete compiled catalog witness before native
SQL; agreement with an incomplete compiled Plan is insufficient. It then borrows
one admitted kernel session, acquires the existing schema-lock ID in fixed mode6,
reads its real owner/mode, and holds the actual installation row in the existing
kernel transaction. `CheckCatalogWithin` performs full native metadata/applied
checksum readback on that connection. `FoundationReady` and `PlatformReady` retain
their original meanings; no platform qualification is inferred from the former.
`Inspect` hashes every catalog table and journal in PK order, closes each bounded
page cursor before LOB reads, and preserves NULL/empty/false/integer/timestamp
precision. CLOB character lengths and UTF-8 byte limits are checked separately.
The custody expires when the callback ends; the kernel owns commit/rollback and
physical disposal. A fresh-target mode admits only an actually empty target for
native import. After import, the same custody verifies the complete installed DB.

`LoadNativeContext` accepts the protected base JSON and optional explicit env file
from [examples](examples/native-context.env.example). The env file selects one
complete replacement JSON; there are no ambient reads or duplicate timeout/lock
settings. The connector and schema owners retain their own typed authorities.
All fields below are required; malformed, unknown, duplicate or null members,
false-as-number values and contradictory references fail before native work.

| Field | Type, units and admitted values |
| --- | --- |
| format_version | Integer, exactly1 |
| connector_configuration_file | Canonical protected connector JSON reference |
| schema_configuration_file | Distinct canonical protected schema JSON reference |
| snapshot.page_rows | Integer1–1024; also within connector row capacity |
| snapshot.maximum_rows | Total rows, integer1–2^40 |
| snapshot.maximum_bytes | Total canonical bytes, integer1MiB–1PiB |
| snapshot.maximum_lob_bytes | Per-LOB byte ceiling, positive and at most maximum_bytes |
| snapshot.lob_chunk_size | CLOB characters/BLOB bytes per native request, integer256–4096; at most one quarter of connector ordinary cell bytes |
| snapshot.maximum_tree_bytes | Aggregate private store bytes, integer1–1PiB |
| snapshot.maximum_tree_files | Aggregate private store files, integer1–10000000 |
| snapshot.maximum_tree_metadata_bytes | Per-tree metadata bytes, integer4096–1GiB |

`CaptureMatchedStores` calls the existing model/dataset/training/preparation
integrity and export owners under source custody. Original database completion,
source trees and typed DB data must remain unchanged across capture.
`RestoreMatchedStores` requires the original backup-root/completion lineage as
well as matching DB contents, imports once into four exact empty locked roots,
then checks domain references/bytes, original snapshots and full DB again. Its
read-only `postgresbackup.TreeDigest` dependency reuses the established generic
private-tree protocol; it selects no PostgreSQL backend, opens no pool and runs
no SQL. Only complete Dameng `platform` application configurations are admitted.

`PrepareRestoredAuthority` calls `recovery.InvalidateRestoredWithin` through the
existing `transaction.DamengTransaction` adapter and writes one bound native audit
inside the same kernel transaction. It seals a prepared candidate before the
kernel commits. It never publishes authority completion inside that transaction.
Only `VerifyCommittedAuthority` in a newly acquired custody can verify the actual
committed audit, full post-image (excluding only that bound audit UUID) and four
store trees. Absent/different readback stays unknown; it never repeats invalidation,
replays import, grants serving rights or deletes an original scope.

## Executable native acceptance

The opt-in `dameng_recovery_integration` tag adds
`TestActualDamengNativeMatchedRestoreAndCommittedAuthority`. Missing inputs fail;
they do not skip. The test starts no service, creates no database/account, migrates
no schema and accepts no license. The operator must retain exclusive stopped
synthetic source/target ownership, use admitted durable output/runtime storage,
provide the fully registered exact catalog and current lawful native inventory,
and provision a distinct empty target with the same principal/schema/version.
The source must contain populated model/dataset/training/preparation rows and live
Admin/Portal sessions. Its four existing file stores must pass the real domain
owners. A small SQL fixture or empty stores cannot satisfy this test.

From `services/control`, with the admitted native build inputs. Set `TMPDIR`
to an existing private admitted durable directory. If the foreground runner
provides compiler scratch, set `GOTMPDIR` only in the Go compiler environment;
the explicit `-exec` below removes it from the test process because Go1.27's
`testing.TempDir` otherwise inherits that compiler setting. Record actual backing
filesystems for the runtime temp, archives, stores and journals before execution.

```sh
go test -p 1 -tags=odbc_manager,dameng_recovery_integration \
  -exec '/usr/bin/env -u GOTMPDIR' \
  -run '^TestActualDamengNativeMatchedRestoreAndCommittedAuthority$' \
  -count=1 -json ./internal/operations/damengbackup -args \
  -dameng-recovery-disposable-target=true \
  -dameng-recovery-source=/private/source-recovery.json \
  -dameng-recovery-source-context=/private/source-context.json \
  -dameng-recovery-source-application=/private/source-platform.yaml \
  -dameng-recovery-target=/private/target-recovery.json \
  -dameng-recovery-target-context=/private/target-context.json \
  -dameng-recovery-target-application=/private/target-platform.yaml \
  -dameng-recovery-output=/private/recovery-new
```

All references are protected files, never credential literals. Choose a short
new output root so its native archive path is at most128 bytes. The test retains
the original archive/stores/intent/prepared/committed receipts and refuses an
existing output; do not rerun an uncertain import against that original target.
It independently checks committed session revocation before its final test
receipt. Native execution remains pending until a genuine source-bound receipt
exists. This test still does not establish restored HTTP authentication,
configuration/keyring closure, restricted runtime grants, physical workload
absence, Kunpeng/Kylin compatibility or third-party redistribution rights.

## Protected Control operator integration

The Control early route is `backup-native-dameng`, `restore-native-dameng`,
`verify-native-dameng-backup` or `verify-native-dameng-restore`. Only `--request`
and optional `--env-file` are admitted, once each. The strict versioned request
and explicit whole-file mapping are in `examples/operator-request.json.example`
and `examples/operator.env.example`. Examples contain nonworking image digests.
Commands do not use serving `OpenProtected`, expose its kernel, start workers,
create a DB/account, accept vendor terms or prepare runtime access. A native
CGO/odbc_manager build is required; other builds fail closed.

`native_operator.go` reads protected private application YAML through the owning
reader/decoder, admits the separate owner connector/schema/recovery inputs, and
uses one `dameng.Open` kernel with `transaction.FromDameng` and the existing
`WithNativeCustody`. Native process environment must match the existing verified
SDK launcher profile; arbitrary overrides and `LD_PRELOAD` are not admitted.
Owner credentials remain outside the captured application configuration root.
At least two bounded kernel connection slots are required for the pinned
maintenance session plus existing domain-owned read-only verification.

The application projection includes the actual keyring/HMAC/certificate/locale
material and protected runtime connector/schema files, validated by their existing
owners. A closed named-role inventory follows the selected typed owner/config/env
authorities. Nested MySQL settings use the existing `mysqlstore.Decode` authority,
including its explicit env mapping, before checking its selected password and CA
material. Arbitrary JSON/YAML keys or filename suffixes grant no file authority.
Unknown reference roles refuse explicitly; missing files, symlinks and escapes
refuse before SQL. Known configuration/secret/certificate references remain inside that projection,
with separate roles for
explicit existing image-owned asset fields and the named `knowledge.scratch_root`/
`object_storage.staging_root` temporary outputs. Temporary paths remain exact
typed references, separate from configuration, operation, image and four retained
store roots. Recovery never reads/copies/deletes their bytes or asserts physical
workload absence; fresh isolated runtime preparation remains required. Image roots are read-only content
pins, not OCI archives or exemptions for private keys. Image symlinks bind their exact link text and resolved target; targets must stay
inside a declared, independently hashed root. Unpinned host/interpreter fallback
is refused. Image digests use sorted traversal and SHA256 of newline JSON records
with fields `Path`, `Kind`, `Mode`, `Size`, `Link`, followed immediately by each
regular file's bytes. Directory size is0; link size/text are retained. Roots are
exact `/opt/projectone` and/or `/usr/local/bin/python3.12`, and metadata/byte/file
budgets come from the existing snapshot policy. These are content bindings, not
proof of an OCI signature or redistribution rights.

Restore exclusively creates the new configuration root from the original matched
backup before loading target application material. The request paths and effective
configuration remain exact: use separately isolated backing/mounts at the original
absolute paths; occupied roots refuse. No origin, callback, credential reference
or data root is silently rewritten. Changing that topology requires a separately
reviewed restore contract. Original bundles are read-only; completed archive
verification never opens them writable or changes modes. New output parents and
the restore configuration parent are writable; request/SDK/image inputs are not.

The original intent precedes file/SQL mutations. Unknown import/commit outcomes
never replay. The verify-restore command only checks the original candidate in a
new custody and may seal a missing completion from committed audit/full post-image
proof. Its output root therefore remains writable; it grants no runtime access.
Machine-readable success contains only fixed status, operation UUID and proof
hash; requests, references, key bytes and native diagnostics are never dumped.
Central dispatch/deployment hunks and actual vendor SQL remain separately tracked.

The current composition captures the four established model/dataset/training/
preparation stores. Additional selected mutable state owners (registry store/
prepared roots, private-cluster bundle mount and release-view directory) refuse
with `STATE_OWNER_BACKUP_UNIMPLEMENTED`, rather than being copied as configuration
or silently omitted. Middleware PostgreSQL operator/backup-request nested owners
currently refuse with `NESTED_OWNER_CLOSURE_UNIMPLEMENTED`. These are explicit
composition limitations; their configured deployment profiles are not claimed as
matched whole-platform backup support. Existing independent external-provider
state, runtime access and physical workload preparation remain separately owned.

The declared initial native operator profile is `native_four_stores_v1`, not the
full MVP backup path. Missing owner configuration never proves unsupported data
absent: original same-session/catalog/installation custody must find all16 named
artifact-object, image-registry, middleware and private-cluster relations empty
before export and again after matched capture. Missing catalog/query evidence or
any row refuses before export; there is no disable switch. The private
`profile.absent.json` proof binds the original database completion, rows, server,
endpoint and table inventory into operator completion. Restore checks this source
chain before import, then actual target relations after import and during
committed verification. Existing data requires the corresponding state-owner
implementation; it is not deferred as target-only qualification. The complete MVP
continues to use its existing registry-aware PostgreSQL operator path.

The current local operator checkpoint passed35 test roots/81 entries with no
skips (18.452s), the same complete race case set (122.502s), native command
checks, tagged vet and40 actual Control executable negative cases. Linux amd64
and arm64 Control artifacts used real CGO/external linking and ODBC dependency/
symbol readback; arm64 was compiled, not executed. Those checks used a private
f2982281 baseline plus the exact owned files and early-dispatch integration hunk.
Shared-main dispatch/deployment/release integration remains coordinator-owned.
No actual vendor SQL/restore, runtime activation, additional state-owner support
or physical-platform qualification follows from these local receipts.
