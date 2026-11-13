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

[agent_gateway.go](agent_gateway.go) serves `serve-agent-gateway`: the agent session stream from a process beside control, with its own pool and no installation lock, fenced on the lock's holder and drained on stop ([ADR 0048](../../../../docs/decisions/0048-agent-gateways-beside-control.md)). It builds the agent path with `identityhttp.NewAgentGateway`, the constructor control's own handler uses.

Its session drain is a lifecycle-owned cancellation loop, so confirmed worker
shutdown includes the drain operation before process-owned stores close. This
does not itself join arbitrary request handlers or bind dataset/training readers;
the original constructor owner and actual request/file-owner shutdown remain
required for ARC-111. See the [reader ownership boundary](../../../../docs/research/agent-gateway-database-role.md#concrete-reader-constructor-dependency-and-process-ownership).

[runtime_lifecycle.go](runtime_lifecycle.go) owns dependency construction, cancellation, worker readiness, stop/join order and closers. Component helpers such as artifactstores.go, imageregistry.go, task_events.go, middleware.go and monitoring.go connect their existing owners. The identity HTTP package still composes/routes domains; extracting that historical hub is a separate verified change, not an invitation to create duplicate owners here.

Configuration comes from [platform/config](../../internal/platform/config/README.md), protected command inputs and explicit policy environment mapping. Bootstrap packets and verification-store helpers preserve exact installation/artifact identity. Unknown commit, failed readiness, interrupted backup or failed worker shutdown must remain visible; cleanup cannot silently destroy the only recovery evidence.

When changing construction, update configuration/schema/examples, actual HTTP mounting, worker shutdown and [release builders](../../../../tools/release). New modules must be wired into current-authority, readiness/restore and both selected-database paths as needed. Source-only imports are not completed composition.

Focused native check: `go -C services/control test ./cmd/server`. Tagged selected-repository, native-command, runtime/broker and full-server tests require their declared isolated fixtures and resource admission. This documentation does not assert those profiles were run.

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

## Cumulative Portal composition — 2026-10-27

## Cumulative localization composition — 2026-10-27

## Cumulative M17 source boundary — 2026-10-27

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

## Schema upgrade

`upgrade-schema` and `inspect-schema-upgrade` take `--config`, `--runtime-config` and an
optional `--env-file`. They accept an installation whose ledger is a prefix of the one
lineage, migrate it, then grant the existing runtime principal the application tables.
`serve` requires the DML-only runtime principal and refuses a superuser. See ADR 0003.
