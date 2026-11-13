# Model family schema admission

ARC integration of the MODEL-owned MOD-103 domain, reviewed 2026-11-13.
The current published platform lineage ends at 60 with 120 logical tables.
The next candidate is 61 with 122 tables. Recheck numbering against main before
publication; no existing migration or domain SQL may be rewritten.

## Original owner input

The admitted domain source is the candidate carried by MODEL's published
dba2eaf6 integration and qualified domain input 2783a46f, as described in
[the domain contract](training-dataset-lineage.md#durable-family-domain-contract).
The original files are:

- models/testdata/model_family_candidate.up.sql: SHA-256
  `03f8c26e190bc16e01a9905c45cb13be1eef8835056d29e86e761ff70ae2c49c`.
- models/testdata/model_family_candidate.down.sql: SHA-256
  `0dc0c6c0c2821a0eead8399db277ab5de2538bf4b2b6ff6b3b8b73dcd8159cb6`.

Copy the upward SQL body unchanged, replacing only its unnumbered header with
the formal migration header. Preserve the downward SQL unchanged after a formal
header. The two tables, three trigger functions, deferred scoped foreign keys,
advisory lock and immutable-family behavior remain MODEL-owned. No inferred
grouping by name, digest, model key, source job or runtime revision is allowed.

## Platform cell ownership

The unnumbered candidate intentionally has no platform site/cell columns. The
existing TestEveryCellOwnedTableCarriesScope requires both non-null local-default
columns on every cell-owned table; model history is not global identity data.
Adding these tables to its global allowlist would hide a real integration gap.
Preserve that assertion and append a separate platform section after the original
upward SQL, without changing its tables' domain keys, functions or trigger bodies.

Use the existing local_site_id()/local_cell_id() defaults on both new tables.
Add composite candidate keys and foreign keys so a family's initial version,
each member's version and that member's family also agree on site and cell.
The version links retain deferred checking for publication's existing cyclic
transaction; the family link remains immediate. PostgreSQL's documented
composite foreign keys and the existing migration59 scoped-key pattern provide
the approach. Append removal of the added model_versions key after the unchanged
downward SQL. Its original nonempty refusal still runs first.

Default backfill is valid for the currently admitted local-cell database. An
inconsistent predecessor with nonlocal version rows must refuse migration and
roll back rather than silently relabel retained versions. Tests must compare
the original version rows, check all three retained scope projections, refuse
foreign site/cell references and preserve the strict cell inventory test.
This is platform schema integration; MODEL's standalone candidate is unchanged.

## Approach and references

[PostgreSQL constraints](https://www.postgresql.org/docs/18/ddl-constraints.html)
and [SET CONSTRAINTS](https://www.postgresql.org/docs/18/sql-set-constraints.html)
support the owner's cyclic deferred membership and named backfill validation.
Keep that ordering. Formal migration uses the existing transaction, lineage
checksum and final exact-catalog check. A failed or unknown commit must not
publish a success projection. The owner's populated downgrade refusal remains;
rollback of an adopted installation requires its matched retained backup rather
than dropping family UUIDs.

Register only model_families and model_family_members in the full PostgreSQL
Control runtime's existing closed table catalog. Preserve its existing DML-only
contract and the domain's mutation-refusing triggers. Do not add schema-wide
grants, ownership, grant options, trigger/DDL/TRUNCATE rights or gateway access.
[PostgreSQL GRANT](https://www.postgresql.org/docs/18/sql-grant.html) distinguishes
those rights. Other database engines and cumulative account catalogs are unchanged.
The future restricted Agent gateway role does not acquire human history access.

The current catalog hashes describe schema 60. Do not invent schema-61 hashes,
accept an arbitrary catalog, remove the strict comparison or alter CatalogSQL
to hide the new objects. A local source candidate can be prepared while admission
continues to fail closed; it is not publishable until the real fingerprints and
full migration/role qualification below pass. Native and whole-repository work
requires the shared carrier/resource allocation; no background queue is started.

## Rejected alternatives

Applying testdata at process startup bypasses the immutable lineage. A second
family implementation would fork MODEL authority. Silently skipping membership
or accepting the predecessor schema would admit versions without their required
identity. Reusing a schema-60 catalog digest for schema 61, disabling catalog
checks or deleting retained family state to downgrade are all rejected.

## Failure coverage and completion

Prepare actual PostgreSQL tests for nonempty 60-to-61 backfill, default publication
under the original restricted Control principal, retry without changed family IDs,
unchanged legacy version references, refused orphan membership and nonempty
downgrade rollback. Exercise schema-committed/grants-pending recovery with the
same role OID/authenticator, journal SELECT-only and DDL/trigger/TRUNCATE denial.
Use the existing restore matrix and explicitly retain restore at predecessor 60;
derive all distinct fresh/restored/upgraded catalog fingerprints from the actual
pinned PostgreSQL 18.6 metadata before registering them. Catalog drift, ledger
tampering and unknown commit behavior remain strict failures.

MODEL's candidate tests currently apply their testdata to a current fixture.
Once 61 is active, that would apply the same DDL twice. MODEL must move candidate
backfill cases onto an explicit predecessor-60 fixture and run normal history
cases on the admitted lineage; ARC does not edit those module tests. DEV/IAM
still own history's actual public contract/dispatcher; this migration does not
claim that route or S-202.

No dependency, model or dataset is added. The existing Go standard library and
Google UUID use BSD-3-Clause, pgx uses MIT and PostgreSQL uses the
[PostgreSQL License](https://www.postgresql.org/about/licence/). Commercial use,
offline execution and redistribution with their required notices are permitted.
Synthetic fixture records establish SQL behavior, not hardware, installed
delivery or ARC-111 completion.

## Prepared integration and qualification boundary

The source candidate contains the numbered up/down pair, schema version 61,
122 logical tables including the migration journal, and exactly 120 tables in
the closed Control DML list. The audit head and journal receive no new DML.
Static byte comparison retains all 72 predecessor migration assets, the original
CatalogSQL/fingerprints, cell inventory assertion and grant-resume helper.
The owner's upward body and entire downward input remain exact prefixes before
the explicit platform section. Source formatting, layout and diff checks are
separate from compilation or SQL qualification; neither has run for this candidate.

The database tests exercise nonempty backfill, unchanged version rows and UUIDs,
repeated migration, populated downgrade refusal, exact empty rollback, mismatched
site/cell references and an inconsistent predecessor's unchanged catalog/ledger
after repeated refusal. The role test consumes the actual predecessor role helper,
publishes a version through the restricted principal, verifies local scope,
refuses immutable mutations and checks that an orphan cannot commit. The original
restore matrix now explicitly includes predecessor 60 as well as 55 through 59
and the current lineage. These are prepared assertions, not run results.

Qualification requires an explicitly allocated carrier; start no queue or new
fixture during the shared gate. Use the exact candidate commit and a source-byte
manifest. The first bounded allocation is one pinned PostgreSQL 18.6/pgvector
0.8.7 owned network-isolated fixture, at most two CPUs and 4 GiB for the native
fixture, with serial compilation on two CPUs and GOMEMLIMIT=512MiB. Budget at
most 20 minutes for metadata collection and focused SQL; a whole-repository gate
is a separate allocation. Recheck the workstation gate and both Linux heavy locks,
disk, memory, PSI, existing jobs and listening ports before any allocation. Keep
the existing stricter preflights; no resource gate or assertion is relaxed.

Collect actual CatalogSQL JSON and SHA-256 for fresh 61 and every restore at
55 through 61 followed by the immutable remaining lineage. An external metadata
collector may use the existing explicit-version fixture and original embedded
SQL/checksums to collect those shapes before admission. This is fingerprint
derivation only: it must not bypass or replace Migrate in acceptance tests.
Register only the actually observed fingerprints, then run the original positive
admission, restore/drift/ledger and runtime grant checks unchanged. The focused
entries, with protected fixture references supplied by that allocation, are:

```sh
GOPROXY=off GOSUMDB=off GOMAXPROCS=2 GOMEMLIMIT=512MiB \
  go -C services/control test -mod=readonly -p 1 -count=1 -timeout=15m \
  ./internal/platform/database -run '^(TestModelFamily.*|TestEveryCellOwnedTableCarriesScope|TestCatalogAdmissionAfterLogicalRestore|TestLineageUpgradeFromEveryPrefixMatchesFreshCatalog|TestM02ActualUpgradeFailureRecoveryAndChecksums)$'
GOPROXY=off GOSUMDB=off GOMAXPROCS=2 GOMEMLIMIT=512MiB \
  go -C services/control test -mod=readonly -p 1 -count=1 -timeout=10m \
  ./internal/operations/databaseaccess -run '^(TestCatalogMatchesImmutableMigrations|TestModelFamilyRuntimeGrantUpgradeRetainsPublicationAndDenial|TestUpgrade.*|TestFreshRuntimeGrants)$'
```

Every selected native test must actually run without a fixture skip. Full
check/build/test, MODEL's adapted formal-lineage cases, offline native serving and
retained/public history are further prerequisites for publication or ticking an
item. Existing agent v1 frames and version negotiation are unchanged. The database
upgrade requires matching control and gateway binaries: an old exact-schema
binary must fence schema 61 rather than claim mixed-database-version support.

The separate gateway reader dependency remains outside this migration. TRN's
5b73fa05 exposes read-only PublishedReader owners, but the current published
NewAgentGateway constructor has no reader binding input. Its construction owner
must provide an additive binding entry that keeps the existing Control writer
path and validates the same service graph. The process consumer then opens only
the configured existing namespaces for the installed tenant and joins admitted
requests, session drain and workers before closing reader descriptors. Do not
type-assert Nodes.Training to overwrite another owner's internal services, create
a second writer, reflect into fields or add a constructor that returns success
without binding the real readers. Missing candidate objects or an unavailable
joint source is not permission to guess that interface. The reproduced dataset
storage refusal remains unresolved until the actual composed claim passes.
