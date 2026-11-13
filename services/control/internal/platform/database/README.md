# Selected SQL infrastructure

This directory supplies real PostgreSQL/MySQL infrastructure to the control composition root. Domain repositories own their statements and business rules. SQL dialect selection is explicit; there is no generic query rewriting layer.

The versioned `releases-cache-v1` graph maps only to the existing localization
account22 catalog, exactly as M17 releases does. Cache adds no SQL state, grants
or migration; caller profile identity remains distinct. The profile/catalog and
installed restricted-principal tests verify this boundary separately from full
serving or signed deployment qualification.

| Package | Ownership |
| --- | --- |
| [database.go](database.go) | PostgreSQL open, installed schema/checksum validation and ordered migration behavior |
| [transaction](transaction/README.md) | Borrowed selected transaction/executor, explicit vendor statements and safe outcome classification |
| [mysqlstore](mysqlstore/README.md) | Protected typed MySQL connection, pool/session invariants and safe driver boundary |
| [mysqlmigrate](mysqlmigrate/README.md) | Ordered MySQL DDL/seed journal, observed object postconditions and dirty-step reconciliation |
| [dameng](dameng/README.md) | Conditional Linux UNIXSOCKET connector, pinned native/session identity and retained ODBC owner/budget/cleanup; shared transaction adapter and explicit checker, separate from the platform engine selector |

[cmd/server/selected_database.go](../../../cmd/server/selected_database.go) selects and owns the pool plus installation lock. Database operation policy and protected connection references enter through [config](../config/README.md). Migration catalogs live under [control migrations](../../../migrations); SQL candidates do not become active merely by existing on disk.

Preserve exact migration/object checksums, fixed session semantics, installation scope and the current runtime-grant catalog. PostgreSQL transaction/advisory-lock behavior and MySQL DDL/user-lock behavior are different protocols. Shared business tests must exercise both supported paths rather than treating a text substitution as compatibility.

When a schema or database policy changes, update the owning repository, migration catalog/checksums, grant/bootstrap/restore validation, typed config/examples and meaningful selected-SQL tests together. Ambiguous commit or migration execution needs explicit readback; do not turn it into an unconditional retry.

Focused native checks: `go -C services/control test ./internal/platform/database ./internal/platform/database/transaction ./internal/platform/database/mysqlstore ./internal/platform/database/mysqlmigrate`. Real server, restricted-role, migration interruption and native backup/restore profiles remain separate from package checks.

The local model-family integration candidate assigns migration 61 to the unchanged
MODEL-owned SQL plus additive platform cell constraints and registers 122 logical
tables. Its existing schema 60 catalog fingerprints remain strict: migration and
serving refuse the new shape until
actual fresh/restored fingerprints are qualified. This source is not a published
schema baseline yet. The [admission research](../../../../../docs/research/model-family-schema-admission.md)
records owner inputs, retention, role and resource gates. No table-count-only
admission or guessed fingerprint is allowed.

The optional [Dameng check command](../../../cmd/dameng-connector-check/README.md)
uses explicit [protected connector inputs](../../../../../config/examples/domestic-platform/README.md)
and the existing public-manager build gate. Its local IPC path does not activate
Dameng as a platform metadata engine. The optional [native foundation owner](dameng/migrate/README.md)
and [schema operator](../../../cmd/dameng-schema/README.md) now supply a scoped
21-table/68-step account prefix inside the composed 104-table/454-step catalog.
The immutable 14-table/31-step foundation stays the first prefix. Registered
domain fragments cover knowledge, middleware, MongoDB imports, task delivery,
training, model preparation, image registry and private clusters. The selector
accepts an explicit `dameng` engine. Opening it still requires the
`cgo && odbc_manager` build; this Mac's default build returns
`DAMENG_NATIVE_SERVING_BUILD_REQUIRED`. Proprietary-driver qualification remains
separate from this source registration.

## M01 account schema

`account.go` selects the account migration catalog through the validated server
application profile. It reuses the unchanged M01 identity DDL, adds local-only
session source columns, and retains actual signed-license history and audit lookup.
Exactly 16 tables include the migration ledger. No vector extension or later
capability schema is required. Catalog checksums prevent opening it as a platform
schema. Existing full catalogs and MySQL/Dameng selection remain independent.
M02 uses the explicit account-derived extension documented below; changing to
the independent platform selector still fails closed and is not an upgrade.

## M01 to M02 forward evolution

The explicit `workspaces` profile retains all four account entries and appends
version5 (unchanged original Workspace DDL) and version6 (membership incarnation
identity). Fresh and upgraded databases have17tables and6ledger entries. No
vector extension or later schema is needed. The full platform catalog remains
independent; a selector change never rebases checksums. The dedicated upgrade
operator also extends the existing runtime role without rekeying it. See the
[Workspace runbook](../../../../../docs/runbooks/workspaces/README.md).

## M02 to M03 forward evolution

`nodes` retains all six `workspaces` entries byte-for-byte and appends the existing
node authority DDL plus `account/000008_node_protocol.up.sql`. Fresh/upgraded
installations have20tables/8ledger entries. Shared repository fields remain inert
and constrained to discovery: `read_only`, `collect_facts.v1`, empty payload and
no inference epoch/fence. No vector, component or workload table is introduced.
Use `upgrade-nodes` for an existing installation and runtime principal.

## M03 to M04 forward evolution

`components` retains all eight accepted `nodes` entries and appends
`account/000009_components.up.sql`:21tables/9entries. It replaces only the
discovery constraints that M04 must extend and creates `managed_components`.
Required shared repository version/configuration/verification fields are present,
but constrained to the fixed1.12.1recipe/default configuration; no log table,
maintenance/inference profile or later operation is admitted. Entry8and the old
platform000004are unchanged. Use `upgrade-components`; switching to platform
is never an upgrade. Exact restore invalidates account/node authority, retaining
component observations as stale evidence rather than asserting physical removal.


`maintenance` appends only account/000010_maintenance.up.sql to the immutable nine-entry M04 prefix:22tables/10entries. It explicitly evolves approved profile/capability/operation and version/config/prior constraints and adds bounded scoped component_log_snapshots. It does not replay platform000005 or rewrite existing rows. Fresh/upgrade catalogs agree; upgrade-maintenance preserves the restricted existing principal.


M06 cumulative integration (2026-10-26): explicit `models` extends maintenance
through entry11 and `upgrade-models`, retaining the installed prefix/principal.
Only approved immutable import, deployment/GPU ownership, exact leased file
transfer and bounded model worker are added. Later owners remain structurally
inactive. Minimal restore invalidates imports and preserves uncertain GPU leases;
see [current integration](../../../../../docs/execution/mvp/m06-cumulative-integration-20261026.md).

M07 `gateway` appends only account/000012_gateway.up.sql (35tables/12entries), reusing M02 authority_id. Seven distribution/history tables support current shared repositories; no cutover, training, Registry or derived owners are admitted. Original models/platform catalogs stay independent. See the [current M07 integration](../../../../../docs/execution/mvp/m07-cumulative-integration-20261026.md).


M08 `datasets` appends unchanged PostgreSQL000008/000019 as profile entries13/14 after the accepted gateway prefix, yielding42tables. Applied migrations and membership authority are preserved; upgrade-datasets extends the original principal.

M09 cumulative `training` appends account/000015 after the unchanged14-entry M08 prefix:50tables/15entries. It preserves existing inference lease IDs while extending the same exclusive reservation domain. Use upgrade-training with the existing runtime principal; historical platform000009 is not replayed on this baseline.


M10 cumulative `lifecycle` appends only account/000016 after the immutable fifteen-entry M09 prefix:63tables/16entries. It reuses M06 model/deployment identities and M07 alias target history, adds preparation/review/cutover owners and extends the same GPU lease domain. Approval identity explicitly forbids NULL. Historical PostgreSQL10/19/20 bytes are unchanged; `upgrade-lifecycle` preserves the installed principal.


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

Current workflow structural readiness keeps two exact PostgreSQL18.6 catalog
forms: fresh migration and official logical dump/reparse. Only six enum-array
CHECK and two partial-index text-cast renderings differ; all other bytes/fields
remain bound. Missing tables/columns, changed predicates, disabled triggers or
future tables refuse serving/restore. The actual source/restored metadata and
negative checks are retained with the current M13 evidence.

## Current cumulative M14 source boundary — 2026-10-27

Explicit account20 extends the exact ordered19-entry M13 prefix to84 tables,
adding four plugin tables and schema2 workflow checks/provenance. Existing19 SQL
files stay immutable. `plugins_catalog.go` verifies the ten affected workflow/
plugin table definitions and total inventory, including raw fresh/pg_dump-restored
PostgreSQL18 catalog spellings; a count alone is insufficient. `Migrate`/`Check`
and restore admission share this catalog authority. M13 `workflows` remains its
independent80-table/schema1 catalog. Used schema2/plugin data refuses disposable
test rollback; application rollback requires its matched pre-upgrade backup.

Implementation and current local execution evidence are recorded separately in the
[current M14 handoff](../../../../../docs/execution/mvp/m14-closeout-current.md).
Physical qualification and real manufacturer approval remain separate.

## Cumulative Portal composition — 2026-10-27

The explicit `portal` profile extends `plugins` with application projections,
private terminal notices and fixed user theme preferences. Account21 adds only
two tables; `upgrade-portal` retains the existing principal. `PluginsRuntime`
retains inherited tools; Portal maintenance joins startup/readiness/stop. Later
owners remain closed. See the [integration contract](../../../../../docs/execution/mvp/m15-cumulative-integration-20261027.md) for current evidence status; source availability is not acceptance.


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

Current repository fixtures select `localization` through the same explicit
profile input as the runtime ledger; the paired workflow owner also binds that
profile. This is actual isolated PostgreSQL/transaction coverage, separate from
a complete runtime deployment. M16 late-restore tests arm an owned sessions
statement trigger after the strict catalog check; the legacy users arm remains
for earlier profiles. No production catalog field or fail-closed check is ignored.


## Cumulative M17 source boundary — 2026-10-27

The explicit `releases` graph uses the exact localization account22 catalog. Schema selection normalizes only this named profile and never changes the caller's profile identity or selects platform. Fresh/migrate/check/transactional recovery keep the existing exact catalog and COMMIT classification. There is no M17 business migration.
