# Audit chain head cell scope candidate

## Baseline and order

The PostgreSQL lineage on `ea92fc4e` ends at `000057_agent_gateways`.
ARC candidate `4f01aa39` is based on `0c78eb79` and changes catalog
admission and restore tests without adding a migration. Its separate
schema 57 measurements identify `audit_chain_head` as the table failing
`TestEveryCellOwnedTableCarriesScope`. The visible Git references and
Linux worktrees had no migration at 58 when checked. Reserve
`000058_audit_chain_head_scope` as a provisional name. Recheck the
lineage and all live candidates before activation and renumber if the
slot is taken.

SCI recorded the SHA-256 digests of the exact up and down candidates.
The same bytes are now in the
[production migration directory](../../services/control/migrations/postgresql).

| Candidate | SHA-256 |
| --- | --- |
| `000058_audit_chain_head_scope.up.sql` | `c4d228212ca9a4107dc5e4a78468f9f7a35dfba818564d406a0a8861cdca1413` |
| `000058_audit_chain_head_scope.down.sql` | `f9e79fe6b7ddaa15b400e8b53283cac5b67286075112c25a96bdcdf12aef0ebc` |

The release order is Web Help gate repair, then ARC's 55-to-57 catalog
repair, then Audit after ARC accepts the fingerprints on the merged
predecessor and the gates are rerun. The accepted integration embeds the SQL only after that
sequence completes. Help repair `9d25ee7c`
is on main. All 57 published up migration checksums match the current
release lineage.

Activation moves the reviewed SQL bytes to
`services/control/migrations/postgresql` and updates `SchemaVersion` in
`services/control/internal/platform/database/database.go`, the release
version and checksum entry in `api/release/v1/postgres_application.go`
and `api/release/v1/mvp-postgresql-cpu-v1-lineage.json`, and the exact
accepted fingerprints in `services/control/internal/platform/database/catalog.go`.
The table count remains 118. Rerun the production migrator, database scope
tests and repository gates on that integrated tree before merging.

## Scope and continuity

[ADR 0006](../decisions/0006-cell-and-site-scope.md) uses non-null
`site_id` and `cell_id` defaults derived from the local registry row.
Keep the singleton head and its serialized position, hash and format
boundary. Bind its two new columns through the same defaults as events.
Use an append check against the head and current local registry inside
the existing row-lock query. Refuse an explicitly foreign event or
registry drift without touching the tail. Keep the head private to the
migration owner and the security-definer append routine.

The [PostgreSQL ALTER TABLE documentation](https://www.postgresql.org/docs/18/sql-altertable.html)
explains default and column constraints. The
[locking documentation](https://www.postgresql.org/docs/18/explicit-locking.html)
defines the row lock that serializes competing appends. Take the write
lock on the audit parent and partitions before altering the head. This
orders migration locks the same way as appends and fences older writes
while the function is replaced. Keep the V2 hash domain, field list and
canonical serialization byte-compatible. The candidate updates no event
or hash values. The reverse candidate removes only the new scope metadata
and restores the prior append routine, which can still read every V2 hash.

A preexisting V1 or V2 export keeps its original integrity coverage.
Previously captured checkpoints continue to verify their retained ranges.
The new scope admission check applies to future writes. PostgreSQL
superuser custody remains covered by the
[independent checkpoint procedure](../../tools/audit-verify/README.md#coverage-and-custody).

Use the pinned PostgreSQL 18.6 and pgvector 0.8.7 images from the existing
test environment. [PostgreSQL](https://www.postgresql.org/about/licence/)
and [pgvector](https://github.com/pgvector/pgvector/blob/v0.8.7/LICENSE)
use the PostgreSQL License. Existing [Go](https://go.dev/LICENSE) and
[pgx](https://github.com/jackc/pgx/blob/v5.11.0/LICENSE) use BSD-3-Clause
and MIT. These permit commercial and offline use and redistribution
under their notice terms. The fixture uses synthetic events and adds no
runtime dependency or dataset.

## Acceptance targets

Measure the exact catalog on an empty database and on real logical
restores at 55, 56 and 57 after applying the candidate tail. Verify the
schema 58 restore shape as well. On the same snapshots, compare the
old export and its separately captured checkpoint, check local defaults,
refuse foreign site and cell values, and verify concurrent appends from
the retained prefix. Exercise the existing scope test unchanged against
a disposable integrated candidate. A standalone DDL pass records targeted
evidence, while production admission requires the shared fingerprints,
checksum closure and repository gates.

## Disconnected PostgreSQL evidence

The Linux amd64 fixture used PostgreSQL 18.6 with pgvector 0.8.7 in
a container with no network interface beyond loopback and no published
ports. It used the released schema through 57 and the exact candidate
bytes above. All three measured predecessor catalogs matched ARC's
55-to-57 evidence at `4f01aa39` in `docs/research/postgresql-catalog-upgrade-gate.md`.
The candidate produces these exact SHA-256 values using `database.CatalogSQL`
over its UTF-8 text result.

| Path | Schema 57 fingerprint | Schema 58 fingerprint |
| --- | --- | --- |
| Empty to 58 in one transaction | No intermediate catalog | `56795c36a7a0115478e17ee9af9ea98f27c1922c201cfec0b874d8d72b713d1d` |
| Native 57 to 58 | `8454a9e3039dfb669a532aaabd26419661c404d683cd9abfa45eb4ee2b25b4a4` | `56795c36a7a0115478e17ee9af9ea98f27c1922c201cfec0b874d8d72b713d1d` |
| Restore 55, apply 56 and 57, then 58 | `4ae1c70a752af0056647e275114e2a16e8831bbcf875e6018ad3a7754b11efcc` | `9e464ca9f6708f89731980adc8def9096e05b7f11f842ee09faa7836a12d0e42` |
| Restore 56, apply 57 and 58 | `c160dfad296f6a93ec88aa88cc63bd232cbfd5dca0518c22d390961b0593e7be` | `ded94bd6eeb9c84949fd7647bd1b200e612b03fd4aedf8462280f5dacd87ce5d` |
| Restore 57, apply 58 | `c160dfad296f6a93ec88aa88cc63bd232cbfd5dca0518c22d390961b0593e7be` | `ded94bd6eeb9c84949fd7647bd1b200e612b03fd4aedf8462280f5dacd87ce5d` |
| Restore native 58 | No intermediate catalog | `ded94bd6eeb9c84949fd7647bd1b200e612b03fd4aedf8462280f5dacd87ce5d` |

The logical archives came from `pg_dump --format=custom` and were restored
to distinct empty databases with `pg_restore --exit-on-error`. The fixture
wrote one event before migration 53, one after 53 and one after 54. Before
applying the candidate it saved the two chained events and independently
captured the last hash outside PostgreSQL. The restored catalogs kept the
legacy row outside the chain and the V1/V2 export byte-identical. The
standalone verifier accepted the original checkpoint. PostgreSQL refused a
foreign cell at the insert boundary without changing the head. On the 55-to-58
and native-58 restore paths, each group of eight parallel appends produced
a verifiable suffix anchored to that checkpoint.

The [Go candidate fixtures](../../services/control/internal/modules/audit/chain_head_scope_integration_test.go)
passed against the same PostgreSQL and vector versions. They cover an
empty head with default and registered non-default scope, a write-only
role's valid local append and foreign site and cell refusals, denial of a
direct head read, registry drift, 32 parallel appends, byte-for-byte
continuity, offline verifier readback and the reverse migration. Run them
with `go -C services/control test ./internal/modules/audit -run '^TestAuditChainHeadScope' -count=1`
using an owned PostgreSQL fixture with `PROJECT_ONE_TEST_DSN_FILE`.
Set `OWNSOLID_TEST_AUDIT_VERIFY` to the absolute path of the standalone
verifier to include the checkpoint readback.

## Integrated migrator rehearsal

A disposable Go build overlay embedded the candidate at schema 58, used
the ARC catalog check with the three distinct measured digests, and
left the tracked migration and release files intact. The production
`database.Migrate` and `database.Check` passed on an empty database and
after logical restores at 55, 56, 57 and 58. Running the migrator again
also passed on every path. The original
`TestEveryCellOwnedTableCarriesScope`, `TestNewRowsTakeTheLocalCell`
and `TestCheckRefusesAnotherCellIdentity` passed unchanged in that build.
The release manifest and the catalog change on the merged predecessor
still require the shared integration gate.

The full audit module passed on the disconnected fixture with
`OWNSOLID_TEST_AUDIT_VERIFY` enabled. The `tools/audit-verify` module
passed in an isolated network namespace with only loopback enabled for
its TLS tests. `make check && make build` passed in a disconnected
namespace on the `e32e2e89` base with the candidate under testdata.
The relevant migration, audit and database scope inputs were unchanged
at `0ae98e1e`, where layout, roadmap and tooling tests passed again.

## Verification on the merged ARC predecessor

The Help fix precedes ARC catalog repair `df1ec219` on main. The merged
catalog query, allowlist and restore tests match `4f01aa39` byte for byte.
The Linux rerun at candidate `ad7cf37e` measured the same three distinct
58 fingerprints. The unmodified schema 57 migrator on `df1ec219` passed
migration, replay and admission for an empty database and logical restores
at 55, 56 and 57. The schema 58 overlay passed those paths plus a restore
at 58 with the same exact digest allowlist measured above.

The original three scope tests and the full audit module, with checkpoint
readback, passed on a separate disconnected PostgreSQL fixture. Keeping
every restore database while also creating the module schemas exhausted
the first fixture's 512 MiB temporary volume. Separating the catalog and
module fixtures eliminated that capacity failure. The exact candidate
SQL, the catalog results and the fixture procedure are ready for ARC
review. Release activation still requires agreement on the 58 allowlist
and rerunning the gates with the production migration and release
lineage in place.

The disconnected repository gate also passed `make check && make build`
at `ad7cf37e` on the merged ARC predecessor. The release lineage in that
gate still ends at 57. The overlay and the standalone catalog matrix
qualify the candidate before production activation.

## Production integration

Activate the exact SQL from SCI's `ad7cf37e`, with the follow-up
evidence from `a8b86a2d`. Keep the original schema 57 migration files
immutable. Publish the migration with its signed-lineage source
checksum, version 58, the unchanged 118-table count and the three
measured catalog digests. The production test covers restored versions
55, 56, 57 and 58. An intentional catalog mismatch forces a rollback,
then the repaired catalog must pass and settle on retry.

Retain the Audit scope tests against the embedded SQL. For mixed V1/V2
history, append the immutable 54–57 prefix before exporting the
checkpoint, then use the production migrator for the 58 transition.
The non-default placement test applies the exact SQL in a disposable
schema to inspect its defaults independently of the current single-cell
runtime identity gate.

The production assets reproduced all three digests and 118 tables on
each path in the same PostgreSQL and pgvector image with networking
disabled. The exact SCI shell replay and Go migrator matrix exercised
fresh install, native 57, and restores at 55, 56, 57 and 58. The
production rollback regression rejected a real constraint change twice
before the repaired retry succeeded. The Audit module retained the
checkpoint, refused foreign writes and verified the retained export and
concurrent appends with the standalone verifier.

### Shared Linux replay gate

The shared Linux amd64 replay requires an idle repository gate, both
the shared heavy-test lock and the SCI replay lock, at least 2 GiB free
memory, 8 GiB available memory and 30 GiB available disk, and full I/O
PSI avg10 at most 5%. Run the SCI shell replay, production Go migrator
and offline verifier against the embedded migration and the pinned
PostgreSQL image.

The admitted amd64 run verified the exact SQL hashes and the six SQL
catalogue, checkpoint and 118-table paths. The source transport archive
included macOS AppleDouble files. The Go embed glob read the 58 extra
up-migration sidecars and the production migrator refused the sequence.
Stream the tracked Git archive into the outer payload, preserving its
file list, and check for exactly 58 up migrations and zero sidecars
before compiling. The corrected archive passed those checks on Linux
amd64. Admission held both locks at full I/O PSI avg10 of 0.04%.
The disconnected PostgreSQL 18.6 and pgvector 0.8.7 run passed all
six SQL paths, the Go migrator matrix, the cell-owned table check
and the Audit module. It retained the three schema-58 fingerprints,
118 tables, earlier checkpoints and the offline-verifiable chain.
The SQL replay accepted eight concurrent appends each after logical
restore from 55 and 58. The Audit module also passed scope refusal,
concurrent appends and mutation detection.

The clean archive was reused for the integration rebased on
`273929ee`. The Linux amd64 package list for the production database
and Audit tests and the standalone verifier selected 758 Go and
embed inputs across 48 repository package directories. None changed
from the archived tree `77795679e6c4dc82abbb5b5328ea8cb621c635f1`,
and the module manifests, release lineage and migration files also
match. Keep the archive SHA-256
`400305ad8c014ad6652301af098f2b3bc09276caf6d8aa8a0aaa4c39cad0993e`
for the SCI replay. The full repository gate runs against the actual
integration tree.

The integration replay comparison at `c04eb4e4` expanded the input
set to include repository test variants, embedded files and assembly.
It found the same 966 paths in the archive and integration tree, with
zero byte differences across those paths, module manifests, migrations
and release lineage. Run the separate full repository gate before
promoting 58.

## Exact integration admission

The joint IAM and Audit gate froze source commit
`0f02cf937479aeae9dcbdea8da4ee8ef27f551a5` on the accepted APP baseline
`279382f2`. Its equivalent code revision is `8c04ab6f`, rebased over the
subsequent three-file APP documentation update. Every non-Markdown file is
identical between those two inputs. The 57 published up migrations and
their release entries remain byte-identical, and all 58 source checksums
match the release descriptor. No NET-102 source-selection work is included.

The expanded Linux package comparison selected 971 repository Go, test,
embed and assembly inputs across 53 directories, compared with the
original archive's 966. Eighteen files changed in the model, assistant and
HTTP dependencies and test variants. Module manifests, migration SQL,
catalog admission and release lineage did not change. Rebuild the affected
Linux tests instead of attributing the earlier archive's run to this input.
The joint tracked-source archive SHA-256 is
`88b4a8b698e50bb95c9c56cc3e42f208baa2183fde693342eb7bd024f8cf3780`.

The supplemental Linux amd64 run held both original locks with full I/O
PSI avg10 of 1.78%, 8,245,684 KiB free memory and 763 GiB available disk.
Compilation used the pinned Go toolchain with networking disabled. The
PostgreSQL 18.6 and pgvector 0.8.7 fixture used `network=none`. Fresh install
and logical restores from 55 and 58 passed the production migrator, its
idempotent replay and admission. All three catalogs matched the accepted
schema-58 fingerprints with 118 tables. Both restored exports preserved
the original checkpoint byte-for-byte and passed the standalone verifier.
The complete Audit module passed, including mixed V1/V2 retention,
32 concurrent appends, foreign-scope and serving-role refusal, mutation
detection, retention and assistant disclosure. All 16 top-level checks
passed with no skips. The combined log SHA-256 is
`57af522c61ced035193816506be6c137ab45d230e223bf83af2df6a5b334cbe8`.
The owned PostgreSQL container, volume and credential files were removed.
The earlier six-history SQL qualification remains the original evidence
for the unchanged migration bytes.

IAM independently qualified the same frozen source: 12 HTTPS cases each
ran three times, all 54 SSO cases passed, real Gitea recovery and callback
replay passed, and three TOTP unit cases each ran three times. All four
stage exit codes were zero with no skips; the source archive, binaries and
raw log digests were checked independently. Those probes ran on Linux
arm64 and macOS arm64. Production startup still calls `sso.New`, so
IAM-119 remains open for its production wiring and UI acceptance.

The frozen joint input passed `make check`, `make build` and `make test`.
Both browser suites ran in sequence without automatic retries: Console
passed 1,075 cases with 28 existing live-test skips, and Portal passed 389
with nine existing live-test skips. Go, Rust and Web tests all passed.
The workstation gate used the shared lock, bounded concurrency and a
short-path private APFS temporary volume. Sleep prevention lasted only
for the gate process, and the owned volume was detached afterwards.
Documentation-only integration updates require the source-input
equivalence check and `make check` before fast-forward publication.
