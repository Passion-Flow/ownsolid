# Real isolated repository fixture

`New` and `NewUnbootstrapped` own real selected PostgreSQL/MySQL fixture setup.
The selected server and private connection inputs remain the original owners;
each business case retains its own fresh database/schema, tenant and authority.
The helper does not pool seeded schemas across cases or copy live database data.

With `PROJECT_ONE_TEST_REPOSITORY_SETUP_FILE` unset, original setup is unchanged:
30seconds outer context, MySQL lock1/DDL5/catalog20/lock-cleanup2seconds. Catalog
steps and metadata limits remain exact original invariants. PostgreSQL statement,
lock and schema-cleanup policies are unchanged. The setup context is separate from
caller business/cancellation/worker contexts, which remain unmodified.

The optional explicit test-only reference selects a complete protected JSON
snapshot through the existing bundle private reader (bounded pinned regular file,
same UID, single link, canonical private parent); policy JSON is at most4KiB within
that reader's1MiB outer cap. Exact keys, integer types, duplicate/null/unknown/alias
refusal and migration-owner bounds are required. `setup_seconds` is30..900 and
must include MySQL total+cleanup+lock-wait. A missing/invalid explicitly selected
file fails before connecting or allocating a fixture. There is no fallback.

See the [slow-disk setup example](../../../../../config/examples/testing/repository-setup.slow-disk.example.json),
[commented environment mapping](../../../../../config/examples/testing/repository-setup.env.example)
and [execution boundary](../../../../../docs/milestones/m20-mysql/repository-fixture-profile.md).
Test process/runner bounds must contain the explicitly selected setup; this policy
cannot extend an outer process deadline. It does not change product migration
policy, accept unknown DDL, waive lock-release confirmation or remove role checks.

Focused pure file/policy controls (no database is started):

```sh
go -C services/control test -tags=repository_integration ./internal/testing/repositoryfixture -run '^TestRepositorySetup' -count=1
```

A later actual slow-disk run is separate evidence. Keep original failures and
unknown targets; execute only outstanding/affected cases in new admitted scopes.

## Explicit native-controller failure custody

`NewRecoverableUnbootstrapped` is the explicit entrypoint for a test that may
leave native backup/restore effects requiring original controller records.
At cleanup entry, an already-failed test logs `REPOSITORY_FAILURE_PRESERVED_SCHEMA`
and retains its exact owned PostgreSQL schema, operation/audit/intent rows and
other metadata. The caller must join its workers before pools close; the outer
owner must stop and retain the disposable database plus protected connection
references. Native scopes, journals and archives remain with their original owner.
There is no automatic replay or broad environment switch. Success and both
ordinary constructors retain normal cleanup; the setup policy above is unchanged.

The Linux-only `repository_fixture_recovery_integration` regression uses actual
PostgreSQL with parent/child processes, intentional exit1 and independent reopened
row readback. Four exact cases cover recoverable failure/success and legacy
failure/success. Original child output is preserved reversibly with SHA256;
successful regression cleanup removes only its verified disposable schemas.
This creates synthetic metadata only, never an M17 effect or full native restore.

Use a newly declared v2 foreground profile: compiler-only GOTMPDIR, runtime
GOTMPDIR unset, private durable ext4 TMPDIR and actual filesystem receipts.
The exact parent root is `TestActualRepositoryFixtureFailureCustody`; the child
root is internal to that test and must not be selected as a standalone control.
Actual execution outcomes remain in the owning M26 evidence, separate from this
source contract and the original setup-policy tests.
