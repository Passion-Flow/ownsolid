# Control process and operator composition

`process_errors.go` adds bounded, sorted, deduplicated terminal diagnostics for
fixed process-composition codes inside joined errors. The existing `safeError`
line and exit status remain; vendor/wrapper text, private inputs and arbitrary
codes are never added. No worker, timeout, lease or fail-closed policy changes.
See the [diagnostic contract](../../../../docs/milestones/m17-offline-compose/process-diagnostics.md)
for source controls versus separately required installed-image evidence.

When `machine_server` is configured, `verify-serving-transport` additionally
requires `--expected-machine-json` with the complete four-field machine-server
object. Omission asserts absence and refuses a hidden dedicated listener. Both
intents are compared before either real TLS pair is read; existing protected
config and the four explicit machine env-file mappings remain runtime authority.
See the [machine verification contract](../../../../docs/engineering/deployment-transport/machine-serving-check.md).
An old browser-only init cannot qualify a configured machine group. Projection,
probe and installed lifecycle forwarding remain separate delivery gates.


[main.go](main.go) is the executable boundary for serving and explicit installation, schema, bootstrap, storage and recovery commands. It selects protected inputs before opening dependencies. Business rules remain in [modules](../../internal/modules/README.md); privileged maintenance mechanics remain in [operations](../../internal/operations/README.md).

[selected_database.go](selected_database.go) owns one selected database, its installation lock and complete schema readiness. [selected_operator.go](selected_operator.go) and mysql_native_commands.go route database-specific operator behavior. Do not construct a second pool inside a domain or treat a candidate migration directory as an activated schema.

[kubernetes_restore.go](kubernetes_restore.go) strictly dispatches native matched
PostgreSQL fresh-target restoration, independent files-only readback and read-only
committed-result verification before normal application construction. It admits
protected source/target/configuration/connection/tool inputs before SQL, then
lends the selected pool/installation-lock session to `postgresbackup`. It never
starts workers or silently prepares the target runtime role. After verified data
restoration, the existing restored-access owner consumes its bound context in a
separate explicit command. See the [restore runbook](../../../../docs/runbooks/kubernetes/postgres-restore.md)
for partial/unknown outcomes, role/serving writes and Kubernetes qualification.

[runtime_lifecycle.go](runtime_lifecycle.go) owns dependency construction, cancellation, worker readiness, stop/join order and closers. Component helpers such as artifactstores.go, imageregistry.go, task_events.go, middleware.go and monitoring.go connect their existing owners. The identity HTTP package still composes/routes domains; extracting that historical hub is a separate verified change, not an invitation to create duplicate owners here.

Configuration comes from [platform/config](../../internal/platform/config/README.md), protected command inputs and explicit policy environment mapping. Bootstrap packets and verification-store helpers preserve exact installation/artifact identity. Unknown commit, failed readiness, interrupted backup or failed worker shutdown must remain visible; cleanup cannot silently destroy the only recovery evidence.

When changing construction, update configuration/schema/examples, actual HTTP mounting, worker shutdown and [release builders](../../../../tools/release). New modules must be wired into current-authority, readiness/restore and both selected-database paths as needed. Source-only imports are not completed composition.

Focused native check: `go -C services/control test ./cmd/server`. Tagged selected-repository, native-command, runtime/broker and full-server tests require their declared isolated fixtures and resource admission. This documentation does not assert those profiles were run.

The explicit `releases-cache-v1` profile keeps the M01–M17 account22 graph,
release-view routes and existing worker lifecycle while optionally attaching the
M23 directory cache to the same model owner. `model_directory_cache.go` validates
protected local inputs without dialing; each cache operation owns its bounded
connection and SQL remains authoritative on unavailability. It adds no worker,
DDL, table, listener or readiness dependency on Valkey. Historical profiles keep
their cache rejection. Other post-M17 features remain closed. Use the new
[YAML](../../../../config/examples/control/releases-cache-v1.example.yaml) and
[explicit environment mapping](../../../../config/examples/control/releases-cache-v1.env.example).
`upgrade-releases` checks the same existing principal/catalog for this profile;
source/configuration, selected SQL, installed image and signed delivery evidence
remain distinct.

The selected-repository HTTPS fixture uses each configured Portal/Admin origin
in request URLs and maps only those two socket destinations to its private
loopback listener. This preserves matching SNI, DNS SAN verification, Host and
Origin under the production transport policy. Cache and task-event HTTP helpers
share that client; redirects and unknown dial targets refuse without changing
host DNS, system trust or TLS verification.

The tagged `current_startup_repository_integration_test.go` has three fixed roots
selected by the identity quality tools' `--group startup`: real `main/serve`
refusals in the same measured test executable, unbootstrapped verified loopback
TLS health, and schema failure/restart against one original PostgreSQL schema.
The recovery root observes Portal/Admin fatal checks in separate generations,
joins workers and listener closure, restores the exact ledger and reopens fresh
owners before checking ready200. Three in-process generations are one test entry
and one schema, not a native product process/restart qualification. See the
[startup contract](../../../../docs/engineering/deployment-transport/current-m01-startup-readiness.md)
and [quality entrypoints](../../../../tools/quality/identity/README.md). Source
checks do not establish measured compilation, SQL execution or retained readback.

Serving validates browser and optional independent machine TLS, then calls
`httpserver.ListenConfigured` before starting workers or announcing startup.
The returned group prebinds both sockets with one shared connection budget;
`Serve` joins their bounded shutdown before process composition joins workers.
The same domain instances and optional monitoring recorder receive the browser
handler and its explicit `MachineHandler`. Machine ingress never becomes a
browser route or proxy exception. See the [machine contract](../../../../docs/engineering/deployment-transport/machine-ingress.md).
No nil-TLS downgrade or fabricated request TLS is used. HTTP/proxy configuration source checks and
full native/Linux compilation are separate from remaining real SQL/browser,
read-only health probe and deployment/recovery work; see the [transport increment](../../../../docs/execution/mvp/deployment-transport-boundaries.md).

Dedicated machine health/probe uses the [machine-health contract](../../../../docs/engineering/deployment-transport/machine-health.md). Explicit `--audience machine` requires a dedicated group and protected CA; it never falls back to Admin.

`health-probe` has its own strict early CLI dispatcher, before database/worker
construction. The [frozen probe contract](../../../../docs/engineering/deployment-transport/health-probe.md)
defines exact required audience/kind/numeric dial/deadline and protected CA input,
same typed serve configuration/env-file authority, fixed output/errors and pending
acceptance. Its main YAML is a bounded pinned private snapshot through
`config.LoadProtectedWithEnvironment`; reachable Registry references use the
configuration owner's protected snapshot reader and pure decoder. Duplicate/unknown flags fail without printing input. This source
branch alone does not establish a passed probe, SQL readiness or installed
Compose/Kubernetes behavior; use its separately retained actual results.

`verify-serving-transport` is a separate strict early command implemented in
`serving_check.go`. It loads the same protected base and explicit policy file,
compares all selected transport fields with versioned public expectation JSON,
then invokes the real TLS material owner. It opens no SQL or listener, writes no
configuration and emits fixed redacted results. See the [contract](../../../../docs/engineering/deployment-transport/serving-check.md)
and [nonworking public example](../../../../config/examples/control/serving-expectation.example.json).
All eight fields are explicit; no ambient environment or expectation defaults.
Select matching validated YAML and policy inputs, pass their intended projection
through `--expected-server-json`, and explicitly rerun after correcting a refusal.
The command is a deployment prerequisite; actual init/lifecycle and installation
acceptance remain separate.

The tagged full serving account browser fixture now has a separately authenticated
restricted PostgreSQL role path with scoped local acceptance. Its [contract](../../../../docs/engineering/deployment-transport/fullserve-runtime-role.md)
keeps migration setup/final verification privileged and serving/restart on the
restricted selected pool. The native owner owns interrupted role cleanup. See [actual results and limits](../../../../docs/execution/mvp/fullserve-runtime-role.md);
native CLI and installed deployment acceptance remain separate.

The separate browser_cli_repository_integration_test.go helper now exercises the
real native serve/health-probe process boundary under the [native account contract](../../../../docs/engineering/deployment-transport/native-account-cli.md).
Inputv2 carries the measured native binary reference; [actual native start/restart](../../../../docs/execution/mvp/native-account-cli.md)
passes its positive scope. The selected readiness cancellation now preserves the original Go interruption
prefix and completes exact cleanup; see the same source-bound results. The accepted in-process role profile retains its original source/scope.

The tagged browser_bootstrap_repository_integration_test.go adds the fixed actual
native operator setup/result-loss reconciliation path before restricted serving.
It releases the Go installation lease, uses protected generated credentials and
real SIGPIPE/stdout loss, then verifies readonly reconciliation/state integrity.
See the [frozen bootstrap contract](../../../../docs/engineering/deployment-transport/native-account-bootstrap.md).
Source presence is not actual acceptance; native migration/backup/offline remain
separate. No general process selector or product test endpoint is introduced.

The current fixed native restart test includes a separate tagged throttle/mail
helper: real default email/IP admission, counter fingerprints across control and
original PostgreSQL restart, isolated expiry-clock recovery and both pending link
MIME deliveries. Its v2 result remains distinct from retained v1 restart and the
unchanged restore profile. See the [frozen contract](../../../../docs/engineering/deployment-transport/native-account-throttle-mail-restart.md);
source presence or compilation alone is not actual restart acceptance.

`releases.go` connects the optional protected release-view configuration to the current
Identity/selected-store owner and admitted locale registry. Startup/readiness reject dirty,
missing, foreign or same-UID projections; no worker or new pool is created. See the
[module guide](../../internal/modules/releases/README.md).

## Account composition

`account.go` owns `server.application_profile: account`: current identity and
license authorization, embedded locales, SMTP outbox, retention and the same
transport/listener/shutdown owners. It opens only the selected account schema;
no node/gateway/training/SSO/cache/broker/Kubernetes worker or vector extension is
constructed. `migrate`, bootstrap, restricted runtime grants, schema checks and
recovery consume the same profile. Later-feature settings are rejected, not
silently ignored. The default platform composition remains in runtime_lifecycle.go.

The minimal M01+M02 `workspaces` profile follows `serveAccount` with only the
Workspace handler added. `upgrade-workspaces --config <operator> --runtime-config
<runtime>` runs before normal schema readiness, holding the existing installation
lease and requiring both profiles explicitly. It never constructs later workers.
See [the M02 runbook](../../../../docs/runbooks/workspaces/README.md).

`account.go` also composes `server.application_profile: nodes`: identity/license/
locales/mail/retention, Workspaces and the existing node service/sweep only.
`upgrade-nodes --config <operator> --runtime-config <runtime>` extends populated
M02 plus its existing runtime role under the installation lock. Machine routes
are the closed discovery subset; later descendants return structured404.

`server.application_profile: components` constructs only M01-M04, reusing the
account lifecycle, Workspace/node services, bounded node sweep and component
callbacks/artifact handler. `upgrade-components --config <operator>
--runtime-config <runtime>` requires the exact installed M03ledger and principal.
Keep serving stopped until schema/grants/access checks pass. Fresh installations
use migrate/prepare-runtime-access/check-runtime-access/bootstrap-admin; no
platform selection, rebootstrap or reset is needed for an existing installation.


The cumulative maintenance profile uses the existing component owner/callbacks/sweep with its approved catalog. upgrade-maintenance --config <operator> --runtime-config <runtime> extends only the exact M04installation with the same restricted principal. Exact human/machine routers refuse M06+ descendants before optional dispatch. No additional daemon is started.


`upgrade-datasets` upgrades only the accepted M07 installation to the cumulative42-table/14-entry datasets profile. It retains the original runtime principal and supports schema-committed/grants-pending recovery. Serve starts and joins only the enabled dataset processor alongside earlier owners.

`upgrade-training` extends an installed accepted M08 database to training without rebootstrap or principal rotation. Serve opens the separate initialized training output store and joins the bounded training worker before releasing store locks. Earlier minimal profiles remain independent.


The cumulative `lifecycle` profile extends training with its real preparation store, joined worker and current holdout guard. `upgrade-lifecycle` extends only an installed M09 catalog and its existing runtime principal; it never selects the historical platform schema. Store initialization, readiness, exact route mounting and coordinated restore use the same profile.

The explicit policy-file admission also covers the six preparation file/store
operators: export/import/verify-preparation-files and initialize/check/record-
preparation-store initialization. They use the same LoadWithEnvironment boundary
as serve. Relative/empty files fail before reading configuration; an admitted file
must still pass protected-file, exact-key and complete typed validation. Database
migration/runtime-role/bootstrap commands retain their original protected-input
admission. The paired native preparation test deliberately selects a wrong YAML
store, proves refusal, then verifies actual accepted files through an explicit
correct environment override.

## Cumulative M12 source integration

The explicit `knowledge` profile retains M01–M11 (SSO source validation and joined
maintenance, preparation/holdout and all four stores) and adds only the existing
knowledge owner. `SSORuntime`/`LifecycleRuntime` select inherited responsibilities;
exact `SSOOnly`/`LifecycleOnly` predicates remain distinct. Forward account18 and
`upgrade-knowledge` preserve the17-entry prefix/existing principal; the expected
catalog is74tables/18entries, subject to actual current SQL verification.
Protected `knowledge-policy.env.example` maps every installed reference/policy
through the existing typed `--env-file`, with strict booleans and a protected
gateway inventory. The cumulative restore calls knowledge last in the same
transaction. See the [current integration contract](../../../../docs/execution/mvp/m12-cumulative-integration-20261026.md)
for pending executable evidence; source integration alone is not acceptance.

## Cumulative Portal composition — 2026-10-27

The explicit `portal` profile extends `plugins` with application projections,
private terminal notices and fixed user theme preferences. Account21 adds only
two tables; `upgrade-portal` retains the existing principal. `PluginsRuntime`
retains inherited tools; Portal maintenance joins startup/readiness/stop. Later
owners remain closed. See the [integration contract](../../../../docs/execution/mvp/m15-cumulative-integration-20261027.md) for current evidence status; source availability is not acceptance.


## Cumulative localization composition — 2026-10-27

The explicit `localization` profile extends `portal` through forward account22,
retaining all M01–M15 owners, 86 tables and the immutable21-entry prefix.
`PortalRuntime` includes both profiles; exact `PortalOnly`/`LocalizationOnly`
predicates remain distinct. `upgrade-localization` preserves the installed
principal and identical DML grants. Runtime locale packages remain immutable,
source-bound configuration inputs. See the [current M16 contract](../../../../docs/execution/mvp/m16-cumulative-integration-20261027.md).
This source checkpoint is incomplete pending current upgrade, runtime, browser
and offline delivery evidence; historical M16 receipts do not establish it.


## Cumulative M17 source boundary — 2026-10-27

The cumulative `releases` profile retains the M16 account graph and opens the existing release view before readiness. It shares the selected PostgreSQL pool and ordinary Admin read-audit authority. Missing/dirty/unbound projection fails readiness. Later platform-only owners remain closed; `upgrade-releases` verifies the existing account22 principal without altering schema or passwords.

`check-kubernetes-maintenance` is an early strict protected-config/environment-only
dispatcher. It uses the same selected PostgreSQL installation lock and read-only
transaction, with the existing startup deadline and bounded cleanup. The
[maintenance owner](../../internal/operations/postgresmaintenance/README.md)
checks the current releases catalog and remote-work/capacity predicates. Output
is historical and appears only after transaction and lock close; it is not
authority for a later backup. No worker, listener, grant or domain update occurs.
Current-source execution evidence remains separate from this source description.

`backup-kubernetes-postgres` is a separate strict early dispatcher with protected
operator configuration, runtime/request references and explicit backup policy.
It checks the existing runtime principal, retains the selected installation lock
across the [matched backup owner](../../internal/operations/postgresbackup/README.md)
and closes it before output. `readback-kubernetes-postgres-backup` reads a candidate;
`verify-kubernetes-postgres-backup` requires durable completion. Both are read-only,
accept no SQL/operator configuration and require the externally retained manifest
digest. Neither output nor a completed native backup grants Kubernetes maintenance,
upgrade or restored-runtime authority. See the [current scoped checkpoint](../../../../docs/execution/mvp/m18-postgres-backup-20261031.md).

## Selected optional-owner composition

`selected_owner.go` compares the actual PostgreSQL pool retained by the selected
database and identity owner; fresh transaction wrappers around that pool are not
foreign owners. MySQL still requires the identical selected Store. Middleware,
private-cluster, monitoring and release-view assembly share that check without
opening any backend or extending an activated catalog. Ordinary module
configuration, installed tenant and current graph checks still apply.

The original generic HTTP graph checks enabled node recovery owners during
initialization through `CheckRecoveryOwners`; missing recovery ownership remains
a startup/readiness error. The imported producer guard preserves minimal profile
requirements and current mutation transactions. Startup graph tests and the
selected PostgreSQL monitoring assembly gate are separate from actual recovery,
unknown COMMIT and installed backup/restore qualification.

## PostgreSQL installation lease request fence

`installation_lease.go` lends the original pinned installation session to a cheap
read-only check before request dispatch and before the first successful response
header. The same verification slot serializes this with full schema readiness;
a failed/unknown lease check latches loss for that selected owner and cancels the
original runtime lifecycle. A recovered business pool cannot adopt or reacquire
that lease. `Close` still reports an unconfirmed original unlock. Recovery is an
explicit fresh selected database plus fresh domain/outbox owners after the old
listeners/workers/owners have joined and closed.

The fence shares one admission budget across browser/machine entrances using the
existing HTTP maximum-active-requests policy, before any pinned SQL query.
The check uses the existing process HTTP request budget, independently of client
cancellation; schema inventory remains in readiness. Liveness keeps its existing
process-only contract. The fixed httpsnoop wrapper preserves optional writer
interfaces, refuses a success before any DTO/artifact/session headers escape,
and leaves original failed/unknown-COMMIT responses intact. The existing single
monitoring recorder wraps this guard.

A mandatory lifecycle-owned lease observer now also runs without incoming
requests. The typed HTTP `installation_lease_interval_seconds` (default5, range
1..60) controls its cadence; zero/false/empty cannot disable it. PostgreSQL uses
only the original pinned session. MySQL reuses its existing installation-session
verifier without activating an incomplete schema. The single existing native
Dameng full-readiness observer uses the shorter of its own readiness cadence and
this bound. No duplicate watcher, alternate pool, reacquisition or replay exists.
Each probe shares the existing HTTP request deadline, including the verification
slot wait. Failure irreversibly latches that owner, cancels the process lifecycle
and joins existing owners/listeners through their existing shutdown budgets.
The conservative process bound is cadence + request + two shutdown budgets
(listener shutdown followed by owner cleanup); a noncooperating owner still
reports an unconfirmed timeout rather than a successful join. Established HTTP
streams inherit the canceled process context; this is bounded liveness, not an
atomic SQL fencing token or proof about arbitrary hijacked sockets.
See the [monitor contract](../../../../docs/engineering/deployment-transport/installation-lease-monitor.md).

`TestActualPostgreSQLRestartCannotResumeLostInstallationOwner` uses the original
serveSelected lifecycle and a dedicated real PostgreSQL restart, with no old
ready probe, then checks refusal/join/unchanged unknown unlock and explicit fresh
full admission. Source/unit/native SQL results are separately recorded; original
M23 outage observations and terminal failure remain immutable.

`TestActualPostgreSQLIdleLeaseLossJoinsBackgroundOwnersWithoutRequests` starts the
actual Mail/Retention/SSO/node ownership graph, restarts its real PostgreSQL, sends
no HTTP/ready request after the fault until termination, checks the configured
join bound and irreversible old-owner latch, preserves UNCONFIRMED unlock, and
admits a fresh owner through full schema verification. These regressions require
an explicit durable runtime TMPDIR and no inherited compiler GOTMPDIR.

## Full PostgreSQL CPU MVP activation

`mvp-postgresql-cpu-v1` selects the full existing `serveSelected` graph and the
independent account22-derived29-entry/108-table lineage. It stays outside the
bounded AccountRuntime/ReleasesRuntime selectors. Startup and schema readiness
first verify the exact selected original lease/catalog, then require the strict
current DML-only runtime principal; superuser operator credentials and incomplete
grants cannot serve. The existing mandatory joined observer/request fence and
unknown-COMMIT/unlock diagnostics remain unchanged.

`upgrade-mvp-postgresql-cpu-v1` and `inspect-mvp-postgresql-cpu-v1` reuse the
original selected pool/installation session and protected effective runtime input.
Only canonical explicit --config/--runtime-config/optional --env-file are admitted;
foreign/duplicate/empty path flags fail before SQL. Both operator and runtime use
the same existing typed protected overlay parser. Output follows confirmed original
lease close, and an unknown schema/grant outcome emits no terminal success.

Native maintenance admits only the exact legacy releases or new MVP profile.
Backup operator/runtime/request profiles must match the request's effective
profile, and MVP principal verification borrows the held session without opening
a second pool. Restore consumes the effective runtime configuration but does not
require the fresh target role before the original restored-role preparation.
The required ReleaseView is opened through its existing selected-pool constructor
and actual stored projection remains part of full graph readiness. Optional
providers still require their own configured and validated owners.

See the [activation contract](../../../../docs/engineering/deployment-transport/mvp-postgresql-cpu-v1-activation.md)
for exact commands, cold-path restrictions and recovery. Source wiring and focused
CLI/SQL checks are separate from a complete serving/artifact/browser/native-backup
or target-environment gate; the profile flag alone establishes none of them.
