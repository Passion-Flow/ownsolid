# Isolated identity database fixtures

fixture.go builds synthetic account state in owned random schemas; input.go reads
the protected test DSN and licensing.go supplies explicitly synthetic License
fixtures. These packages are imported by tests, never the production command.
The test supervisor must bind toolchains/cache/TMPDIR to its admitted private
storage, authorize only synthetic scope and retain actual source/raw/cleanup.

`NewWithSetup` and `NewM16WithSetup` accept test-only `SetupOptions` for an
explicitly slower creation/migration phase. Zero options and all historical
constructors keep the original 5s statement/3s lock budgets with no new total
deadline. Nonzero options specify `TimeoutSeconds` (30..900) and
`StatementTimeoutSeconds` (5..300), with total greater than statement plus the
unchanged 3s lock budget. A separate temporary setup pool owns those settings and
closes every connection before business owners are returned. The returned pool,
cleanup and product policy retain their original budgets; no ambient setup
environment switch is introduced. `TestM16ExplicitSetupReturnsCanonicalBusinessPool`
checks real account22 migration, joined setup consumers, actual 5s business timeout
and usable authentication afterward under an isolated admitted PostgreSQL fixture.

`NewRecoverableWithSetup` is the failure-custody counterpart of `NewWithSetup`.
They share the same private selector and existing explicit application-profile
validation. With no selected profile, both retain the complete platform catalog
(currently 108 tables and 27 original migration entries); a selected supported
profile is never silently widened or reduced. A caller using `New`/`NewWithSetup`
must use this counterpart to preserve its catalog rather than switching to M16.
Two additional full-catalog failure/success leaves pass on actual isolated
PostgreSQL18.6/vector0.8.7 with one raced Linux binary (15.448s/8.258s). Both assert
108 tables, all 27 original migration checksums, actual 5s statement/3s lock
settings and zero setup consumers. The failed child retains its original metadata
until the independent parent reads exactly one audit and matching unknown intent;
only then is its disposable schema removed. The existing four M16 leaf receipts
remain separate unchanged-source evidence. These two leaves do not establish an
M22 network/provider/browser repair. The earlier source-preparation refusal of two
contained tracked symlinks remains failed before Go; corrected archive validation
and a fresh compile are separately recorded.

`NewM16RecoverableWithSetup` is the explicit failure-custody variant. At cleanup,
an already-failed test retains its exact newly created SQL schema and metadata,
and closes and joins both runtime and operator pools. It logs only the nonsecret
schema identity. Successful tests and every existing constructor keep their
original table/schema disposal behavior. Setup options, returned 5s statement/3s
lock budgets and product policy are unchanged. Stop and join all consumers before
fixture cleanup. This opt-in preserves SQL only: temporary file/keyring/store
custody and any failed native effects remain their existing owners' obligations.
It cannot recover a schema deleted by an earlier default fixture invocation.

The separately tagged `TestActualIdentityFixtureFailureCustody` executes an
intentional failed child and independent post-exit SQL readback, plus recoverable
success and default success/failure controls. Only its explicitly disposable
synthetic audit/intent schema is removed after a successful independent readback;
a failed parent retains it. This metadata regression does not qualify object
storage, a provider, browser serving or a complete backup/restore.

The isolated Linux metadata profile passes all four separate PostgreSQL18.6 /
vector0.8.7 leaves with one raced binary, ext4 runtime storage and no inherited
runtime GOTMPDIR. Three existing setup unit roots also pass. The initial fixture
bootstrap/port-admission failure remains a separate failed attempt; it ran no Go
metadata case. These results establish fixture custody only, not an M22 upload or
browser repair.

postgres.go retains the historical Docker-owned PostgreSQL18.6/vector0.8.7
preflight/export/fresh-restore variant. It checks the exact M12 container labels,
loopback port and cluster identity before effects. No shared Engine wake is
authorized by importing a fixture or reading this guide.

native_postgres.go adds the explicit OwnedNativePostgres input for the
[native account recovery profile](../../../../../docs/engineering/deployment-transport/native-account-database-recovery.md).
It compares protected measured tools/references and actual TLS/pool/system/schema
identity. The native dump/fresh-restore path preserves the exact source and
predeclared new database; it has no arbitrary command or customer connection
input. RestartNative writes one private request and waits for a nonce/scope-bound
ack from the Python owner; Go never signals a client-selected postmaster PID.
The existing default Docker entrance remains separate.

native_postgres_test.go uses explicit non-executed tool/CA/credential file doubles
for metadata/namespace/mode/link/tamper/argument/environment refusals. Passing
those controls does not establish actual native SQL/restart/backup/restore. The
integrated native profile and exact cleanup remain required. Do not run these
fixtures against customer production databases or expose their private state.

The actual restore profile registers RegisterNativeSourceCleanup before export.
It inventories only the already attested source schema and drops each synthetic
table in its own autocommit transaction before the original empty-schema cleanup.
This bounds relation-lock demand on the admitted small fixture without changing
its immutable PostgreSQL settings. Identifiers are quoted, the table inventory is
bounded and failures remain test errors. The current profile actually disposes
108tables. This is test fixture disposal, not a customer migration or backup
cleanup API; production operator authority remains in its existing owner.

cleanup_postgres.go shares that bounded per-table disposal with the canonical
New/NewM01 fixtures. It accepts only their generated m01_ or attested
m20_repository_ namespace and exact loopback _test pool context. The constructor
still creates a fresh schema before use; cleanup now reports errors, closes its
pools and drops the empty exact schema instead of silently ignoring a failed
large DROP SCHEMA. The current canonical [M01 SQL contract](../../../../../docs/engineering/deployment-transport/current-m01-account-gates.md)
selects one announced schema per root/subtest; it does not acquire customer cleanup
authority or requalify historical receipts.

`PROJECT_ONE_TEST_NATIVE_POSTGRES_INPUT` explicitly binds identity-based training
recovery to the same strict measured native document. Before creating its declared
synthetic source schema, it verifies actual TLS/endpoint/system/data identity and
namespace absence. It rechecks the exact created schema afterward. Existing schema
adoption, arbitrary commands/DSNs and the legacy Docker fallback remain refused
when this explicit input is present. No original Docker fixture needs startup.

`RestoreFresh` checks the attested source for knowledge_chunks even on cumulative
account catalogs, and installs the approved vector prerequisite only in its new
disposable restore database before vector columns import. It revokes PUBLIC CREATE
there. Existing account-only restores retain no-vector behavior; no source database
or retained extension is modified by this helper.
