# Read-only access to published training inputs

Research for TRN-101, DAT-101 and the ARC-111 composition boundary. Reviewed
2026-11-13 before implementation. The separate Agent gateway currently constructs
its dataset service without file access. The integration-ready source is the unmerged Linux candidate
5b73fa05305c2c5d8178a256683ede8b4e9ef99f on main ancestor 97a563ba.
Its complete dataset/training module bytes equal the qualified dc2db707 export
follow-up to the af5cefcd reader baseline. This note does not qualify that gateway,
its restricted database role or any new filesystem deployment.

## Current ownership and primary sources

The dataset and training stores retain one exclusive writer ownership flock.
Opening another full Store, including OpenStoreForVerification, correctly refuses
while control owns it. A verifier is an exclusive maintenance operation, not an
online reader. Do not remove or downgrade the writer/verification locks.

[Linux flock](https://man7.org/linux/man-pages/man2/flock.2.html) associates locks
with open file descriptions. Advisory writer exclusion does not prevent a
separate read-only descriptor from reading immutable bytes. A reader must never
unlock, replace or truncate the writer's carrier. Closing its independent
read-only descriptor cannot release the writer's lock.

[Go os.Root](https://go.dev/blog/osroot) anchors traversal-resistant operations to
an opened directory. The [Root contract](https://pkg.go.dev/os#Root) permits
concurrent access and retains its original directory if that directory is moved.
It is not an OS sandbox and does not prevent mount traversal. Retain current
private path/UID/mode checks, exact namespace identity, descriptor identity,
full size/hash verification and post-read checks. A changed pathname must not
silently switch an existing reader to a different namespace.

[Docker read-only bind mounts](https://docs.docker.com/engine/storage/bind-mounts/)
are an existing same-host mechanism for separately constraining a reader's writes.
This note does not assume a cross-host mount or qualify NFS/CSI. A deployment must
explicitly provide the same retained namespace at its configured artifact root;
no missing mount is created or adopted. Read-only submount behavior requires the
installation owner's real mount qualification.

[MLflow artifact stores](https://mlflow.org/docs/latest/self-hosting/architecture/artifact-store/)
separate large retained artifact bytes from SQL metadata and support explicitly
configured local/shared stores. This is a design comparison; no MLflow service,
package or protocol is imported.

## Chosen slice and rejected alternatives

First prototype independent read-only descriptors while the writer remains
exclusively locked, root replacement, changed bytes, symlinks, closure and repair
outside product code. Implement only after that mechanism holds. The dataset
owner then exposes a concrete read-only published-file reader with no mutation,
initialization or arbitrary callback. It independently owns and closes only its
root/carrier descriptors. Existing Store APIs retain their exclusive locks.

The service may use the concrete reader for published-source metadata admission,
training snapshot verification and authorized train/validation asset opens.
Neither the reader nor a retained snapshot authenticates a user/node or grants a
lease. Existing transactions retain current actor/Workspace/node/attempt locks,
retirement checks, frozen inventory comparison and the new authority pass after
file verification. Missing, closed, foreign or changed storage fails closed.

Reject giving a gateway the full writable Store, weakening ownership flock,
opening a new namespace, assuming a remote/shared mount, inventing an artifact
RPC, success callbacks, privileged retries and using SQL grants to hide missing
file access. Do not add processing/training/GPU workers to the gateway.

ClaimTraining currently needs dataset file verification and Models.ReadyWithin.
Gateway file transfers remain on control under ADR 0048. Preparation of an
accepted training adapter separately requires the training published store;
checkpoint resume/transfer and accepted adapter verification must retain their
own seal/receipt and current-attempt checks. Dataset access alone cannot qualify
all gateway claim profiles. Inventory these separate readers before claiming the
whole owner graph is closed. Model ownership remains outside this lane.

## Protected SQL locks remain a separate contract

PublishedSourceWithin locks dataset and immutable version rows FOR UPDATE;
training locks the current scoped job before its attempt/GPU checks. PostgreSQL [SELECT privileges](https://www.postgresql.org/docs/18/sql-select.html)
require UPDATE privileges for those row locks. Keep their order and lifetime.
ARC/database owners must admit narrow trusted-schema security-definer functions
through the formal migration/catalog/role workflow, not arbitrary id UPDATE
grants. This slice introduces no function, privilege, query rewrite, protocol or
runtime switch. Its reader qualification does not qualify the restricted role.

## Licences and offline behavior

No new dependency, model or dataset is added. Existing [Go/os](https://go.dev/LICENSE) and
[x/sys](https://github.com/golang/sys/blob/master/LICENSE) licensing is BSD-3-Clause;
[pgx](https://github.com/jackc/pgx/blob/master/LICENSE) is MIT; PostgreSQL uses
the [PostgreSQL License](https://www.postgresql.org/about/licence/). Commercial,
redistribution and offline use are allowed with their original terms/notices.
The external prototype uses only Python's standard library under the PSF licence
and generated fixture bytes; it adds no product Python dependency. Docker/MLflow
are comparisons, not new shipped inputs. No runtime network operation is added.

## Failure and completion checks

- Concurrent reader succeeds against actual published bytes while the existing
  writer and a second-reader descriptor remain open; another writer remains
  refused before and after reader closure. No source or namespace byte changes.
- Missing mount, wrong tenant/namespace, unknown root files, changed carrier,
  replaced root, symlink, changed/truncated/overlong file and hash mismatch refuse.
  Exact repair is retryable; no source or successful lease is exposed on failure.
- Cancelled/deadline reads close their descriptors. Explicit Close is idempotent,
  rejects later opens and cannot invalidate an already returned pinned file.
  Concurrent read/Close must be race-free and have bounded cancellation.
- The dataset service with Store=nil and the real reader verifies the same frozen
  train/validation snapshot and returns actual bytes. Zero/closed readers and a
  forged/mismatched snapshot refuse; arbitrary asset names cannot be opened.
- Actual PostgreSQL authorization/retirement/rollback/interleaving cases and the
  real ARC gateway graph must run separately when resources and owner integration
  exist. Preserve current-role revocation and final transaction after I/O. No
  standalone gateway, restricted-role or P1 completion is claimed by unit checks.

## External native mechanism result

The generated-only Linux amd64 descriptor prototype passed all ten checks in
0.0343 seconds, with 17,408 KiB maximum parent RSS and a 128 MiB address-space
ceiling. It ran no PostgreSQL, processor, Docker or GPU process. Evidence is
retained at /root/lane-trn-reader-prototype.af7ts6yw; prototype SHA-256 is
f8f74d6b50b66a61c6d28faae61a201b3d2e848b3da3c04ecf7d108513ae80ae.
Independent process reads and descriptor closure preserved writer exclusion.
Same-length file changes, missing/symlink files, namespace changes and root
replacement were refused; exact repair restored reads. The fixture was removed.
This proves the kernel descriptor/ownership mechanism only, before Go product
implementation. It does not qualify a mount, database role or gateway graph.


The proposed owner boundary is recorded in
[ADR 0077](../decisions/0077-read-published-training-files-without-writer-ownership.md).

## Concrete owner integration surface

Both owning packages now prepare the same narrow constructor shape:

- datasets.OpenPublishedReader(path, tenant string) (*datasets.PublishedReader, error).
  Set datasets.Service.PublishedReader and leave Store nil. PublishedSourceWithin,
  TrainingInputSnapshotWithin, VerifyTrainingInputSnapshot and OpenTrainingAssetContext
  use the concrete reader, with the original current-scope SQL and retirement checks.
- training.OpenPublishedReader(path, tenant string) (*training.PublishedReader, error).
  Set training.Service.PublishedReader and leave Store nil. AcceptedAdapterWithin,
  VerifyAcceptedAdapter, OpenAcceptedAdapterAsset and checkpoint input reads retain
  the accepted receipt, current-attempt and seal checks. Complete publication
  validation includes every declared file, not just the requested adapter file.

Neither field accepts an arbitrary interface/callback. The constructors initialize
no state and acquire no writer ownership. Their concrete types expose no mutation.
A zero or closed reader refuses use. Setting both a writer and reader on one
consumer fails with STORAGE_MISMATCH instead of silently selecting a file owner.
A scoped SQL miss retains NOT_FOUND; namespace binding is checked after that
scoped metadata read, so a foreign tenant does not become a configuration oracle.

ARC owns injecting both consumers into the actual newAgentPath/NewAgentGateway
graph and runAgentGateway startup. Resolve the installed tenant using the existing
trusted startup path, use its configured dataset/training artifact roots, and
close each successfully opened reader if the next step fails. Stop admission,
drain/join active claims, then close readers before the remaining process resources.
The reader pins existing immutable files; no training/processing/GPU worker starts.
The exact same namespace must be demonstrably present, with a read-only mount for
OS write enforcement. This slice does not manufacture a mount or claim a gateway
on another host has the files. Models.ReadyWithin is metadata-only but takes
FOR SHARE on model_versions; its restricted lock privilege belongs to its owner.
Model/preparation output stores and their lifecycle are also outside this slice.

## Exact SQL permission handoff

The remaining protected-lock contracts are fixed scoped row-returning functions,
not permission to mutate an arbitrary id column:

| Proposed closed function inputs | Exact owner row/lock shape | Existing caller behavior to retain |
| --- | --- | --- |
| dataset parent: tenant UUID, workspace UUID, dataset UUID | datasets exact scope, FOR UPDATE | Dataset NOT_FOUND and parent retirement |
| dataset version: tenant UUID, workspace UUID, dataset UUID, version UUID | dataset_versions exact parent scope, FOR UPDATE | Version NOT_FOUND; new selection refuses retirement, retained recovery keeps its exact version |
| training job: tenant UUID, workspace UUID, job UUID | training_jobs exact scope plus local_site_id/local_cell_id, FOR UPDATE | Current job/attempt, creator epoch/membership, cancel and GPU fences |

Use explicit RETURNS TABLE columns matching datasetFields, versionFields or
jobFields and their SQL types. This preserves the existing projection/scanner
without another lock query or changed error codes. Keep that return contract
closed when a later schema adds columns.
The [security-definer function](https://www.postgresql.org/docs/18/sql-createfunction.html#SQL-CREATEFUNCTION-SECURITY)
must bind the installed schema/relations in a parsed SQL body, fixed trusted
search path, VOLATILE/PARALLEL UNSAFE declaration and non-login definer with only required lock rights. Revoke PUBLIC
execution, grant only the dedicated role, preserve transaction lock lifetime,
refuse foreign/local-cell mismatch and keep direct table UPDATE denied. The
formal migration/catalog/role owner must prove writer blocking, rollback release,
shadowing denial, current-role changes and real pool/borrowed-executor lifetimes.
These are explicit proposed signatures/requirements, not implemented functions or
an activated grant catalog. This lane changes neither SQL locks nor privileges.
The separate opaque Node reader API remains ARC-owned and is still required for
the repeated node authority reads and unchanged fleet SQL budgets.


## Current owner verification and remaining acceptance

The complete candidate tree equals the Darwin verification copy. Records under
/private/tmp/ownsolid-trn-qualification.knvee3h8 bind each changed/new Go file
before and after execution. Existing dependencies resolved offline; no host
network-namespace egress isolation or native Go execution is claimed.

| Candidate verification on Darwin arm64 | Outcome | Log SHA-256 |
| --- | --- | --- |
| Concrete dataset/training readers | 10 passed test actions, zero skips/failures, 5.380 seconds | 63c7bc73d5a563042ad730899e5cf68e09257ae8f733815ca392c24531d8f8a9 |
| Same reader cases with race checking | 10 passed actions, zero skips/failures, 25.912 seconds | b68216e9d25f27af7ef653c30eee30673a6aeb14eae01eea82c444f17efb476f |
| Existing storage regressions | 16 passed actions, zero skips/failures, 5.362 seconds | 7910b275f82c344927fe74d289850a5dc30b9f5ae2ae251ad4344631aa5f9cf5 |
| Complete ordinary dataset/training packages | 253 passed actions, 86 environment skips, zero failures, 7.774 seconds | 5638b3e3e050989c6e3761c737ec87044826443d36026b3378e461aedd643c8a |
| repository_integration compilation | Passed, zero test bodies executed, 4.165 seconds | b2f7c7921ac1e3b6d14800cadb5d0964ead15157ecf5dc2ca426b73227c1c1ea |

Preserve the original failed owner compilation log, SHA-256
9b1fcbf5caa7c5fdfd071154b59a916ba37b47baa9adf867a191d2888e23dc54.
It found a local marker-descriptor variable redeclaration in the training
namespace check. The variable was renamed before the successful owner/race runs;
no assertion or threshold changed. The earlier dataset-only run passed five
reader roots; it is not an additional disjoint qualification.

The tagged dataset case covers concurrent real file access, exact snapshot
readback, retirement/refusal/retained recovery, corruption/repair and closed
reader rejection against selected SQL. The existing real accepted-experiment
case additionally includes the independent training reader with its accepted
receipt and actual retained CPU bytes. These tagged bodies remain unrun until a
properly handed-off resource window. The native descriptor prototype above
proves its own ten kernel checks, not these Go/SQL cases.

Standalone gateway composition, same-host read-only mount readback, protected SQL
functions/role catalog, Model/Node owner contracts and the original event/fleet
cost profile remain open. The reader source is private/unmerged; TRN/DAT boxes
remain unticked. Control-side training qualification and the reproduced separate
gateway failure remain separate records. No service is left running by the reader
prototype or unit tests.

The first complete repository check used the shell's system CPython 3.9 and
failed 12 existing code-generator imports on structural pattern matching before
web checks. Its original log SHA-256 is
9310a7e05b0662e211cf00aecc8f089f48afac4468e5c60e73387062aa64b544.
Pinned offline web installation had already passed. Retrying with the available
CPython 3.12.14 changes the driver interpreter/PATH only; product source and
assertions are unchanged. The initial check failure is retained independently.

A separate bounded Linux mount-namespace prototype will check a same-host
read-only bind against the still exclusively locked writer: exact inode/data
visibility, independent descriptor closure, refusal of create/write/unlink/chmod,
and automatic namespace teardown. It uses generated bytes, no Docker/PG/runtime,
and no shared mount propagation. Even success qualifies only that local kernel
mechanism; real installed root/UID/mount and Go/SQL composition remain required.

The exact af5cefcd source then passed complete make check in 30.554 seconds and
make build in 12.974 seconds on Darwin arm64 with CPython 3.12.14 and frozen
offline dependencies. The log SHA-256 values are
ae1ff3dc69c525a46caa08294683afa138f2bcbdea1c06707f94531ec5f9b073
and 954c5f4c41c21b0754ee43ec791e26314d6848bae73da441ab3356ec5bf3bd13.
No product source/assertion changed after the original interpreter failure.
The permanent workstation guard was held throughout each run and preserved;
the owned RAM device and generated private audit binary were removed.
A complete make test and new native Go/SQL owner acceptance remain unrun.

The generated-only Linux amd64 read-only bind prototype passed all nine checks
in 0.0898 seconds with 17,280 KiB child peak RSS. It used private mount and
network namespaces, retained the parent's exclusive writer lock and started no
Docker, PostgreSQL or processor. Create, write, unlink and chmod through the
reader mount each returned EROFS. Exact root inode/data and writer exclusion
remained unchanged after reader shutdown; the mount namespace and generated
fixture were removed. Evidence is /root/lane-trn-reader-bind.mjq9ns5j.
Prototype SHA-256 is
1211809b69db73c7557aaea1f19ca1c00f063b4016bc3f28ab937c9da4897a20;
the independent child source SHA-256 is
24cca98c19c10bb43dff63ede2172c23e94c02f8e9990d3e1212e3f007a59309.
This is actual local kernel/mount mechanism evidence, separate from Go product,
installed root/UID/mount, SQL-role or standalone gateway qualification.

## Complete caller inventory and export follow-up

Final caller review found one additional training read entry: the shared
withVerifiedAdapterExport helper still rejected Store=nil, and Export's direct
asset branch still dereferenced the writer. The follow-up uses the same concrete
publishedFiles selector in both places, preserving both current actor/Workspace
transactions, immutable-result comparison and final export audit. This is required
for the new real accepted-experiment tagged subcase; the earlier af5cefcd unit
checks alone did not execute that SQL body. Keep the original candidate/evidence
and record the follow-up source and gates separately.

The remaining dataset effective_source.go operations PublishedSourcesAvailable,
OpenPublishedSource and PublishedSourceSize retain their writer-only contract.
They serve effective-source cloning/scans and are outside the bounded train/
validation reader. A reader-only service must not advertise that full source
capability. Gateway claims call PublishedSourceWithin directly; they do not
create holdouts or run effective-source scans. Model-preparation recipe/startup
also require its own output Store and Training.Store; those control-only writer
paths must not be invoked as gateway reader initialization. The actual claim's
retainedPreparationSourcesWithin consumes training accepted metadata, ready model
metadata and the scoped holdout/dataset version; its current Model/holdout locks
and Node/current-attempt fences remain outside this reader change.

The export follow-up is dc2db707fd7734be3d611f3079b922e742d2b463. Its only
production change from af5cefcd is services/control/internal/modules/training/
export.go, SHA-256 3c304f374d1256557129c3bb673b68633585c62e574533592d34789f985fa5f9.
Both ordinary owner packages again passed 253 test actions with 86 environment
skips and no failures in 12.496 seconds. Log SHA-256 is
d4e43d264ab3ef449c78957acb97b7de62665aab8048b8073eeb3d6553cd1da5.
The earlier focused/race/file-reader and kernel results remain scoped to their
recorded sources. The real accepted-experiment SQL/export body remains unrun.

The original 31-file af5cefcd evidence archive is retained on Linux at
/root/lane-trn-tools/reader-af5cefcd-evidence.tgz, SHA-256
94bb894372b4e70cb1295b7c5f6ddfe8ff715cdf1ca76be64664d8ae64044bfb.
All entries were independently read back and hash-checked there. Its portable
source bundle, /root/lane-trn-tools/trn-reader-full-af5cefcd.bundle, SHA-256
28998a852e009d91ebc0a9c044df823653e0a9c31a655ec7b03c9b748399a791,
requires the existing 5a9b40d7 main ancestor. Preserve it alongside the export
follow-up rather than rewriting the original candidate qualification.

The first dc2db707 complete-gate attempt was refused by the permanent
workstation guard before a test/build or fixture started. Its ordinary-owner
module result is separate from the completed af5cefcd repository gates.
A busy gate is not a test failure or a passed gate.

The later dc2db707 complete gate acquired the same permanent workstation guard
and passed make check in 30.573 seconds and make build in 13.501 seconds with
CPython 3.12.14 and frozen offline dependencies. The respective log SHA-256
values are bbab278db219a867e6820bb9c62dc4b501f949dfa45dbb4d549c5c81a31fe621
and bf94be52598424cdb19ea255388aff199d2f0b4c02524431dfd0b9635a6978a8.
The owned RAM device and generated audit binary were removed, and the lock
carrier was preserved. The earlier busy refusal and source-specific evidence
remain retained. Full make test, native Go/SQL, installed gateway and restricted
role qualification remain open.

The portable current candidate is retained as
/root/lane-trn-tools/trn-reader-full-dc2db707.bundle, SHA-256
fe6da1ecc0d2595754bd79ed8dc7f3b93f07706efb5380fcc6999a7b095b3a1a.
It requires the existing 5a9b40d7 main ancestor and preserves both research
commits, concrete readers and the export follow-up. The event candidate c527fb68
remains separate and unchanged; the new reader evidence measures no fleet cost.

## Current-main integration-ready candidate

5b73fa05305c2c5d8178a256683ede8b4e9ef99f applies only the 21 owning module/
README paths to main ancestor 97a563ba. All owning dataset/training bytes are
identical to dc2db707; current scheduling implementation and the complete main
research/decision records are retained. No source-research add/add merge is
needed to adopt this candidate. The original qualified branches remain intact.

The exact new shared-dependency composition passed these Darwin arm64 phases
under one permanent workstation lock with CPython 3.12.14/offline dependencies:

| Phase | Actual result | Log SHA-256 |
| --- | --- | --- |
| Ordinary dataset/training owners | 253 passed actions, 86 environment skips, no failures; 9.735 seconds | 021c13cc95b9ab704e0e008fa32427e02d0438bd7e546979e11fcb67b017c724 |
| repository_integration compilation | Passed, zero test bodies executed; 2.964 seconds | 5d1219950ce8590fb05d5454e79cbdcde78bff3294b3fa1ae56d9763d2705a63 |
| Complete make check | Passed; 32.000 seconds | d79d308eff230a74b2648f247388b09632c616cd3ebca638e610d391aa518856 |
| Complete make build | Passed; 13.235 seconds | 4738b2ea3ff95fad0d2d98a470a3f5bc3965748577108329c919269c4dc923f4 |

All earlier evidence remains source-specific. The unchanged reader implementation's
race tests are recorded above; no new native Go/SQL, complete make test, gateway,
restricted role or fleet-budget result is inferred from this composition run.
The owned RAM disk and audit binary were removed; no service/worker was started.
The c527fb68 event branch remains unchanged. Its prepared native replay driver
pins c527fb68, so select that preserved source before invoking the event runner;
it must not silently execute the reader candidate instead.

## Prepared native authority/writer cases and parent-boundary review

The following observations describe preparation before the authorized single
attempt recorded below; they are not its final opportunity status.

The additive test-only source is e639db9035b0b2c0229c90175493c605f328e295,
based on preserved 5b73fa05. Production code is unchanged. Dataset coverage now
keeps its real writer open, checks its exclusion before/after reader Close,
performs authenticated snapshot plus actual file I/O and then refuses the real
logged-out session's final read. Training's existing corruption/repair, adapter/
experiment export, uncertain-outcome propagation and actual logout before the
final authority transaction now all use Store=nil with the concrete real reader.
Its independent writer is checked while the reader is open and after Close.
Original assertions, SQL locks, error codes and source/physical envelopes remain.

| Prepared test source | SHA-256 |
| --- | --- |
| datasets/published_reader_repository_integration_test.go | 28872e65701f152917e3ddb205af78399285d8c41d7d4751796bc817ee6bdf5d |
| training/accepted_adapter_repository_integration_test.go | e835a9565c6ee8f0851fc4457a4f2cf5f2e9d57aec9dc875dd71f8c1c20404ce |

Formatting, layout and roadmap checks passed. The workstation compilation
attempts were refused before startup by its permanent guard (raw code 75); no
new tagged compilation/body result is claimed for e639db90. The unchanged
production candidate's earlier qualifications remain scoped to their sources.
No unchanged complete make test was rerun; that gate remains open. WEB
subsequently recorded its focused Help fixes and seeded/fresh-client evidence
at f11aa11fc298c3eefed696a6f05db9e637ab0e05. This lane does not treat the
previous Help failure as an unchanged blocker or inherit WEB test results.

The existing native event parent SHA-256 is
dc3f6f5eb9d35dc421e06101a7ce1cdab1adfe329b283901948a0c949b6dc3a1.
It waits for both carriers and permits a 420-second child; the native shell's
five-minute test timeout also excludes setup/cleanup. These bounds do not meet
the assigned single opportunity: one first-lock kernel wait up to 60 seconds,
second lock nonblocking with immediate first release, and an absolute 300-second
parent lifetime including cleanup. The fixed c527fb68 driver is not repurposed.
Following the instruction to prepare exact inputs/source/plan when an existing
runner lacks those reviewed bounds, no new heavy execution framework was run.
There were zero Linux lock attempts and zero new native Go/SQL test bodies.
The single native opportunity has not been consumed; this is not a resource
refusal or a native test failure.

The exact plan is retained at
/root/lane-trn-tools/reader-native-qualification-plan.json, SHA-256
7e3a02017f214a264ae37e9b83524aab7a7cb58ddfaa81618b505d08eb3f6874.
Its source binding is reader-native-plan-source-binding.json, SHA-256
f858093a1ee7b4f065dcdea76c9a7bcc885f8798bc5d62d779baf00024e2d3ab.
The additive source bundle is trn-reader-native-test-source.bundle. The existing
owned shell fixture base is training/testdata/replay_accepted_experiment.sh,
SHA-256 8293475c3bdbeed24733c81d0a96e8ad9b5b4dffe8c0ff998753c1a54091656b.
The plan requires review of bounded deltas to that existing fixture/parent; it
is not an executable or an approved new native framework.

The original admission remains Free >= 3 GiB, Available >= 12 GiB, disk >= 15 GiB,
full I/O avg10 and avg60 <= 5 and no competing heavy process. Same regular-file
carriers remain device/inode 29/82 and 64512/7508772, UID 0, without replacement,
truncation or unlink. The plan reserves the last 30 seconds for owned cleanup
and records, limits the Go child to at most 170 seconds within the absolute work
cutoff, and retains at most two CPUs/1 GiB PostgreSQL, one Go package at a time,
GOMAXPROCS=2 and offline dependencies. It permits only the two exact owner roots,
requires the existing independent-reader subcase, and refuses skips or missing
roots. Its fixture has no host port/network route, no processor/GPU/fleet, a
private DSN shared by both fixture families, exact owned labels/names and bounded
process-group/container/volume cleanup. Busy/refused/timeout outcomes retain raw
codes and never trigger an in-round retry.

Retained CPU inputs were streamed and hashed without modifying them:

| Retained input | Bytes | SHA-256 |
| --- | --- | --- |
| result.json | 7405 | 42f0790680294637f8248a37a405cec920ee6d1d6c3b678248946b769781796f |
| adapter/adapter_model.safetensors | 4602248 | a46897f648b94c94a3fe55dff81c15a082e1a427126f958e3075a8feee2bf06b |
| adapter/adapter_config.json | 1092 | 4ed67247993152de870045c4551f54de48a5fb0b65d61118bf95acb2c15dfe67 |
| adapter/README.md | 248 | a6e9d9c398894029e948df596fe712e77bdd25f3e1a53f3dc51344b2f7d7a341 |

Their full private paths, unchanged preflight/policy hashes and cached Linux
amd64 PostgreSQL image ID are bound by the plan. Metadata inspection started no
fixture, container, child worker or service. No permanent carrier was acquired,
and no owned runtime resource remains. Standalone gateway/role/Model/Node
contracts, all product boxes and the original fleet budgets remain open.


## Bounded reader fixture review artifact

The prepared bounded wrapper keeps the existing fixture's PostgreSQL image, loopback
namespace, extension, private file DSN and exact reader roots. It replaces
only the parent/fixture lifetime and ownership handling in a separate review
artifact; the original event runner, source binding and plan remain unchanged.
[Python subprocess](https://docs.python.org/3.12/library/subprocess.html) documents
that child startup can exceed a communicate timeout and timeout handling needs
explicit termination/reaping. [Python signals](https://docs.python.org/3.12/library/signal.html)
are handled by the interpreter, so the wrapper must not claim an unconditional
hard wall-clock guarantee for uninterruptible kernel/daemon operations. It
arms an absolute timer before input inspection, reserve cleanup, bound every
owned child group and retain raw outcomes. No queue or retry framework is added.
[Linux flock](https://man7.org/linux/man-pages/man2/flock.2.html) supports one
blocking first acquisition and one nonblocking second acquisition; closing all
owned descriptors releases ownership. A failed second acquisition must close
the first before evidence writing or container work.

Pure local tests cover deadline arithmetic, strict full PSI parsing,
source/input binding, lock release order, child failure classification and
foreign-resource cleanup refusal. They mock the permanent carriers and
Docker/Go, leaving the native opportunity unused. The wrapper is not
qualified by these tests to run native owner bodies. Execution remains a
separate reviewed step. Python's standard library adds no dependency; its
[PSF licence](https://docs.python.org/3.12/license.html) permits commercial and
offline redistribution subject to its notices. Existing retained inputs and
image licences remain as previously recorded; nothing is downloaded.

The review bundle is /root/lane-trn-tools/reader-native-bounded-v2/. Its
review.md explains the exact parent/fixture deltas, deadline ledger, raw outcome
classification, cleanup ownership and retained limitations. The originals were
copied for comparison only; no original source, plan or binding was changed.

| Review artifact | SHA-256 |
| --- | --- |
| reader_native_bounded.py | a9b7a943abcf7c615906f6b71401ff34cf5490a6da7aaf6879007af9606f8589 |
| config.json | efac4a20bd4be8941a008a19fa52d5015120c3c6243e5467eb63f440719d534e |
| entry.sh | 6fa7a2aa825dbd3e27209319c7653394540126ff7e30cf0149bdd0965dcb4343 |
| parent.diff | 622a15aa03aafeaa21b5f4a6e28b2db422e02303f386e11d542021cb214dd6cb |
| accepted-fixture.diff | f265abe1ca411f79730be73fe9ff66ad48758978df49b4d0aa48c2757cd84548 |
| event-fixture.diff | f4e9f403f6fadc123262aaab0936cbf19c4ef37d198d104468533c1399b5709e |
| manifest.json | 0455559de4aa037b4ccae4da495baf837603eed0a8bb026a4bc03137cba463c4 |

The exact prepared entry is:

```sh
bash /root/lane-trn-tools/reader-native-bounded-v2/entry.sh --execute
```

That command has not been run. The initial v1 review bytes are retained,
unexecuted. The current v2 also preserves the live started state if final-record
writing fails, forces nonqualification and reports driver exit 1 instead of
misreporting zero starts after a late error.

The entry wraps the one-shot parent in a 295-second TERM / 5-second KILL backstop. The inner work cutoff is 270 seconds, cleanup
commands end by 295 seconds and the final five seconds are for records. Each Go
child including its termination/reap has at most the smaller of 170 seconds and
the remaining work budget. No new child work starts during reserved cleanup.
After a hard kill records can be partial; the outer raw status and exact owned
resources must be inspected before any cleanup claim. This is a prepared bound,
not an observed complete native wall-clock qualification.

Twenty-six pure local mock/file checks passed in 0.024 seconds, and Bash syntax
passed. Their log SHA-256 is
f683c70ad40eb0193d9b1fb4ce26ef8a724271bd9df233b7c14206d4a0d18a57.
One Linux --check-config passed against clean e639db90, the unchanged originals,
all source hashes and four retained CPU input hashes streamed in blocks. It only
stats the permanent carriers and runs bounded Git reads; it opens/acquires no
carrier and starts no Docker/Go/SQL body. The actual output is retained as
server-input-check.log, SHA-256
9ccb4cb633dae8db504cebc01fd3c2800367d043ef0b92cc596d9f60c0e8674d.

Preparation itself made no Linux carrier attempt and started no native
compilation, Go/SQL body, processor/GPU/fleet or runtime resource. The reviewed
wrapper was then authorized for exactly one attempt; its actual refusal and
opportunity status are recorded below. The workstation rule stays nonblocking
busy 75 with no additional queue. WEB owns its now-recorded focused Help fixes;
no complete suite result is inferred here. No P1 box, gateway/restricted-role
qualification or fleet budget changed.


## Authorized single native attempt: first carrier refusal

The first opportunity used the exact reviewed bounded-v2 entry, source
e639db9035b0b2c0229c90175493c605f328e295 and frozen plan/binding above. The
outer raw exit code was 73, FIRST_LOCK_BUSY. Its one blocking first-carrier
acquisition reached its 60-second deadline before ownership. The second
carrier was never opened or attempted. There was no in-round retry and this
first opportunity was consumed; it did not authorize an automatic
second attempt. No parent, source, test, admission threshold or timeout was
changed after the refusal.

| Actual observation | Result |
| --- | --- |
| Complete entry wall time | 60.128525 seconds |
| Parent elapsed at its final record | 60.055 seconds |
| source_head child | Exit 0, reaped, group absent on audit |
| source_status child | Exit 0, reaped, group absent on audit |
| First carrier | FIRST_LOCK_BUSY, raw 73, no ownership obtained |
| Second carrier | Not opened or attempted |
| Original admission, PG creation and Go | Not started |
| Native roots, subtests, packages and skips | All zero; no native test result |
| Schema and DSN | None created; no Go fixture environment applied |
| Actual parent platform | Linux x86_64, Python 3.12.3 |

The pre-entry resource snapshot had 738 GiB disk free, MemFree 10,290 MiB,
MemAvailable 20,038 MiB and full PSI avg10/avg60 of 0.32/5.71. That snapshot
was not admission: first-lock refusal occurred before either the strict PSI
check or original preflight ran. The recorded policy remains Free >=3 GiB,
Available >=12 GiB, disk >=15 GiB and full avg10/avg60 <=5, with no competing
heavy process. The observed 5.71 is not relabelled a passing resource check.

The read-only post-run audit found no live exact entry/wrapper process, no
remaining recorded child group and no container with this run's exact name
lane-trn-reader-cda71224da6549fe95d9de11dae66c1d. Its inner evidence directory
contains only result.json; no DSN, database, anonymous volume or worker was
created. The first carrier FD was closed and its process exited; the second
was never opened. Both permanent regular carriers still have their original
device/inode/UID, zero size and 0644 mode: 29/82/0 and 64512/7508772/0. The
audit acquired neither carrier and made no cleanup mutation; foreign resources
were untouched. e639db90 remains clean and c527fb68 remains unchanged.

| Retained evidence | SHA-256 |
| --- | --- |
| /root/lane-trn-reader-native-outer.jdncpuqh/receipt.json | 02143c12cee6538123d3d5b9aae20c59c64e038c62289f3298d798f66603ab45 |
| /root/lane-trn-reader-native.2gldxtkt/result.json | 53b36680e6eb696f01c2ddbb5689ac99155f09e825742f081b6437f4a8c0338f |
| outer.log in the receipt directory | a362548be564ba5dfb1afec9f294859e3cb6a07a525cc93c8fd19a537da2089f |
| owned-cleanup-audit.json in the receipt directory | ef394161d778bde314cae5fb359b1fcf07bf9a4bcc54d4ae6de6cda60bb8e7f5 |
| evidence-manifest.json in the receipt directory | be908255c4089a49f929c570d88b7c4d2243c2a43a821be60c67970659ba096d |
| /root/lane-trn-tools/reader-native-single-attempt-e639db90.tgz | be3ae7c56dec995a1d44546a4fafa7b663edf6fcd2025745127f760a462f8c60 |

The archive has 20 entries and 32,965 bytes; it includes the exact reviewed
wrapper inputs, receipt/result, audit and per-file manifest. No model, data or
database enters Git. The owner reader source stays private and unmerged.
Darwin and kernel-prototype qualifications keep their original source/platform
scope; zero native bodies does not establish current-role, writer-exclusion,
SQL, restricted-role or installed-gateway acceptance. Actual ARC constructor,
startup rollback/drain/Close, read-only namespace delivery, Model/Node and
protected-function/grant contracts and the original fleet budgets remain open.
All product boxes remain unchanged.


## Authorized new opportunity after carrier release

A new explicit opportunity followed actual release of the permanent carriers.
It used the same reviewed bounded-v2 entry and clean frozen e639db90, unchanged
plan/binding, inputs, thresholds and timeouts. It did not relabel or overwrite
the original FIRST_LOCK_BUSY receipt/result. Both carriers were acquired, but
the original admission failed on free memory before any PostgreSQL or Go work.
The entry returned RESOURCE_REFUSED (driver/outer raw 78), with original
admission child raw 1. Its complete wall time was 0.359098 seconds and parent
elapsed at final record was 0.273 seconds. This new opportunity is consumed;
there was no in-round retry or wait for memory to recover.

| Actual admission field | Observation | Original requirement |
| --- | --- | --- |
| MemFree | 1,759,084,544 bytes | >=3,221,225,472 bytes; refused |
| MemAvailable | 16,132,050,944 bytes | >=12,884,901,888 bytes |
| Disk free | 790,032,785,408 bytes | >=16,106,127,360 bytes |
| Full PSI avg10 / avg60 | 0.01 / 0.08 | Both <=5 |
| Competing heavy process count | 0 | 0 |

The raw assertion was shared-host free memory below admission threshold. No
threshold was lowered, cache flushed, other process stopped or fixture
changed. source_head/source_status exited 0 and original_admission exited 1;
all three were reaped and their groups were absent on audit. No compile,
native owner root/subtest/package, SQL, processor, GPU or fleet body started.
There were zero test actions, including skips; this is no native test result.
No schema or DSN was created and the Go fixture environment was not applied.
The actual parent ran Linux x86_64, Python 3.12.3.

Both owned carrier descriptors were released with no recorded release error.
The read-only audit found no live exact entry/wrapper process and no container
with the exact run name lane-trn-reader-dfbcb03d811645eda2500510611230f9. No
container or anonymous volume creation was attempted; the private evidence
directory contains only admission.log and result.json. Both permanent regular
carriers remain 29/82/0 and 64512/7508772/0 (device/inode/UID), mode 0644 and
zero bytes. The audit acquired neither carrier and performed no cleanup
mutation or foreign-resource operation. All heavy leases had already ended
before the subsequent evidence/document work.

| New opportunity evidence | SHA-256 |
| --- | --- |
| /root/lane-trn-reader-native-outer.j49w8wib/receipt.json | 2a2f375737ca697d66aceb549712b1d7732db4fe541dc5805312985ff50e8dfa |
| /root/lane-trn-reader-native.f6vcj2uw/result.json | df9f2e6a4385484a67506722a5962d02da1f1034646b6629b3ce499e5aafe09f |
| admission.log in the result directory | f984c56f75fbef62f6ffc38031238c8e6ec340bdf347ffd475e9fc1ce9d53b3a |
| outer.log in the receipt directory | 0837f96c65f9515f5511087ca7ae34713203eec29d538a1b2cda50f62c551c93 |
| owned-cleanup-audit.json in the receipt directory | dc701e64adf36e374ec23236b659d35414faef94354a56068b4fe11270000e0d |

The new archive /root/lane-trn-tools/reader-native-release-attempt-e639db90.tgz
has 21 entries and 33,508 bytes, SHA-256
18b850bc392e18942ff243f2f6875787972020690a1cd90688d19ff25286e613.
Its evidence-manifest.json in the receipt directory hashes to
95ac911b3b4f345945b32a6d7e55bfe7cc0f1765f0c27cbe0dfcfd0e1ac890a7.
It includes the exact reviewed inputs and new admission/result/receipt/audit.

The prior receipt/result retain their original hashes. The reader producer
source stays private and unmerged; actual owner SQL, ARC construction/startup
rollback/drain/Close, read-only namespace delivery, Model/Node/protected SQL
and original fleet-budget acceptance remain independent open work. No new
Mac wait or whole-suite run was added. Product boxes and original support
scopes remain unchanged.


## PostgreSQL readiness status contract

The post-DEV opportunity reached the cached PostgreSQL fixture but exposed an
error in the prepared v2 wrapper: its first pg_isready returned raw 2 while
initdb was still writing configuration, and the wrapper treated that as fatal.
The [PostgreSQL 18 exit-status contract](https://www.postgresql.org/docs/18/app-pg-isready.html)
defines 0 as accepting, 1 as rejecting, 2 as no response and 3 as no connection
attempt. A response of 2 does not establish a permanent database failure.

The chosen minimal preparation fix is to retain 1 and 2 as startup probe
outcomes inside the existing bounded readiness loop, require 0 before any
extension or Go invocation, and keep 3/other errors fatal with their raw
status. Preserve every numeric timeout, carrier rule, resource/image/source
binding, test root and cleanup limit. Reject indefinite polling, native-body
retry, reclassifying the original failed attempt or executing a modified parent
under the consumed fixed-v2 opportunity.

Pure local regression checks simulate 2/1/0 readiness, invalid-parameter
status 3, and persistent status 2 exhausting the existing readiness window.
They verify that readiness timeout never starts Go and still cleans only
the exact owned fixture. All 29 checks passed in 0.029 seconds. Docker,
permanent carriers and Go are mocked; this is no native qualification.
No new dependency or licence is introduced.


## Post-DEV opportunity: actual startup failure and release

The separately authorized post-DEV opportunity used unchanged bounded-v2
with clean e639db90. Its fresh pre-entry original preflight passed; its
guarded original preflight passed again after both carriers were acquired.
The guarded observation was MemFree 4,791,730,176 bytes, Available
20,741,779,456 bytes, disk free 789,123,780,608 bytes, full PSI avg10/avg60
0.00/0.01 and no competing heavy process. These actual observations replace
no earlier refusal or threshold.

The entry returned 1 in 2.344562 seconds (parent final-record elapsed 2.266
seconds): CHILD_FAILED, phase pg_ready, raw child 2. Source head/status,
admission, Docker context/image identity and PG create/start each returned 0.
The first readiness probe returned 2 while postgres.log still showed initdb
creating configuration. The wrapper's readiness-state error above stopped
the attempt. No second native invocation or changed parent ran in this
opportunity, and its failure remains intact.

No extension command, product schema migration, Go compiler/test, native owner
root/subtest/package, processor, GPU or fleet body started. There were zero
test actions, including skips; no native owner/SQL result is inferred. No Go
fixture environment was applied. The parent was Linux x86_64/Python 3.12.3;
the cached pinned Linux image identity passed, but no server SQL/version
query or vector extension ran.

All cleanup child exits were 0, including exact presence/identity, logs, stop,
remove-v, container readback and owned-volume readback. The independent
read-only audit found all 15 recorded child groups absent, no exact live
entry/wrapper process and no DSN or named container. Container
a1a93e2e2e77805c998a9b99d71ede864c9023515fac5d2f2fa67023fc1c1183
(name lane-trn-reader-a7eebd7223234f779de2a8607e3b06d5) was removed. Its
volume e22ae2a318efce7d269851b7353c674f4bdfc69728dcd53fe43619f611d74a68
was recovered from its container-bound unmount event and absent from the
successful volume-list readback. Both original carrier FDs were released;
the regular objects remain 29/82/0 and 64512/7508772/0, mode 0644, zero bytes.
No post-run cleanup mutation or foreign-resource operation was needed.

| Actual retained evidence | SHA-256 |
| --- | --- |
| /root/lane-trn-reader-native-outer.ifbikxy_/pre-entry-receipt.json | 9dfcb34dc7a854a162df40acf64423cf2b9bf91470ca4782566e7c25487ee899 |
| receipt.json in that outer directory | 6de5dbfc7a5160fa2cb5e892ea43cbda63bfb895cf958a4e3e136f70019f92da |
| /root/lane-trn-reader-native.xbl3dpmg/result.json | b64bc5892bcf712be222c23bfdf158382414b07b38606cded3e12b6b314f3faf |
| admission.log in that result directory | 18dff289bb87ccf3b2d88fdfc69e4c2235cae0627c0516571dbe7169bae9f196 |
| postgres.log in that result directory | 90ffb397eb61630755f7cba46d68b5850a4f3b827a76853f9166c810098b7ac3 |
| outer.log in the outer directory | d28f65a268fe7e34e16e17beb23d3500f7db00ee81ce646b31553a5aa4956ef9 |
| owned-cleanup-audit.json in the outer directory | 20379726457cbb5cde4b26b39ba04a5eea16185398d3245290ab14fa34d9a01b |

Subsequent archive/download SSH commands failed with transport 502/raw 255.
No successful archive or local raw-evidence download is claimed for this
attempt. That post-run transport failure is separate from the real pg_ready
raw 2 and entry raw 1. Cleanup had already completed and been audited.
Earlier receipts, sources, plan/binding and native/refusal classifications
are unchanged.

## Prepared v3 readiness correction

The local review artifact is native-bounded-review-v3 in the private
qualification directory. Only the parent conditional changes from rc != 1
to rc not in (1, 2). v2-to-v3.diff is the exact one-line delta; config and
entry bytes retain their v2 hashes. No numeric bound, identity, carrier,
resource, image, test-root or cleanup behavior changes.

| New preparation artifact | SHA-256 |
| --- | --- |
| reader_native_bounded.py | 463b216824c437ccad5cd09444ed8c7594bcc3a33bca04b30ae87d6e16d77284 |
| v2-to-v3.diff | 376b9de42527638fe91108a34d4917673b9574b1cc7b3ad34420c35f2cdac6f1 |
| local-review.log (29 pure local checks) | 1bb0562dde3fb44441aa5d02e3c0bdc70902801dae57e26954dff340f92cfe47 |
| Complete local manifest.json | 12a3d7f59af7b28c99f8e3fd90fe6feef3108617349322b8dc8b636b56bce5dc |
| Local reader-native-bounded-v3-review.tgz (30,024 bytes) | c0203119f335e0900a7ae6638fd44be45891f2ab1cf215d9d177b2035aed120e |

The three core files transferred to /root/lane-trn-tools/reader-native-bounded-v3/
with exit 0. Its single read-only --check-config also returned 0, but raw
output and remote SHA readback were not independently fetched. The remaining
review metadata was not uploaded after the separate transport failures; the
complete local manifest/review remains available. No v3 --execute occurred.
The consumed frozen-v2 opportunity does not admit a modified-parent execution.
Owner source remains private/unmerged, native reader/SQL and ARC actual
construction/startup/Close/drain, Model/Node/protected SQL and fleet budgets
remain open. All product boxes and original support scopes remain unchanged.

## Bounded native owner qualification plan

Preserve 5b73fa05 and the original evidence. Prepare an additive test-only source
that keeps the independent writer open while reader-only services exercise both
owners against real PostgreSQL. The training experiment's corruption/repair,
export, uncertain-outcome propagation and actual final-session logout must run
through its real PublishedReader. The dataset case adds authenticated snapshot
readback and final authority refusal after real file I/O, plus explicit writer
exclusion before and after reader Close. All existing SQL locks/assertions stay.

The previously prepared event parent is not eligible for the new native window:
it waits for both locks and allows a 420-second child; its shell test timeout is
five minutes before setup/cleanup. That does not meet the assigned 300-second
parent lifetime and nonblocking second-lock contract. Do not repurpose the fixed
c527fb68 event driver. Prepare the exact test source, input hashes and bounded
runner-delta plan before any new heavy native framework is run. No lock attempt
or native SQL outcome is implied by this preparation.
