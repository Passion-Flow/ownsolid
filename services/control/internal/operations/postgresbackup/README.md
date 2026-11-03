# Matched PostgreSQL backup and fresh-target restore owner

This package composes the existing four domain snapshot owners and current
PostgreSQL maintenance gate. Its caller lends the selected pool and the exact
live installation-lock session, retaining close/unlock authority. It starts no
worker or listener. Backup never migrates, grants or invalidates authority; the
explicit fresh-target restore reuses the existing cumulative recovery owner.
Neither path assumes physical workload absence. The Kubernetes controller must establish and retain
its own current maintenance/Lease/UID/claim/image authority.

`Backup` validates the explicit runtime/configuration projection, existing
private roots, current schema22/86/tenant/work quiescence and pinned client tools.
It holds the real dataset/training/preparation namespace locks, verifies all
four owners, exports accepted files and writes their existing audits before the
logical PostgreSQL export. A fresh read-only transaction on the same installation
session records all86 table contents as server-computed SHA256 streams and all
public sequence values. A later transaction must observe the identical contents.
The logical dump imports the exact fingerprint transaction's exported snapshot;
the exporting transaction stays open until `pg_dump` joins. Its historical ID is
recorded in the manifest, with no claim that it remains importable afterward.
The native dump uses a private short-lived service/password scope, a fixed custom
archive command and explicit single-endpoint transport; no URI/password is placed
in argv or diagnostics. Raw native diagnostics remain bounded and unexposed.

Configuration/keyring/locale bytes and effective typed runtime configuration are
retained separately. Database credentials stay outside that projection. Image
assets retain their exact declared paths and signed source/image binding. The
caller must provide a complete private configuration projection; missing files
refuse, and custom file/executable/directory references must be inside it. A
field name alone cannot classify an arbitrary path as an image asset.

The candidate manifest appears after actual exports/dump/copy. A separate pinned
Control invocation calls read-only `Verify`, streams all hashes and runs actual
`pg_restore --list`. It receives no operator configuration or database credential.
After independent readback, current SQL, source files, configuration and request
are checked again before exclusive durable readback/completion publication.
Partial output is retained on failure; existing roots are never overwritten.
Cancellation terminates and joins the fixed native process. Close/output failure
does not erase an already-published historical completion or authorize replay.

Inputs and policy belong to the [v1 request](../../../../../contracts/release/v1/postgres_backup.go)
and [explicit policy example](../../../../../config/examples/kubernetes/postgres-backup.env.example).
The [contract](../../../../../docs/milestones/m18-kubernetes/postgres-backup.md)
and [runbook](../../../../../docs/runbooks/kubernetes/postgres-backup.md) distinguish
native files/SQL verification from restored-runtime and Kubernetes qualification.
Private-file/tree protocols reuse the existing MySQL backup implementation under
this distinct owner; MySQL source and historical evidence are unchanged. This
owner uses projected allocation plus the explicit fixed-byte reserve.

Store roots remain current-owner0700. Descendants admit only current-owner0700
directories or the models owner's sealed0555 derivative directories; regular
immutable0444 files retain their existing single-link/no-write checks. Copying
assembles private0700 directories, then preserves the source descendant modes
bottom-up with directory synchronization and full source/destination readback.
It never chmods a source model or changes any domain verifier. Symlinks,
hardlinks, unsupported directory modes and replaced roots still refuse.

Focused checks: `go -C services/control test -race ./internal/operations/postgresbackup`.
The `repository_integration,postgres_backup_integration` fixture requires its exact
fresh disposable PostgreSQL/client container, database marker and protected inputs;
it refuses absent prerequisites. Its declared empty-domain-store/native scope
does not establish populated artifacts, fresh restore, TLS, Kubernetes or CSI.

`Restore` admits a distinct fresh database name/system/OID/ownership marker and
four empty locked private directories. Immutable intent precedes fixed native
SQL import; all86 tables/sequences must match before existing domain imports and
independent current Control file/roundtrip readback. Original recovery owners
invalidate authority with the bound post-image/audit in one borrowed transaction.
Before exclusive completion it reloads source/configuration/keyring/locale facts,
the same held target and pinned directory identities. Existing/partial scopes
refuse replay. `VerifyRestore` is a separate read-only observer; it never imports,
invalidates, grants, starts a worker or manufactures a missing completion. Its
new PID must preserve the original durable target/intent binding. Later role or
serving writes need their own evidence because they change the exact post-image.

The [restore request](../../../../../contracts/release/v1/postgres_restore.go),
[contract](../../../../../docs/milestones/m18-kubernetes/postgres-restore.md) and
[runbook](../../../../../docs/runbooks/kubernetes/postgres-restore.md) define the
strict entrypoints and partial/unknown outcome handling. Current role preparation
uses `databaseaccess.PrepareRestored` only afterward. Native tests and tagged real
PostgreSQL restoration have separate receipts; source alone establishes neither
populated store/served-auth nor Kubernetes/CSI qualification.

The tagged `TestActualRestoredPostgresHTTPReadinessContentAndAuthority` is a
separate continuation of a retained native target. Protected inputs bind the
original manifest/completion/archive, target scope, current executable and image.
It starts the real full `releases` Control CLI with the existing restricted role,
Knowledge/readiness/store owners, signed fixture license and trusted TLS for
both exact advertised origins. The populated mode checks actual model/dataset
IDs through both entrances. The distinct original-authority mode requires raw
tokens retained privately before their original commit/backup, backed-up revoked
row/hash/epoch proof, immediate source validity, exact served401
`AUTH_SESSION_EXPIRED`, and current-credential200 controls before/after.

`http_inputs_repository_integration_test.go` owns only the isolated fixture's
typed input and separate serving configuration. `http_prepared_repository_integration_test.go`
pins all setup/credential/license/configuration bytes before a new serving
attempt can reuse them. Attempt identity is separate from setup identity;
incomplete/uncertain setup never reissues sessions, license imports or restores.
The error decoder checks the existing production RFC problem producer, including
media type and HTTP/body status agreement. No production handler is substituted.
Child process termination and join, protected bounded logs and separate attempt
receipts are part of the case. Private credentials/keys/DSNs stay outside Git.

The [HTTP checkpoint](../../../../../docs/execution/mvp/m18-postgres-http-20261031.md)
retains actual populated40.06s/reuse33.98s, native original-token custody21.68s
and exact original-token HTTP8.99s results, plus the original decoder13.28s
failure. HTTP execution is local amd64 emulation; native PostgreSQL execution is
arm64. These are dated profiles, not current Kubernetes images/Jobs or CSI proof.
The separate [native cancellation checkpoint](../../../../../docs/execution/mvp/m18-postgres-native-cancellation-20261101.md)
now passes actual blocked SQL import/SIGTERM, joined native child/backend,
single-transaction rollback and empty SQL/four-root/publication readback after
EACH refused replay/verification. Its retained source mount is read-only and its
new target is independent. The executable case is
`TestActualPostgresRestoreSQLCancellationJoinsNativeAndRefusesReplay` in
`native_fault_repository_integration_test.go`; it requires exact protected
`/fault/inputs.json` and retained `/fixture` proof, not ambient deployment input.
The [partial-file checkpoint](../../../../../docs/execution/mvp/m18-postgres-file-failure-20261101.md)
adds a distinct actual populated-source/new-target Linux case. Its child lowers
only its own file-size limit, then executes unchanged Control; full SQL imports
before a real model write fails and retains capped staging bytes. Parent limits,
complete SQL, all four roots and original records are checked, including after
each refused replay/verification. The child helper is not an independent pass.
Strict `/fault/inputs.json` binds `file_limit` plus explicit `maximum_file_bytes`
in128 KiB–1 MiB; `sql_cancel` forbids a cap. These are isolated test inputs, not
new runtime configuration/environment keys. The separate
[unknown-COMMIT checkpoint](../../../../../docs/execution/mvp/m18-postgres-commit-unknown-20261101.md)
now passes an actual authority COMMIT reply loss through an explicitly selected
isolated loopback proxy. PostgreSQL commits; the original Control fails without
completion and retains its lost-lock diagnostic. A new native read-only observer
verifies the exact audit/post-image without replay or missing-file publication.
Strict `commit_unknown` input binds a loopback `proxy_address`; other modes
forbid it. No protocol/authentication/SQL payload is logged. Native fault results
remain separate source-bound profiles; current Jobs/images/offline/lifecycle and
actual Kubernetes/CSI qualification remain required.

## Exact native client distribution

`PostgreSQLBackupPolicy.client_distribution` is the single optional packaging
selector shared by backup, archive verification and fresh restore. Omitted JSON
keeps the original canonical bytes and exact upstream 18.6 descriptor. Explicit
`upstream` expects the same descriptor; `pgdg-bookworm-18.6-1.pgdg12+2` expects
the complete `18.6 (Debian 18.6-1.pgdg12+2)` descriptor. Semantic
`expected_version` remains fixed 18.6. Paths, per-executable SHA, ownership/mode,
single-link, platform and bounded process/cancellation checks stay mandatory.
Unknown builds, suffixes, changed release, null/boolean values, duplicate fields
and case aliases refuse. No wrapper normalizes native output.

Only the explicitly selected existing backup/restore policy env-file may
override the request selector. The env loader uses the JSON name before its
`omitempty` option; ambient environment and Helm/Compose expansion are not
additional authorities. See the [distribution contract](../../../../../docs/milestones/m18-kubernetes/native-client-distribution.md).
Synthetic version-process tests establish exact admission behavior. Signed
package provenance, dependency loading, current complete image and native SQL
execution remain separately bound checks.

## Actual current packaged native tools

The existing tagged fixed-Job integration fixture accepts a protected explicit
`artifact_source: current-image` with the exact approved PGDG client_distribution.
It executes only packaged /usr/local/bin/control and /usr/local/bin/kube-job,
without production executable overlays. Omitted selectors retain the legacy
overlay/upstream request behavior. The [qualification contract](../../../../../docs/milestones/m18-kubernetes/native-image-sql.md)
keeps profile admission before source SQL and all native/configuration/lock/
capacity/proof assertions. The current image's default UID 65532 is preserved;
this isolated fixture explicitly runs at UID 65534. These are container process
contexts, not OS/account changes.

The local current-image case exercises schema 22/86 with 33 rows/seven initialized
store files, actual native backup and independent byte/SQL archive readback. A
distinct network-none verifier mounts only the backup read-only and one protected
public request; source volumes, operator YAML and DSNs are absent. Its readonly
proof does not repeat backup or write an attempt. This is not populated-store,
serving/TLS, Kubernetes/CSI, signed installed-release or physical qualification.
Actual current-client fresh restore and remaining installed/controller work keep
separate contracts/receipts; prior populated/default-client results are unchanged.

The optional [measured target roots](../../../../../docs/milestones/m18-kubernetes/postgres-restore-roots.md)
are consumed by `restoreInputBindings` before the target connection and by
`Restore` immediately after acquiring all exclusive directory locks. They must
match the existing typed configuration's four paths and actual device/inode/UID/
GID identities. They never select or rewrite those paths. Legacy omission keeps
the original native behavior; fixed Job integration must always supply them.

The tagged `TestActualPackagedPostgresFreshRestoreThroughFixedJobRuntime` consumes
a protected current-image profile and retained readonly backup without a live
source volume/SQL connection. It uses separate narrow initialization, builtin
current artifacts, a fresh marked target and native measured-root/post-image/
four-owner roundtrip checks. Source01 passes full22/86 at the basic initialized
store profile. Its later kernel-readonly observer fails opening the dataset
namespace lock for write; this remains an explicit implementation limitation.
See the [incomplete image/restore checkpoint](../../../../../docs/execution/mvp/m18-native-image-restore-20261101.md).

`openVerificationOwners` now selects the three existing store owners' dedicated
verification lock openers only for `VerifyRestore`. They keep exclusive locking
and all private namespace guards while opening the ownership descriptor read
only. `openOwners` remains the ordinary backup/restore path. The [repair contract](../../../../../docs/milestones/m18-kubernetes/postgres-readonly-owners.md)
requires a new pinned image/fresh target for kernel-readonly revalidation; the
original current-image failure is never overwritten or relabelled.

The repaired verification openers pass source02 package checks and a separately
manufactured current-image/new-target full22/86 restoration plus actual SQL
observation with all seven input mounts kernel-readonly. Original failed source01
is retained. Its [repair checkpoint](../../../../../docs/execution/mvp/m18-postgres-readonly-owners-20261101.md)
records byte custody/no-resend and separate pending controller/CSI/serving scope.


## Explicit cumulative MVP lineage

The protected request's optional `application_profile` selects the separate
`mvp-postgresql-cpu-v1` forward lineage. Omission retains the original releases22
request bytes, manifest shape and86-table checks. The MVP selection must match
both typed operator/runtime configurations and uses the database owner's exact
29-entry/108-table catalog. Source/export/import/read-only post-image checks
carry that selection; the restored runtime context retains it and the actual
schema version. The four original store owners and snapshot protocols remain.

The new optional `mvp_source` manifest binding records the actual same-session
source system/database/backend and runtime principal OID/salted-verifier hash,
plus bounded external ownership fingerprints. It never stores the verifier or
credential. An isolated target cannot reuse the original system/database-OID
pair. Full SQL content still includes every original artifact, middleware,
private-cluster, task-event, grant and physical-reservation row.

The optional `mvp_source.source_metadata` records actual server version180006
and installed vector0.8.7 from that same original snapshot query. New writers
always retrieve these facts; legacy omission preserves its exact JSON bytes and
supplies no new facts. Explicit malformed, null or unapproved facts refuse, and
the original completion recheck compares them. The [source metadata checkpoint](../../../../../docs/runbooks/postgresql/mvp-source-metadata-20261103.md)
records focused compatibility controls and an actual same-backend29/108 SQL pass.
New Scope completion must require its own original nonnull candidate facts;
historical candidates and the current full image retain separate qualification.

The explicit MVP path now composes the registry owner's all-Workspace operator
snapshot at `artifacts/imageregistry`. It is supplementary artifact custody;
the four original domain namespaces and snapshot list remain unchanged. The
manifest binds the registry receipt, source root identity, payload tree and typed
configuration digest. A credential-free independent reader projects only the
retained signing trust bytes and verifies the original signed payload. All four
exports plus the supplement share the existing aggregate file/byte budget.

Fresh restore validates a separate empty registry CAS, the same declared registry
origin/CA/signed release/trust and its pinned directory identity before SQL. It
imports full SQL, the four original domain stores and then the registry files;
independent file readback precedes the single original authority/audit transaction.
The original restore intent and files record carry optional registry identities
and receipts. Their omitted fields preserve historical releases JSON. Subsequent
read-only verification reopens target files against that original import receipt
without replaying SQL, imports or authority invalidation.

Exact source runtime principal/grant checking reuses
`databaseaccess.LoadRuntimeWithEnvironment` and the held-session
`VerifyMVPPostgreSQLCPUV1Principal` before backup allocation and completion.
Fresh restore does not require a runtime role before the later original role
preparation stage. The real29/108 binding case checks system/backend/role
fingerprints, all108 table contents and partial-grant refusal. Complete default
package tests under UID65534 and focused supplementary-boundary checks pass;
these establish package behavior only. Actual current-image populated native
backup/restore, reopened target/both entrances and the signed offline/M26 consumer
remain separate execution gates until their source-bound receipts exist.

The tagged `TestActualMVPInitializedStoresSupplementNativeFreshRestore` is an
explicit cheap native integration fixture for the new lineage. It requires the
original privately marked disposable server, actual source/image descriptor and
packaged production Control/client bytes before its SQL admission barrier. Its
two Workspace pins contain tiny real locally signed OCI files and its four
business stores are initialized only. It composes actual native SQL export/import,
fresh registry CAS, existing role/session controls and equal-byte root replacement
refusal. The [native critical-path checkpoint](../../../../../docs/runbooks/postgresql/mvp-native-critical-path-20261103.md)
records its actual Linux20.59s pass and a separate network-none verifier with only
the37-file backup mounted read-only. It does not establish populated
model/preparation, complete installed image, HTTP or signed offline consumer
qualification. Original preflight/setup failures remain retained privately.

## Safe native evidence observations

`ObserveBackup` and `ObserveRestore` run the original complete readonly verifiers
and return response-only `native_postgresql` references. The [reference checkpoint](../../../../../docs/runbooks/postgresql/mvp-native-reference-20261103.md)
defines exact canonical/file digests and the `16975e21` wire dependency. Durable
candidate/readback/completion records are unchanged; a completion hash is never
inserted into the earlier readback that it hashes. References expose no roots,
credentials, principal names or verifier material, and do not verify a signed
release class. That independent authority remains with the original scope owner.
Missing original restore completion yields the original readonly observation and
no completed-restore reference. The source25/new-fixture26 case verifies actual
backup/restore reference links and that missing-completion behavior; CLI response
wiring and the final signed offline consumer remain separate integration gates.

The independent [target-marker initializer](../../../../../docs/runbooks/postgresql/restore-target-initialization-20261103.md)
adds explicit readonly discovery and one pinned fixed comment on an already
created pristine PostgreSQL target. It borrows the original selected pool and
installation-lock lease, records a durable exclusive intent/completion in a
separate private journal, and never retries an uncertain mutation. Inspection
cannot adopt a marker without the original intent or publish a missing
completion. It starts no runtime/schema/role owner. Shared wire and direct
producer/CLI protocol checks pass; integrated main dispatch and actual new
disposable-database effects have separate scope. The actual original CLI helper
now passes four fresh/foreign/nonempty/lost-COMMIT cases on PostgreSQL18.6,
including readonly missing-completion observation. Exact disposable fixtures are
cleaned after retained evidence. Full main/image/Engine/Scope and explicit later
runtime-role activation remain separately required; this owner never creates
that role as part of marker initialization or silently repairs native Restore.

The separate [readonly four-root inspector](../../../../../docs/runbooks/postgresql/restore-roots-inspection-20261103.md)
derives existing empty target paths only from the protected typed runtime. It
reuses original readonly directory handles/locks and returns actual four-root
identities only after every unlock/close succeeds. It opens no SQL, store owner,
CAS, scratch or record and writes no initialization. Scope must retain physical
custody, bind these observed roots before final request freeze, and keep the
original Restore's nonnil-pin relocking/comparison. Focused wire/owner/CLI and
tagged vet/compile checks pass individually; the enclosing source47 resource
attempt remains failed. A separately admitted same-binary ext4/readonly case
passes12 subcases, including real release failure and the original CLI handler.
Full main/image/Scope and physical-custody qualification remain separate.
