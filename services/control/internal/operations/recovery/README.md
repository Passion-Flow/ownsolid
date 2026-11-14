# Restored authority and external-workload recovery

This package owns explicit protected operator actions after restore or uncertain workload termination. [The dispatcher](../../../cmd/server/main.go) exposes its command families using the selected identity service and typed configuration. Browser permissions and an operator reason alone do not prove physical workload removal.

## Restore transaction boundary

The explicit `account` application profile first verifies its exact four-entry
checksummed ledger in the same borrowed transaction, then invalidates every
account authority, pending encrypted mail and replay record with an audit. It
does not query absent later-domain tables. A platform ledger selected as account
is refused before mutation. The platform/default owner list below is unchanged.

[InvalidateRestored](restore.go) opens the selected restore transaction; `InvalidateRestoredWithin` participates in a caller-owned transaction without committing it. [Repository handling](restore_repository.go) uses the selected database dialect and bounded operation context. Account authorities and explicitly enumerated domain owners are invalidated/fenced through their own APIs. [Artifact stores](artifactstores.go), [middleware](middleware.go), [private clusters](private_clusters.go), and [task events](task_events.go) have additional restoration fences.

The private-cluster fence checks the three persisted tables (`private_cluster_connections`, `private_cluster_runs`, and `private_cluster_intents`) before calling [the domain invalidation owner](../../modules/privateclusters/integrity.go) in the same borrowed transaction. PostgreSQL requires all three tables even when the runtime component is disabled. The MySQL path skips only when the component is disabled and all three tables are absent; a missing/partial required schema fails closed. This guard does not establish an active MySQL M28 catalog.

The domain marks connections `restore_fenced`, increments their revision when newly fenced, and refuses any connection that remains unfenced, including a revision-limit refusal. It leaves remote UIDs, active-run pointers, intent history, immutable proofs and unresolved effects intact. This step requires no provider credentials or network calls and does not delete Kubernetes Jobs, Pods or Leases. Do not clear capacity fences or report physical removal from its database result.

The owner list in `restore.go` is executable scope, not automatic coverage of every future module. When adding a domain, explicitly integrate its restoration behavior and transactional tests. A database commit, including a successful invalidation audit, does not terminate an external process or delete provider objects. An unknown commit result must be reconciled against actual state before another mutation.

## Workload and object commands

- [Inference](inference.go): `fence-inference-node` prevents further use; `reconcile-inference-removal` verifies the supplied removal receipt against the retained identity and state.
- [Training](training.go) and its [file boundary](training_files.go): `prepare-training-removal` writes the bound removal challenge; `reconcile-training-removal` validates the matching returned receipt.
- [Model preparation](model_preparation.go) and its [file boundary](model_preparation_files.go): corresponding `prepare-preparation-removal` and `reconcile-preparation-removal` commands retain their job/node identity and protocol bindings.
- [Object stage](object_stage.go) implements `initialize-object-stage`. [Object snapshot verification](object_snapshot.go) implements `verify-object-snapshot` with the selected Workspace/object/path binding; it is not permission to substitute another object or a storage-success stub.

Removal challenges and receipts are explicit protocol artifacts. Keep their original bindings and enforce receipt shape, identity, timing, and state checks from [receipt.go](receipt.go). Replacing a remote object with the same name must not be treated as proof that the retained workload was removed.

## Recovery procedure

1. Preserve the database state, original challenge/receipt, selected configuration, and operator diagnostic after a failed or uncertain operation. Keep the relevant workloads fenced.
2. Reestablish the actual selected database and operator prerequisites, then inspect the retained identities. Do not manufacture success receipts or use database invalidation as a physical deletion acknowledgement.
3. Have the corresponding workload owner perform and attest the real removal through its defined protocol. Reconcile that exact receipt; a mismatch requires investigation rather than clearing retained reservations manually.
4. Only proceed to readiness/access restoration when the relevant owner has established its required result. For store/object recovery, verify the bound stored bytes and metadata before declaring recovered content usable.

## Verification and maintenance

From `services/control`, the package test entry is `go test ./internal/operations/recovery`. [Borrowed restore tests](borrowed_restore_test.go), [selected restore tests](selected_restore_test.go), [receipt tests](receipt_test.go), and [snapshot scope tests](object_snapshot_scope_test.go) exercise their specific transaction and refusal boundaries. [Preparation runtime repository tests](preparation_runtime_repository_integration_test.go) require the `repository_integration` build tag and its selected isolated repository fixture.

[Private-cluster activation tests](../databaseaccess/private_cluster_activation_integration_test.go) provide `TestActualPostgreSQLPrivateClusterActivation/borrowed_restore_fence_atomicity` under `repository_integration && privatecluster_schema_integration`. That separate PostgreSQL fixture checks original restore composition, rollback on a rejected fence, and retention of unresolved remote identity; it is outside the untagged recovery package command above. It does not establish full backup/import, MySQL activation, or actual Kubernetes cleanup.

A unit or repository pass is not target-runtime removal qualification. Preserve actual external execution and unknown-outcome evidence separately.

The cumulative `gateway` restore verifies its exact12-entry ledger and adds distribution invalidation to the models owners in the same transaction. It preserves request metadata/unknown capacity and model files; credential/challenge invalidation never proves external termination. Existing inference removal accepts the actual selected minimal ledger.


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

For `plugins`, the common borrowed restore transaction first checks the exact
84-table/account20 catalog and retains inherited account/SSO/lifecycle owners.
Plugin grant disable/authority rotation precedes workflow interruption in that
same transaction; a late workflow failure rolls every cumulative owner back.
Fresh login and explicit reviewed grant enablement are required. Restore never
automatically installs, regrants, retries or reinvokes a tool. SQL, all four stores,
SSO keyring and immutable artifacts must be restored as a matched set.

Implementation and current local execution evidence are recorded separately in the
[current M14 handoff](../../../../../docs/execution/mvp/m14-closeout-current.md).
Physical qualification and real manufacturer approval remain separate.


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
