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
separate from compilation or SQL qualification. The joint source's original
packages now compile as recorded below; actual SQL qualification has not run.

The database tests exercise nonempty backfill, unchanged version rows and UUIDs,
repeated migration, populated downgrade refusal, exact empty rollback, mismatched
site/cell references and an inconsistent predecessor's unchanged catalog/ledger
after repeated refusal. The role test consumes the actual predecessor role helper,
publishes a version through the restricted principal, verifies local scope,
refuses immutable mutations and checks that an orphan cannot commit. The original
restore matrix now explicitly includes predecessor 60 as well as 55 through 59
and the current lineage. These are prepared assertions, not run results.

Qualification requires an explicitly allocated carrier and the MODEL owner's
adapted formal-lineage source. Start no queue or fixture during another shared
gate. The current immutable ARC input is 0f47a66bcb84c585b00bb1a92bf30f455017c1c9,
tree d45ba1d14f18f0e0298c8a6d4c5106447d0a351b. Its prepared archive contains
all 5,089 tracked paths with exact file/symlink bytes, modes and per-file hashes;
8,964,003 compressed bytes, SHA-256
3397ea71ef5458307eceeab12ccc53f12a6547ca6c07c23afb2ef8195cf7c579.
The source-manifest SHA-256 is
3d711332ecacb1fa52bd573ef50e4d48b90e8bafa5acd901b0a090cffd06988a.
This is an unqualified ARC input, not an invented combined source. MODEL's
adaptation and the final measured catalog-pin source remain unset.
The external preparation contains source-manifest.json, migration-inputs.json,
qualification-plan.json and sixteen plain SQL inputs. Their structural/hash
checks ran locally; no SQL parser, compiler, Docker create/start, carrier or
background queue ran. No input package has been sent to the test server.

## Metadata derivation followed by one admitted source

The prepared metadata inputs contain the exact unchanged CatalogSQL, immutable
prefixes 55 through 61, their original full-file migration checksums, and each
remaining lineage through 61. They are inputs to the existing psql/pg_dump/
pg_restore flow, not a new runner or a replacement test. SQL wrappers select only
a generated owned schema and wrap the original migration bodies and journal rows
in a transaction. They collect metadata only; they do not activate runtime grants
or replace production Migrate.

Use one owned PostgreSQL postmaster for the entire opportunity. Collect the full
CatalogSQL JSON and its SHA-256 for fresh 61 and each restore at 55 through 61.
For each restored prefix, replay only the immutable remaining lineage and exact
journal checksums, then collect the final 61 catalog and full ledger. Restoring
at 61 executes no new DDL. Preserve each original catalog, dump, restored catalog,
ledger and error separately. Hash exactly the CatalogSQL text result, excluding
only psql's output newline; do not normalize definitions or hide a drift. Verify
122 logical tables and all 61 original checksums. The current schema-60 hashes
remain strict until actual metadata has been collected.

After the MODEL adaptation is available, assemble and commit one complete input A
before admission; freeze its whole-source manifest and original test roots.
Derive catalog fingerprints from A. Within the same bounded opportunity, register
only the observed fingerprints in catalog.go and create source B. Retain A,
its raw metadata and the exact A-to-B patch: only measured catalog constants and
their explanatory comment may differ. Capture B's commit/tree, byte/mode manifest
and compiled binary hashes before any positive acceptance. All database, role and
MODEL acceptance must run from that same B against the same postmaster. Re-derive
metadata from B's unchanged CatalogSQL/DDL and require the identical results.
A changed domain body, migration, test, grant or fixture invalidates this sequence;
stop and prepare a new source instead of silently mixing candidates. No passing
Migrate result is claimed for A, and no guessed B identifier or hash is prepared.

## Existing restore fixture and resource binding

Keep TestCatalogAdmissionAfterLogicalRestore and its original owned-fixture
checks. identityfixture.OwnedPostgres verifies actual Docker labels, cluster ID,
server version and an explicit 127.0.0.1 published port. The old network-none
prototype alone cannot satisfy that entry. Reuse its existing M12 Docker fixture
contract: projectone.owner=mvp-development, projectone.milestone=M12, one generated
fixture UUID and the matching knowledge-fixture- cluster name. Choose a free
explicit loopback port once; verify the real HostConfig port binding equals the
protected test input. Never fabricate inspector output, use a restore overlay,
label a Linux toolchain as Darwin, or disable that original preflight.

The proposed fixture uses one owned internal Docker bridge and only that
loopback publication. Docker documents [internal isolation and host access](https://docs.docker.com/reference/cli/docker/network/create/#network-internal-mode---internal)
and [bridge port publication](https://docs.docker.com/engine/network/drivers/bridge/).
This combination is an inference from those contracts: actual loopback reachability,
network membership, external isolation and the original fixture preflight must
be verified in the fixture phase before any schema workload. Its presence in a
plan is not a network-none or disconnected-install acceptance result. No public
port or additional database is allocated outside the owned test cluster.

Pin the cached Linux amd64 image by its immutable local configuration ID,
sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47,
with --pull=never. This ID was retained by the earlier ARC prototype; it is not
presented as a repository manifest digest. A current read-only inspection could
not complete because SSH timed out during banner exchange, exit255. No image,
lock or native workload was created. At admission require an actual cached-image
ID/platform match, then SQL server_version_num=180006 and vector0.8.7. Missing or
different inputs refuse; do not pull an image or silently change the pin.

The fixture is capped at two CPUs and 4 GiB. Serial Go uses the existing pinned
1.27.3 toolchain and offline cache, GOMAXPROCS=2, GOMEMLIMIT=512MiB,
GOTOOLCHAIN=local, GOPROXY=off, GOSUMDB=off, CGO_ENABLED=0 and -mod=readonly -p1.
Use secret-file references only. Keep the existing nonblocking, inode-checked
workstation/Linux carrier rules, both Linux heavy locks, free memory>=2GiB,
available memory>=8GiB, disk>=30GiB, full I/O PSI avg10<=5 and foreign-job checks.
Stricter current carrier rules take precedence. Admission itself does not grant
a new opportunity while TRN or DEV owns the shared window.

## One absolute 20-minute deadline

The earlier 15-minute and 10-minute examples were not additive allowances.
Replace them with one absolute monotonic deadline at T0+1,200 seconds, starting
before the first carrier/admission attempt. The same controller retains that
original deadline across every child, failure and cleanup; none may reset it.
Each phase below includes its process termination/join overhead. An early finish
may advance the next phase, but does not enlarge any phase cap or absolute end.

| Phase | Maximum seconds | Absolute end from T0 |
| --- | ---: | ---: |
| Source/binding and nonblocking resource admission | 45 | 45 |
| One fixture, readiness, real ownership/version/isolation checks | 60 | 105 |
| All eight source-A metadata histories | 180 | 285 |
| Observed catalog registration and freeze source B | 60 | 345 |
| Serial compile of database, databaseaccess and MODEL test binaries | 240 | 585 |
| Original database admission/restore/lineage/drift and family roots | 240 | 825 |
| Original Control runtime-role upgrade/refusal roots | 150 | 975 |
| MODEL original adapted family/history/retirement/scope roots | 105 | 1,080 |
| Exact owned-child/container/volume/network cleanup and readback | 120 | 1,200 |

Collect A's eight histories in the metadata phase. B-equivalence readback runs
inside the source-freeze phase after constants are registered, from the same
unchanged catalog/DDL and already collected databases; it has no extra time slot.
After T0+1,080 start only cleanup. Within each cap, leave 15 seconds for TERM,
KILL if needed and confirmed child-group reaping; do not add those seconds after
its deadline. Readiness may observe pg_isready1/2 only within its original bounded
window, and must reach0 before SQL; status3/other errors refuse. No native body,
failed test or consumed opportunity is retried automatically.

A cap is a stop bound, not evidence that unmeasured work fits. A phase failure or
unfinished root makes this opportunity incomplete and prevents later acceptance
phases. Keep every required case, error and refusal; do not trim the restore
matrix, lower fixture scope or weaken an assertion to fit the allocation.
Full check/build/test, affected race and installed/offline behavior require
separate allocated gates; none is implied by this focused opportunity.

Compile only from frozen B, once per package, without a test overlay:

```sh
go -C services/control test -mod=readonly -p 1 -c -o "$RUN/database.test" ./internal/platform/database
go -C services/control test -mod=readonly -p 1 -c -o "$RUN/databaseaccess.test" ./internal/operations/databaseaccess
go -C services/control test -mod=readonly -p 1 -c -tags repository_integration -o "$RUN/models.test" ./internal/modules/models
```

Execute the resulting binaries from their original package directories, with
the existing protected DSN/Docker/repository fixture bindings on that one cluster.
The database root selector remains
`^(TestModelFamily.*|TestEveryCellOwnedTableCarriesScope|TestCatalogAdmissionAfterLogicalRestore|TestLineageUpgradeFromEveryPrefixMatchesFreshCatalog|TestM02ActualUpgradeFailureRecoveryAndChecksums)$`.
The role selector remains
`^(TestCatalogMatchesImmutableMigrations|TestModelFamilyRuntimeGrantUpgradeRetainsPublicationAndDenial|TestUpgrade.*|TestFreshRuntimeGrants)$`.
Use -test.count=1 and -test.v; the body timeouts are respectively225s/135s/90s,
inside their phase caps. MODEL must bind its actual adapted roots and protected
restricted-principal input before admission; the current five original roots are
TestActualModelFamilyCandidateBackfillAndLifecycle,
TestActualModelFamilyHistoryPagesAndAuthority,
TestActualModelFamilySelectionTransactions,
TestActualModelFamilyRetainedDerivedGraph and
TestActualModelFamilyDerivedRegistration. Preserve their actual cases and include
any additional formal-lineage roots supplied by the original owner. Root names
alone are not a completed fixture contract. Missing bindings refuse before launch.

Cleanup has no independent or renewable allowance. Bound child-group TERM/KILL
and join to15s, log/metadata capture to20s, exact container stop/removal to25s,
owned volume/network removal to20s, absence readback to20s and private credential
removal/final record/carrier release to20s, clipped to the absolute remaining
120 seconds. Verify IDs and ownership before each destructive step. Never prune,
remove a foreign resource or destroy retained failure evidence. If any operation
or absence cannot be confirmed by the global deadline, stop with that exact
resource marked unconfirmed and do not report the carrier as cleanly released.
No unbounded finally, wait, cleanup retry or background watcher is permitted.

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

## Exact original-owner joint input and transport refusal

The clean assembled input is e321fdcb84fd20586c503cbdf3ae179eea1c6c10,
tree efa43828c7febdadd72d03b70c30a28aefd800ef. It merges original MODEL
db8435034a2a94a3285a059f6bd40237322ae1c2 and original TRN
e639db9035b0b2c0229c90175493c605f328e295 onto the ARC input through baa584c3.
All13 MODEL module paths match its original author, and all21 TRN module paths
match its original author. The original formal61 SQL and schema60 catalog pins
remain byte-identical to0f47a66b. The later dirty request/RPC join increment is
not included in this input. MODEL's seven original TestActualModelFamily roots
and TestModelHistoryCursorIntegrityAndScope remain required, with its exact
protected PostgreSQL/schema and pinned approved non-weight file inputs.

The regenerated full-source manifest covers5,096 paths. Its archive is8,987,025
bytes, SHA-256 bb96b822445bc540e8b0853b051104b2d64f05e1cae8f2dee7fe82c698fdeee1;
manifest SHA-256 ca272a2f9f5902aee32b8b4ce0e6900ad18f9253007f42e9995a4137f912752d.
This replaces no earlier source or evidence; it is the actual new unqualified
input A. There is no observed schema61 fingerprint or admitted input B.

The one granted Linux opportunity stopped before entry. The required read-only
image/resource SSH inspection returned255 with banner-exchange timeout. An earlier
read-only command connected, but its clipped process-list output cannot establish
usable final resource/image admission. No retry followed the transport refusal.
No permanent carrier was opened, no source was uploaded, no compiler/test group
or SQL statement ran, and no container, volume, namespace or credential was
created. The result is NOTRUN, not a failed SQL assertion or a passed preflight.
The local native-notrun.json retains the exact source and refusal classification;
cleanup is not applicable rather than an invented successful cleanup result.
The original1,200-second ceiling and every assertion remain unchanged.

## Joint-source compilation and renewed native refusal

Joint source `3513ab2ba5885bb5d782919f7611d96e35d28fc6`, tree
`c65b8e3fde9833369c253c8303b70c3f13cae5d8`, retains the exact formal61
candidate, original MODEL/TRN/IAM bytes and original author DAGs. Its three
affected local packages passed all 11 explicit race roots and three IAM cleanup
subcases, without skips or failures; see the
[gateway lifecycle qualification](agent-gateway-database-role.md#joint-source-local-lifecycle-qualification).
These are no-database component tests, not formal migration or role execution.

The same bounded Mac opportunity serially compiled the original Linux amd64
database, databaseaccess and repository-tagged MODEL test packages, with no
overlay or assertion change. The binary SHA-256 values are:

| Original package | SHA-256 |
| --- | --- |
| internal/platform/database | 2bc2623083210a5265dd215647f4762a9959ada8a3d1be13f102820e05da0124 |
| internal/operations/databaseaccess | 293ab7afe7db552a9c450634394a013fc5bbc856ff19915c199a12707b3a7be5 |
| internal/modules/models (repository_integration) | 6ff49492f7a535dd1a1d67880aa3d6b330ac9538eb6a2376456ba4d90dc80947 |

The new original native opportunity then stopped at its single SSH/resource
preflight: exit255, 8.616071125 seconds, no resource JSON. No native carrier was
opened, input uploaded, SQL executed or database/container/volume/credential
created. Its exact result SHA-256 is
`167ce561f41006e262a06eb0de132a0f5c1fd0a2314f1c91f4b4c71988175b31`.
It is a distinct NOTRUN for the joint source, preserving the earlier e321fdc
refusal. No source B or schema61 fingerprints were invented. Actual fresh61,
prefix55..61 restore, drift/ledger, original grant-resume and MODEL's seven SQL
roots plus cursor dimensions still require the original native environment,
protected shared fixture inputs and a newly allocated opportunity. The strict
schema60 directory fingerprints remain in force.

The independently renewed original-source opportunity likewise returned NOTRUN:
one SSH child, exit 255 after 19.858641375 seconds, no remote resource/preflight
JSON or entry receipt. The original source archive/manifest, 61 migration assets,
16 metadata inputs and three native binary hashes were verified before entry.
No actual catalog JSON or fingerprint was collected, and no pin/source B patch
can be derived from this refusal. The matching exact result SHA-256 is
`fed248a4ece4bc3ef9c8fc698d51a6d38088c8744f3f2bad16288d06ebd38d1a`;
see the [gateway qualification boundary](agent-gateway-database-role.md#joint-source-local-lifecycle-qualification)
for the scoped process cleanup and preserved earlier evidence. A diagnostic
connection's success does not qualify a later native entry. The original
schema60 pins and every migration/role assertion remain unchanged.

## Offline native-entry selector contract

The original frozen qualification plan's MODEL selector
`^TestActualModelFamily` selected the seven SQL roots but omitted
`TestModelHistoryCursorIntegrityAndScope`, already required by the owner handoff
above. An offline source audit reproduced that omission and froze the corrected
selector `^(TestActualModelFamily.*|TestModelHistoryCursorIntegrityAndScope)$`.
All eight roots have exactly one definition in the unchanged original MODEL
source; the repository_integration profile includes both the tagged SQL roots
and the untagged cursor root. Its eleven original dimensions are account,
session, epoch, audience, tenant, workspace, site, cell, family, anchor and filter.
The corrected selector retains every original selected SQL root. No owner
implementation, assertion, conditional skip or fixture was changed.

The frozen delta is
`lane-arc-diagnostics/arc111-ssh-offline.9vgaoj6h/model-profile-correction.json`,
SHA-256 `c8e5f88a425546a7eb37c7906744c234f4fba39fc00cfac8b428b57a677c532d`.
Static checks prove the seven-to-eight selection change, unique definitions,
eleven dimension names and original file hashes. They execute zero Go/SQL cases
and are not a binary test-list or cursor qualification. The original plan and
three compiled binary hashes remain preserved; the correction is not activated.
The matching binary list and every root/subcase still need actual execution
after authorized connectivity/resource admission and a matching admitted source.

## Observed entry without catalog execution

The later observed source 3513ab2b entry completed proxy/banner/KEX/authentication,
fresh resource checks and both exact NB carriers, but lost transport after
entry. Cleanup-only readback recovered PAYLOAD_REFUSAL before source extraction
and fixture creation; image inspection alone ran. This is an entered, failed
input-admission opportunity, not an unconfirmed connection or a passed SQL run.
No database, catalog JSON, fingerprint, restricted-role case, binary root or
source B was produced. The original schema60 pins and all original owner bytes
remain unchanged. See the
[actual trace and scoped cleanup](agent-gateway-database-role.md#observed-remote-entry-and-source-admission-failure)
and [bounded input correction](agent-gateway-database-role.md#bounded-input-reception-and-entry-aware-failure-classification).
The private corrected receiver and expanded MODEL selector still require a new
authorized transfer/run; their local checks do not qualify the seven SQL roots,
cursor dimensions, migrations, restore paths or grants.

The next allocated opportunity measured complete delivery of a smaller exact
frame: 720,340 bytes, SHA-256 and EOF matched in 2.256576253 seconds remotely.
Its decoded member hashes matched, but the unchanged strict source verification
then refused a cached file with `SOURCE_MODE_MISMATCH`. The actual path and mode
were not recorded. No complete target-source admission, PostgreSQL fixture,
catalog JSON/SHA, restore, native binary or role case was reached. Both original
locks closed, owned groups joined and credentials remained absent; SSH and outer
both returned 1 in 10.179709125 seconds without a retry. The three binary hashes,
expanded eight-root MODEL selector, formal61 SQL and schema60 pins remain fixed.
See the [actual frame receipt and cache boundary](agent-gateway-database-role.md#observed-complete-frame-and-strict-cache-mode-refusal).
This verifies only that specific frame's transport and integrity, not any of
the outstanding database or reader capabilities.

The follow-up read-only diagnostic proved the cache rejection was the exact
`.editorconfig` tar mode 0664 versus Git `100644`/0644, with matching content,
regular type, UID/GID0 and a single link. A corrected private conversion passed
all actual local 5,015 basis and 5,102 target files while preserving strict source
checks. No native run followed: activation was refused after the original work
cutoff, with zero new native connections, carriers, PG or SQL cases. Final local
process absence was confirmed after the original 1,200-second window and is not
reported as an in-window confirmation. The new exact guard/input binding and
remaining fresh allocation, actual61 catalogs, matching pin rebuild, original
role tests and protected MODEL inputs are recorded in the
[cache conversion boundary](agent-gateway-database-role.md#diagnosed-git-archive-mode-and-private-cache-conversion).

The separately allocated next native attempt did start. Fresh original dual NB
and complete wire/hash/EOF checks passed, then source conversion refused
`Makefile`: actual 0644 versus the private uniform-tar policy 0664, with identical
5,555-byte SHA-256. The actual cache therefore mixes canonical and archive
permissions. SSH and outer returned 1 at 9.662645250 seconds; no fixture, catalog,
SQL root, restricted-role case or source B was produced. Owned FD/group cleanup
was confirmed at allocation65.780720 seconds. A new unactivated per-file input
representation correction passed 24 affected cases and preserves exact target
0644/0755 and original verification functions. See the
[actual mixed-cache boundary](agent-gateway-database-role.md#actual-mixed-cache-modes-and-one-failed-native-admission).
All real61 fresh/restore fingerprints and matching pin/binary qualification
remain outstanding; the unchanged old binaries are not evidence for new pins.
