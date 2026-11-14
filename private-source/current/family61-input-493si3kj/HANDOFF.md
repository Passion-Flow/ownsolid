# Model family formal-lineage input package

Source: `db8435034a2a94a3285a059f6bd40237322ae1c2`
Tree: `25e5262c4eddd521d7c075862cf8293e5faf61c4`
ARC prerequisite: `0f47a66bcb84c585b00bb1a92bf30f455017c1c9`
Own branch: `codex/lane-m-family61`

This is an uncompiled, unrun and unpublished owner adaptation. It is not an
acceptance result. MOD-103 and the other roadmap boxes remain open. No gate,
compilation, SQL execution or lock wait is authorized by this package.

## Scope

The four owner commits change only models and its two research notes. They adapt
family/history tests to actual migration 61 and an actual separately authenticated
restricted Control role through the original LoadRuntime/Prepare/Check operations.
They add local site/cell checks to retained history, exact selection, retirement,
retained derived graphs and exact-version execution, plus eleven cursor binding
dimensions. Broader catalogue/import/cache/transfer scope remains MOD-101/102/109.
No API, Web, Agent, gateway, platform, migration or grant catalog is changed.

## Inputs

The bundle contains only the four owner commits after the exact ARC prerequisite;
its verification output records that prerequisite. Consume it in the original
owner's isolated checkout, preserve ARC changes and future fingerprint commits,
and review the model-only commit range. Do not publish this private branch or
replace another checkout/ref. The patch is the same range. The source archive
contains the complete tracked tree, without dependencies or build outputs.

## Pending original-owner registration and joint allocation

- `services/control/internal/platform/database/catalog.go` still pins schema-60
  fresh/restored catalogs. ARC must collect and register actual 61 fresh, restored,
  and restored-at-55-through-61 fingerprints on the pinned PostgreSQL. Keep the
  predecessor-60 exact checks and CatalogSQL unchanged. Never guess a fingerprint.
- `services/control/internal/platform/database/database.go` already has 61/122 in
  the ARC prerequisite. The numbered SQL is byte-identical to that prerequisite.
- `services/control/internal/operations/databaseaccess/postgres_catalog.go` already
  appends only model_families and model_family_members for Control in the ARC
  prerequisite. No broader gateway grant is included. ARC retains original-role
  OID/authenticator preservation, grant resume and exact admission ownership.
- IAM owns real authenticated route registration, DEV owns public schemas, and
  Web owns S-202. Synthetic service actors do not prove those flows.

The seven owner SQL roots and the cursor root are listed in manifest.json.
Following ARC registration and an explicitly allocated joint native window, the
intended owner command is (NOT RUN):

```sh
go -C services/control test -mod=readonly -p=1 -tags repository_integration -run '^(TestActualModelFamily.*|TestModelHistoryCursorIntegrityAndScope)$' -count=1 -timeout=15m -v ./internal/modules/models
```

Provide the existing protected PROJECT_ONE_TEST_DSN_FILE and
PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE references to the same disposable
loopback PostgreSQL `_test` instance; set the existing repository engine to
postgresql and exact owned repository schema. Preserve the URI transport policy,
original setup budgets, and original protected setup-policy input when used.
Derived registration requires PROJECT_ONE_REPOSITORY_MODEL_FILES_DIRECTORY
pointing to the approved pinned non-weight files. Never put credential values in
commands or output. Each fixture owns a fresh schema, generated private role and
protected temporary configuration. The fixture logs only its random role name for
original-owner cleanup custody; it never logs the credential or URI.

Mandatory ARC roots include the six family roots in manifest.json, the original
catalog logical-restore/drift, every-prefix lineage/foreign-ledger, and original
restricted-role grant-upgrade/resume/denial controls. These are joint entries, not
replaced by the seven domain roots. Positive formal tests deliberately remain
blocked by the current schema-60 catalog pins.

## Evidence boundary

Only formatting, whitespace, layout and roadmap consistency were checked here.
The old complete gate belongs to exact source 2783a46fa8990daa0792f11ee764c46a7795ef36,
tree fd479f8e54563061274ec898923d9b16dcdccc0a. Published dba2eaf6c2a8e90aa2a73634dfc576ddc91c6521
has a separately retained documentation-only equivalence proof. Neither is a
formal-61 result. Both earlier full-gate failures remain preserved. No new service,
container, database, runtime principal, lock or heavy process was started here.
