# Agent gateway database role

Research for ARC-111. Reviewed against the current PostgreSQL 18 documentation
on 2026-11-13. This note defines the acceptance boundary before implementation;
it does not activate a new role, grant, credential or serving mode.

## Upstream approach

[PostgreSQL CREATE ROLE](https://www.postgresql.org/docs/18/sql-createrole.html)
provides a login identity and connection limit independently of any table grant.
The gateway needs its own connection budget so exhausting the session tier cannot
consume control's role limit. A pool limit alone cannot enforce the aggregate
limit across several processes using the same login.

[Role membership](https://www.postgresql.org/docs/18/role-membership.html)
can confer both inherited privileges and permission to switch roles. Merely
setting NOINHERIT does not establish isolation: the current preparation policy
also refuses membership in either direction, ownership, superuser, database/role
creation, replication and bypass-RLS authority. Preserve those restrictions.

[GRANT](https://www.postgresql.org/docs/18/sql-grant.html) supports separate
read and write rights on individual objects. Effective authority includes PUBLIC
and memberships; checking only explicit ACL entries misses inherited grants.
[Security-definer functions](https://www.postgresql.org/docs/18/sql-createfunction.html#SQL-CREATEFUNCTION-SECURITY)
need a trusted search path and selective execution privileges. Keep the existing
private audit-chain owner private; a gateway must not read or mutate its head.
Audit-trigger execution is not permission to grant the caller direct head access.

Comparable infrastructure separates component identities from administration:
[Kubernetes etcd operation](https://kubernetes.io/docs/tasks/administer-cluster/configure-upgrade-etcd/)
uses authenticated clients, while [etcd RBAC](https://etcd.io/docs/v3.6/op-guide/authentication/rbac/)
assigns read/write rights to explicit key ranges. These support the principle of
a process-specific principal; ownsolid keeps PostgreSQL table/function grants
rather than introducing another datastore or copying etcd's permission model.

## Existing boundary

[ADR 0048](../decisions/0048-agent-gateways-beside-control.md) serves only the
agent session stream and health routes from the dedicated process. Enrollment,
results, renewals, event batches and file transfers remain on control's machine
entrance. Do not grant their complete write surface merely because the shared
constructor also creates their handlers.

The session backend reaches heartbeat and held claim. Their transitive owners
include node credentials/cursors/tasks, component and inference observations,
training and preparation claims, model/dataset validation, current site/cell and
Workspace status, audit, liveness flush, task wakes, node-view invalidation and
shed policy. Inventory these actual statements, triggers and worker paths before
activating an allowlist. A read-only-node trace alone cannot prove permissions
for delegated claims or background flushes.

`runAgentGateway` currently opens the normal configured serving pool and calls
`database.CheckRuntimeWithin`. That predicate intentionally requires full
business DML. `installationFence.check` calls `CheckRuntimeReady` again after a
new installation-lock holder appears. Changing startup alone would make the
first control restart reject a correctly restricted gateway. Both admission
paths need a dedicated predicate while preserving exact migration/catalog checks
and the current transient-storage versus incompatible-schema distinction.

The runtime-access operator owns protected credentials, installation-lock and
exact target binding, transactional PostgreSQL grants and principal readback.
It must remain the owner of preparing the new role. No gateway process may create
or alter its own login, grants or password. Existing control principal grants,
OID and SCRAM verifier remain unchanged across introduction and retry.

## Chosen approach and rejected alternatives

Use an explicit PostgreSQL gateway principal and separate pool/aggregate limits,
with protected password-file input and the same configured database endpoint,
TLS policy, schema and installed identity. Admit only the closed catalog of
permissions needed by every active session/worker path. Enumerate the required
privileges and reject excess effective rights, unrelated objects, ownership,
membership, DDL, trigger control, delegation and journal writes. Preserve
SELECT-only migration-ledger access and the private audit-head exclusion.

Prototype the permission closure outside product code against the current real
PostgreSQL catalog first. Exercise real heartbeat, claim, observation flush,
reconnect and delegated paths as the restricted principal; use SQLSTATE 42501 as
evidence to review a missing right rather than granting an entire schema. Record
each required table/operation with the owning runtime path and negative tests.
Only enable product composition when that closure holds.

A second login with the same business DML was rejected: it isolates the connection
budget but fails the least-privilege requirement. Membership in the control role
was rejected for the same reason. Search-path substitution, query rewriting and
catch-and-retry with privileged credentials were rejected because they bypass
real authorization. A separate gateway database was rejected for this item;
per-cell metadata topology belongs to ARC-201.

No agent wire change is required. Old v1 agents retain their current stream,
fallback, lease, cursor and retry behavior. Existing gateway binaries/configs
continue to use their existing control principal during a rolling upgrade. The
new principal must coexist; never revoke the old principal while those processes
or control still depend on it. Installer provisioning and final switch-over need
separate qualified delivery changes, without weakening either role's admission.

## Licenses and offline behavior

No new dependency, model or dataset is proposed. The existing Go toolchain uses
[BSD-3-Clause](https://go.dev/LICENSE), pgx uses
[MIT](https://github.com/jackc/pgx/blob/master/LICENSE), and PostgreSQL uses the
[PostgreSQL License](https://www.postgresql.org/about/licence/). Each permits
commercial and offline use and redistribution with the applicable notices.
Kubernetes/etcd documentation is design evidence; their code is not imported and
no additional etcd process or network service is introduced. Synthetic fixture
identities, credentials and rows must remain outside Git. Runtime preparation,
login, serving and recovery must work without a network route off the site.

## Failure coverage and completion

- Missing/wrong credential, invalid TLS, foreign database/schema or lost
  installation lock refuses preparation without changing grants or either role.
- Repeated preparation preserves both principal identities and credential
  verifiers. Interrupted work is read back before any deliberate retry.
- A gateway can heartbeat, claim, flush observations and recover after a control
  restart; schema mismatch and an unavailable database retain distinct verdicts.
- Every current delegated profile works through its real owner. Cross-node,
  cross-Workspace, disabled-node and stale-lease requests remain refused.
- Gateway sessions cannot change human identity/membership, licensing or unrelated
  application state, mutate the schema journal, truncate, disable triggers,
  inspect the audit head or switch to the control/operator role.
- Exhaust the gateway's real aggregate connection limit while control continues to
  obtain a connection; each pool and worker releases its owned connections on
  failure and shutdown. Revoke a required grant and add an excess grant separately;
  startup and post-restart readiness must both refuse.
- Finish with the installed two-gateway simulator flow, actual privilege readback,
  disconnected operation and the complete repository gate. Any capacity statement
  additionally needs the corresponding measured simulator workload.

ARC-111 stays unticked until the complete permission closure, operator preparation,
serving/readiness, delivery path and installed flow have all passed. No reduced
role or successful installed result is claimed by this research note.
