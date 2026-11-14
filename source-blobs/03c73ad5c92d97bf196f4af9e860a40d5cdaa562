# Runtime database access and catalog binding

This owner implements protected operator commands selected by the existing control
CLI. `input.go`/`mysql_input.go` validate explicit private connection references;
`migration.go`/`mysql_migration.go` bind migration to the exact installation lock
and target. `access.go` and `mysql_access.go` prepare/check separate restricted
runtime principals. Restored context loaders bind fresh destinations to the
original backup/SQL archive and installed tenant. They are not browser APIs.

`upgrade-releases` also admits the explicit `releases-cache-v1` target through
`ReleasesRuntime`. Both targets require the same verified account22 installation
and existing runtime principal. The operation only checks catalog/role/access;
it issues no DDL, grants, credential rotation or automatic cache mutation. Current
cache policy/secret/endpoint provisioning remains with the configuration owner.
The installed profile regression compares every table row, original OID/SCRAM,
restricted readiness and DDL/journal denial across repeated admission.

`catalog.go` owns the shared initial tables. `postgres_catalog.go` adds only active
PostgreSQL variants, including migration27's three private-cluster tables. MySQL
receives only its independently activated catalog; candidate statements never
receive invented grants. `mysql_catalog.go` independently lists the22 tables
registered by MySQL204–265; the shared85-table list and PostgreSQL catalog remain
unchanged. The MySQL runtime set is108 DML tables plus SELECT-only access to
`schema_migrations` (433 exact table/privilege pairs). Tests compare this set with
the actual embedded CREATE TABLE assets. This is an explicit schema/role boundary;
current native migration, restricted serving and populated backup/restore still
need their own evidence. Preparation grants DML on this closed catalog and read
only on the migration journal; it does not grant schema ownership, DDL, trigger
control, TRUNCATE or delegation. Repeat preparation/check must verify the same
real target and role rather than silently repair conflicting privileges.

The model-family migration 61 source candidate adds only model_families and
model_family_members to the PostgreSQL Control principal's closed table list.
The existing DML-only grant contract and MODEL-owned immutable triggers remain;
no gateway role or other-engine catalog is extended. The actual predecessor-role
grant-resume test must pass before this becomes a published runtime baseline.
See [formal admission](../../../../../docs/research/model-family-schema-admission.md).

## MySQL uncertain account changes

MySQL account creation, grants, and unlock are not one rollbackable transaction.
Retain the protected runtime input, original intent and its separate `.created`
receipt together. [The access implementation](mysql_access.go) binds that receipt
to the original intent SHA and the observed account authenticator. Ownership
attributes or a missing completion audit alone do not establish safe resume.

After `MYSQL_RUNTIME_ACCESS_CREATE_UNCERTAIN_REVIEW_REQUIRED` or
`MYSQL_RUNTIME_ACCESS_AUTHENTICATOR_RECEIPT_REQUIRED`, preserve the original
material and inspect the same locked target through check/reconcile. An existing
account without a matching `.created` receipt remains a review-required case;
do not synthesize the receipt, drop/recreate the account, reset its password, or
broaden grants. Resume only a partial state accepted by the original intent and
authenticator checks. Keep ordinary and restored-access intents separate.

Changes must synchronize ordered migrations, module repositories, restored-state
validation, the fixed catalog and real restricted-role acceptance. M28 activation
cases are frozen in [activation-cases.json](../../../../../docs/milestones/m28-private-cluster/activation-cases.json).
The tagged SQL tests require a fresh explicitly owned repositoryfixture schema,
TLS/SCRAM fixture and `repository_integration,privatecluster_schema_integration`.
No customer endpoint or default production migration is accepted by that fixture.
Package controls and a compiled artifact alone do not establish actual grants.

The tagged `dameng.go` path borrows two admitted native Stores through
migrate.WithReadyRuntime. It holds the native installation lock, checks exact
schema/runtime privileges, binds the license deployment UUID and installation
object ID, and verifies actual Store provenance. Its context expires when the
callback returns. It does not create database principals or activate a serving
selector. Missing native metadata/permission/driver support fails closed.

`workspaces.go` owns only the versioned delivered-M01 upgrade: verify exact
account/new ledger and old principal, migrate, grant DML on `workspace_notes`,
verify the same restricted runtime connection. It accepts exact old or complete
new grants on repeat, never repairs arbitrary privilege conflicts or recreates
roles. The installation lock excludes serving throughout both phases.

`nodes.go` owns the delivered-M02 forward evolution. It verifies the exact old or
new ledger and the original restricted principal, appends schema atomically and
grants DML only on node_join_tokens/managed_nodes/node_tasks in a separate atomic
phase. Repetition recovers schema-committed/grants-pending execution. Complete
new grants are verified; arbitrary intermediate/elevated privileges fail closed.
The role OID, password verifier and M02 data are preserved.

`components.go` owns `upgrade-components` from the exact delivered M03catalog.
It preserves the original runtime principal/OID/password and extends DML only on
`managed_components`; ledger access stays SELECT-only and runtime DDL is refused.
Schema and grant transactions are separately atomic, with exact old/new grant
readback and repeat recovery for schema-committed/grants-pending interruptions.
Use the [component runbook](../../../../../docs/runbooks/components/README.md).


`maintenance.go` owns upgrade-maintenance from the exact delivered M04catalog. Only component_log_snapshots DML is added to the existing role. Migration 32 later drops that table from PostgreSQL: component logs live in the log store ([ADR 0012](../../../../../docs/decisions/0012-telemetry-store-boundary.md)). Exact old/new grants are accepted for schema-committed/grants-pending recovery; role OID/password and all prior grants remain. Runtime migration metadata is SELECT-only and DDL is denied.


M06 cumulative integration (2026-10-26): explicit `models` extends maintenance
through entry11 and `upgrade-models`, retaining the installed prefix/principal.
Only approved immutable import, deployment/GPU ownership, exact leased file
transfer and bounded model worker are added. Later owners remain structurally
inactive. Minimal restore invalidates imports and preserves uncertain GPU leases;
see [current integration](../../../../../docs/execution/mvp/m06-cumulative-integration-20261026.md).

`gateway.go` owns `upgrade-gateway` from delivered M06: preserve the exact11-entry prefix and existing principal, append entry12, extend only seven gateway/history table DML. Exact old/new role verification permits schema-committed/grants-pending recovery; it never rotates credentials or bootstraps the installation.


`datasets.go` owns existing-principal M07→M08 upgrade-datasets, transactional seven-table DML grants and repeat/no-op or schema-committed recovery. Runtime cannot mutate the schema ledger or create tables.


`lifecycle.go` owns installed-M09 `upgrade-lifecycle`: exact15-entry prefix, sixteen-entry target and existing principal; thirteen new tables receive only DML in an atomic grant phase. Schema-committed/grants-pending repeat and complete no-op retain role OID/password, existing grants and ledger SELECT-only access. It never reboots or rekeys an installation.


## Current cumulative M11 source integration — 2026-10-26

The explicit `sso` profile extends the accepted M10 lifecycle graph with only
Gitea account linking/sign-in. `LifecycleRuntime` retains preparation, holdout and
four-store behavior in both profiles. The exact forward catalog is67 tables/17
entries; use `upgrade-sso` with the existing runtime principal. The immutable
sixteen-entry prefix remains unchanged. Current execution and limitations are in
[the integration contract](../../../../../docs/execution/mvp/m11-cumulative-integration-20261026.md);
source integration alone is not current provider/build/installation acceptance.

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
transaction. See the [current integration contract](../../../../../docs/execution/mvp/m12-cumulative-integration-20261026.md)
for pending executable evidence; source integration alone is not acceptance.

## Current cumulative M13 source integration

The explicit `workflows` profile extends accepted knowledge through forward
account19, six schema1 workflow tables and `upgrade-workflows`. KnowledgeRuntime
retains knowledge plus all inherited SSO/lifecycle/four-store owners. Current
workflow SQL uses the original schema1 fields; later plugin/tool result columns
are absent and schema2 is refused. Startup owns the joined workflow executor;
readiness and exact HTTP routes use this owner. Common restore appends workflow
invalidation last in its existing transaction. The expected80table/19entry
catalog, role-preserving interrupted upgrade and late-owner rollback require
actual current SQL evidence. See [the current integration](../../../../../docs/execution/mvp/m13-cumulative-integration-20261026.md); source availability is not acceptance.

## Current cumulative M14 source boundary — 2026-10-27

`UpgradePlugins` and `upgrade-plugins` accept the exact populated M13 or current
M14 catalog with a separately protected runtime configuration. Account20 and the
four new DML table grants have explicit atomic boundaries. Preserve the existing
role OID, password verifier and all old grants; do not create/rekey a replacement
principal. A schema/grant interruption fails serving access and the same explicit
upgrade completes the missing grant set. Unexpected role authority/catalog drift
refuses. Restricted runtime DML and exact catalog checks precede restart.

Implementation and current local execution evidence are recorded separately in the
[current M14 handoff](../../../../../docs/execution/mvp/m14-closeout-current.md).
Physical qualification and real manufacturer approval remain separate.

## Cumulative Portal composition — 2026-10-27

The explicit `portal` profile adds only application projections, recipient-private
terminal notices and fixed themes to the cumulative M14 graph. Account21 and
`upgrade-portal` retain existing data and the runtime principal. Notice maintenance
joins startup/readiness/stop; M16+ owners stay closed. See the [integration contract](../../../../../docs/execution/mvp/m15-cumulative-integration-20261027.md). Source availability is not acceptance.


## Cumulative localization composition — 2026-10-27

The explicit `localization` profile extends `portal` through forward account22,
retaining all M01–M15 owners, 86 tables and the immutable21-entry prefix.
`PortalRuntime` includes both profiles; exact `PortalOnly`/`LocalizationOnly`
predicates remain distinct. `upgrade-localization` preserves the installed
principal and identical DML grants. Runtime locale packages remain immutable,
source-bound configuration inputs. See the [current M16 contract](../../../../../docs/execution/mvp/m16-cumulative-integration-20261027.md).
Current upgrade, runtime, actual installed-font browser, native recovery and
same-image cold-install controls pass their declared local profile. Final offline
package/source/publication binding is retained by the current M16 delivery receipt.
The exact202 resource release, native human review and physical qualification
remain pending; a partial component never establishes those results.


## Cumulative M17 source boundary — 2026-10-27

`upgrade-releases` verifies the exact delivered account22 schema, held installation lease and existing restricted runtime principal using its protected runtime configuration. Since M17 adds only a file projection, it performs no DDL, grant or credential repair. The offline operator separately owns pre-change backups, image changes, activation and recovery; this command alone is not a product-upgrade receipt.


The guarded migrate-with-runtime-context entrance preserves the exact bounded
unknown/rolled-back COMMIT classification from the migration owner before generic
failure/cancellation handling. A lost acknowledgement returns no success and
requires independent original-ledger readback. The M17 verified-TLS case covers
one acknowledged, one not-forwarded and one suppressed-ack attempt, each exactly
once; two direct original-server reads establish its durable outcome.
