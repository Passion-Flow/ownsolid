# Scoped private-cluster proof Jobs

This M28 source package owns one bounded Kubernetes proof-Job path in an operator-prepared isolated namespace. It is separate from node enrollment, general cluster provisioning, production workload scheduling and the M29 Xinchuang/secure connector boundary. Source import does not establish that the main control server, schema, UI and recovery lifecycle are integrated or accepted; follow the [mainline handoff](../../../../../docs/milestones/m28-private-cluster/mainline-handoff.md) for that work.

## Entry points and dependencies

[service.go](service.go) constructs the service from matching identity, Workspace and licensing owners. Its transaction boundary rechecks current actor/session and Workspace membership, with distinct Admin operations and Portal role checks. Installation/license state is locked before connection/run state; the request does not choose an authoritative tenant.

- [connections.go](connections.go): list admitted profile choices, bind, observe, disable and explicitly rebind a connection.
- [runs.go](runs.go): start a run, continue a known prepared stage, reconcile, cancel and clean up using persistent operation identity.
- [repository.go](repository.go): tenant/Workspace-qualified connections, runs and scoped pagination with explicit PostgreSQL/MySQL statements.
- [projection.go](projection.go): return a verified execution proof only when the retained successful result, challenge, Pod identity, logs and observed image identity agree.
- [http/handler.go](http/handler.go): strict request envelopes and service dispatch; the owning server must supply the real authenticated Origin/CSRF entrances.

The package also depends on [transaction](../../platform/database/transaction), [privateclusterproof](../../platform/privateclusterproof), [release contracts](../../../../../contracts/release/v1/private_cluster.go) and the production-shared `projectone/offlinefactory/pkg/bundle` library. Do not remove that tools/release library from production source closure or substitute a browser-supplied image digest.

## Configuration and remote scope

[config.go](config.go) owns typed policy/profile validation and Load/LoadEnvironment. The component reference is `PROJECT_ONE_PRIVATE_CLUSTER_CONFIG_FILE`; [configuration examples](../../../../../config/examples/private-cluster/README.md) describe protected CA, token and image-proof files, explicit HTTPS origin/TLS name, allowed private CIDRs, exact cluster/namespace/service-account/installation/Workspace identities, resource budgets and restart semantics. Global control/Compose/Helm integration must map this existing boundary rather than add ambient kubeconfig or scattered environment reads.

[client.go](client.go) verifies the signed M28 image proof and architecture, protected credential material, CA/hostname validation and resolved address allowlist. Redirects and ambient proxies are not accepted. [discovery.go](discovery.go) rechecks cluster version, namespace identity/labels, service-account identity, required permissions and forbidden excessive permissions. Quota, network-policy and node observations describe inspected API state; they do not prove effective network isolation or production capacity.

[scope.go](scope.go) and the [scope command](../../../cmd/private-cluster-scope/main.go) render operator-reviewed preprovisioning objects only. They do not apply objects, issue credentials or contact Kubernetes. The [probe command](../../../cmd/private-cluster-probe/main.go) is the bounded program used in the admitted Job; the bearer credential stays with the control client and is not passed to its Pod.

## Persistent states and recovery

The active [PostgreSQL migration27](../../../migrations/postgresql/000027_private_cluster.up.sql) and guarded down script own connections, runs and mutation intents; original candidate bytes remain historical source. The [MySQL statements](../../../migrations/mysql/candidates/private-cluster) remain candidates outside its active catalog/grants. Active source alone is not actual installation or restricted-role acceptance. Connection state distinguishes active, disabled and restore-fenced; fingerprints bind configuration, credentials and approved image. Changes require an explicit same-scope rebind under current authority.

The normal create sequence persists prepared, lease-pending/acquired and Job-pending/observing stages. A repeated Start idempotency key reads its recorded run rather than resending the remote mutation. Unknown remote responses and commit uncertainty retain the unresolved stage for explicit reconciliation. [provider.go](provider.go) checks exact labels/specification, UID and resource version; cleanup observes Job/Pod disappearance before removing the original lease and releasing the connection. A deletion timestamp alone is not absence or completed cleanup.

New creation requires current business/license admission. Safety cleanup retains its separate current-authority path after business expiry. [integrity.go](integrity.go) exposes VerifyStoredWithin and InvalidateAuthoritiesWithin for readiness/restore composition; restore fencing preserves remote work for reconciliation instead of silently starting replacement Jobs. Successful proof output establishes only the observed bounded probe, not exactly-once execution, effective CNI policy, production/GPU workload or another architecture.

## Changing and checking this owner

Synchronize [M28 contracts and cases](../../../../../docs/milestones/m28-private-cluster), [frontend request journal/recovery](../../../../../apps/web/packages/privateclusters), release manifest/image proof, [probe deployment](../../../../../deploy/private-cluster), schema catalogs/grants and the actual server/readiness/restore wiring. Preserve original request identity across browser refresh and explicit retries; a frontend remount must never resend an uncertain mutation.

Focused native check, after pinned-toolchain and external resource admission:

    go -C services/control test ./internal/modules/privateclusters/... ./internal/platform/privateclusterproof

Config, scope, provider, HTTPS wire and handler tests cover controlled failures. Paired repository tests require `repository_integration` and `privatecluster_integration`; actual local-cluster tests additionally require `privatecluster_local` and their real isolated environment. Their exact inputs are in the [runbook](../../../../../docs/milestones/m28-private-cluster/runbook.md). This documentation batch did not execute those tests or establish cluster/mainline acceptance.

The original operator recovery now invokes the module-owned borrowed transaction
fence without loading provider credentials or constructing an enabled network
client. The exact installed table graph must be present; partial state fails.
Fencing never clears active_run_id, remote UIDs or immutable intent history.
[Activation cases](../../../../../docs/milestones/m28-private-cluster/activation-cases.json)
separate actual migration/grant/atomic restore checks from full ledger/probe and
whole database-aware backup/import qualification.

The existing maximum_body_bytes policy now also bounds complete public data JSON
including Encoder newline. List/Run pages retain only budgeted items, use an
unscanned next-row sentinel and check the cursor/envelope. The HTTP module checks
every successful projection before parent serialization. Oversize returns a safe
error without data truncation, remote release or intent acknowledgement. Public
maximum_response_bytes exposes this same selected budget; there is no new setting.
Pure encoding/configuration/public-policy and selected actual SQL page recovery
checks pass; real authenticated HTTP and browser behavior remain pending in
[response-budget cases](../../../../../docs/milestones/m28-private-cluster/response-budget-cases.json).

Readiness now retains one connection observation at a time independently of the
public page size; run rows continue to stream. Its two added native SQL cases
now pass in the actual selected PostgreSQL fixture, including complete-page
overflow with zero module projection, nondefault pagination and retained corrupt
readiness graph refusal. No provider credentials were loaded; component
reconstruction is not a process restart or whole backup/restore qualification.


## Current canonical input and permission boundary — 2026-11-03

Protected profile/policy and image-proof wrapper fields use their exact JSON tag
names, including nested arrays. Case aliases and canonical-plus-alias duplicates
are refused. A server origin is a bare HTTPS origin, with no query/fragment
delimiters. CA input contains only current signing CA certificates and whitespace;
PEM preambles and certificates without certificate-signing usage are refused.

The credential transport is shared with newClient after image approval; it never
grants image authority itself. Its real HTTPS tests exercise protected files,
Bearer delivery, private CIDRs, hostname verification, proxy exclusion and refusal
without contacting a cluster. Kubernetes response decoding allows future API
fields while rejecting aliases of consumed fields, duplicate keys, invalid UTF-8,
null whole responses and null scalar authority fields. Every permission review
must contain an explicit boolean status.allowed, even for a forbidden permission.
An omitted/null denial is not evidence of restricted authority. List responses
require explicit nonnull items arrays and their API/kind/metadata envelope; a
missing/null collection cannot establish physical absence or release capacity.
See the current
[parallel handoff](../../../../../docs/milestones/m28-private-cluster/parallel-20261103.md)
for exact source-bound results and remaining installed gates.
