# Training dataset lineage (DAT-106)

[OpenLineage's dataset version facet](https://openlineage.io/docs/spec/facets/dataset-facets/version_facet/) identifies the input version supplied by the data store. [MLflow Dataset Tracking](https://mlflow.org/docs/latest/ml/dataset/) records a source and a content digest with the run and distinguishes training, validation, and evaluation inputs. These designs inform the ownsolid projection. They are not runtime dependencies.

## Retained sources

A dataset publication has an immutable version ID, a manifest digest, and separate train and validation byte digests. A training job freezes the version and manifest at creation. Each attempt command binds both partitions. A new version or retirement changes dataset admission without changing earlier commands. Recovery of an attempted job can read the retained version under the original workspace authority and must fail if the retained bytes no longer verify. An exact replay of the original creation key returns the original job. Reusing the key with another version is a conflict.

Use the retained accepted adapter read for exports and for model preparation. It reconciles the frozen job, the stopped attempt's command, the physical stop receipt, the accepted result, and the adapter inventory. Keep the public export fields and recheck current authority after physical verification. Looking up the latest dataset version would silently substitute inputs. A second lineage ledger would duplicate the same source of authority and introduce a consistency problem. No dataset scan or file buffering is needed for this projection.

## Failure coverage

Exercise a real processor-backed clone, changed row content, publication, and retirement in PostgreSQL. An existing job's retry must still read the original train and validation bytes. A new job cannot use the retired version, a different workspace cannot reference it, and the successful export and retained preparation source must agree. An inconsistent archived job and accepted result must fail closed even if the accepted adapter files still verify. Tests inject this inconsistency only into an isolated fixture schema.

The existing test stack uses [PostgreSQL](https://www.postgresql.org/about/licence/), [pgvector](https://github.com/pgvector/pgvector/blob/master/LICENSE) (PostgreSQL License), [pgx](https://github.com/jackc/pgx/blob/master/LICENSE) (MIT), [google/uuid](https://github.com/google/uuid/blob/master/LICENSE) (BSD-3-Clause), [Go](https://go.dev/LICENSE) (BSD-3-Clause), and [CPython](https://docs.python.org/3/license.html) (PSF License). They permit commercial, offline use and redistribution under their notice terms. The pinned [Qwen3-0.6B configuration](https://huggingface.co/Qwen/Qwen3-0.6B/blob/main/LICENSE) is Apache-2.0 and may be used commercially and redistributed offline subject to its terms. The test uses the existing approved configuration, synthetic weights and optimizer files, and test-generated CC0 records. It downloads neither a model nor a dataset. [OpenLineage](https://github.com/OpenLineage/OpenLineage/blob/main/LICENSE) and [MLflow](https://github.com/mlflow/mlflow/blob/master/LICENSE.txt) are Apache-2.0 design references. No dependency, model, or dataset is added to distribution.

## Model and UI handoff

Model preparation already freezes the training command and adapter together as a source identity through `AcceptedAdapterWithin`. Project the original training dataset ID, version ID, and manifest SHA-256 from that command for the model's lineage. Keep the preparation holdout's lineage separate. The derived catalog already carries `scope.preparation_id`, the preparation attempt ID, artifact ID, and report digest. Use these scoped references to resolve the retained preparation snapshot when presenting or exporting model lineage. DAT-106 also requires end-to-end evidence from the run and derived model views.

## Preparation detail projection

The preparation detail service can return two named dataset version references after validating the retained version 1 snapshot. Training comes from the frozen command. Holdout comes from the frozen preparation input. The read must retain the existing session, tenant, workspace, preparation, and training attempt checks and reject a conflicting snapshot or digest without returning either reference. The projection only uses validated metadata. It requires no active dataset lookup, file read, or network access. Legacy preparation records keep their existing snapshot format, and public JSON stays unchanged until the API and UI owners bind the fields.

Verify the detail read with a version 1 snapshot in PostgreSQL, a separate holdout source, and a second version of the training dataset. Retiring the original version must preserve its ID and digest on subsequent reads. A request in a different authorized workspace must not find the preparation. An internally consistent snapshot that names a different training attempt must fail against the scoped preparation row. Compare the serialized detail before and after projection to guard the existing HTTP response shape.

`TestPreparationDetailRetainsDatasetLineage` and the existing `TestActualPairedPreparationReview` passed on PostgreSQL 18.6 with pgvector 0.8.7 inside a Linux arm64 container with no network interface beyond loopback. The test seeds synthetic source and artifact metadata under the ordinary SQL constraints. It verifies the scoped read and compatibility with retained snapshots, without running training or rebuilding model artifacts.

## Derived model detail

The model registry detail read binds the ready model and its retained derivation
through the existing catalog validator. PostgreSQL's [row lock rules](https://www.postgresql.org/docs/18/explicit-locking.html#LOCKING-ROWS)
allow the existing model and derivation share locks to remain held through the
scoped source read. Reconcile the approved preparation, its current attempt,
approval, artifact, base model and report digest in that transaction. Decode
the frozen preparation input with the same version 1 codec used by the
preparation detail. Match the command and holdout references to the retained
training job, adapter and holdout records. The training and holdout triples
remain distinct even after retirement. The current dataset head and physical
model or dataset files are outside this metadata read.

Keep the existing catalog digest, publication bytes, and public model
response. A detached source query or a second lineage record would create
another authorization or consistency boundary. The snapshot codec is shared
by the two owning modules without introducing a dependency on either service
or modifying the training wire contract. The existing license inventory above
applies, with no new dependency or distributed asset.

Verify against real PostgreSQL using the existing publication graph and a
version 1 preparation snapshot. Add a newer training version, retire the old
one, read the original training and separate holdout triples, and exercise
both the explicit scope and the administrator model-by-ID read. Refuse a
different workspace, inconsistent catalog references, inconsistent relational
approval and artifact references, and a corrupted snapshot or source tuple.
Compare the existing response serialization and verify previously published
model metadata reads without a data migration.

The published detail and reference refusal tests passed on PostgreSQL 18.6
with pgvector 0.8.7 in a Linux arm64 container with only loopback. The fixture
publishes synthetic metadata under the existing SQL constraints. It verifies
the version 1 encoding, retained training and holdout triples, and unchanged
catalog and HTTP bytes. Fault cases alter each catalog reference or inject
inconsistent metadata into the disposable schema. This exercises metadata
readback without running training, evaluation or model file transfer.

### DEV contract handoff

Use `Version.DatasetLineage` in the Admin detail handler for `getAdminModel`
(`GET /api/v1/sites/{site_id}/cells/{cell_id}/admin/models/{model_id}`).
Define a detail-specific success schema so lists and `getPortalModel` keep
their existing shapes. Add optional `data.dataset_lineage` to Admin detail.
If present it requires two objects, `training` and `holdout`. Each requires
`dataset_id` and `dataset_version_id` as canonical lowercase UUIDs and
`manifest_sha256` as 64 lowercase hexadecimal characters. Use closed
objects with no extra fields. Omit the field for a base model or when the
repository cannot project lineage. An invalid derived graph returns the
existing `MODEL_DERIVED_CATALOG_INVALID` error rather than an omitted field.
Use the current model manifest for preparation, attempt, artifact, base and
report references. Leave that manifest and its digest as published.

In `web/packages/api/src/models.ts`, return the generated Admin detail type
from `getModel`. In `web/apps/console/src/model-page.tsx`, use the existing
detail query and add the S-202 Versions and lineage tab. Keep the existing
base/import links from `Origin`. Label the two sources separately as training
dataset version and preparation holdout version. Show each retained version
ID and digest even when a dataset was retired. A record with no lineage field
needs a localized unavailable state. Keep permissions and data visibility
on the server and use the existing model detail error and retry behavior.

### Admin HTTP projection

Project the verified version references only after the Admin model detail
service succeeds. Keep the response-specific types in the models HTTP owner.
The existing model version type is shared by lists and Portal and continues
to exclude lineage. The detail serializer must omit the optional property for
base models and preserve the existing problem response for a refused read.
The [OpenAPI response object](https://spec.openapis.org/oas/v3.1.1.html#response-object)
allows the Admin operation to reference its own success schema. Check the
actual response against the schema assigned to that operation.

Reuse the migrated PostgreSQL and trusted loopback HTTPS fixtures. Verify
an existing version 1 publication, retained training lineage after dataset
retirement, separate holdout references, exact base and Portal responses,
unchanged lists, and a problem-only refusal for invalid metadata or
unauthorized requests. This metadata read uses the existing dependencies and
requires no model files or network beyond loopback.

## Remaining model version history boundary

The [MLflow registry model](https://mlflow.org/docs/latest/ml/model-registry/)
separates a registered model identity from its immutable versions and their
source runs. ownsolid already persists version manifests, derivations,
preparation snapshots, approved reviews and training/holdout references.
`RetainedDerivedVersionWithin` supplies a retained same-scope catalogue to
trusted consumers, and the Admin detail projects verified dataset lineage.
Those existing reads and the publication graph remain the source of truth.

The current Admin and Portal detail reads select `state='ready'`. Their
contracts accept the existing model identifier and have no historical-version
selector. The ready catalogue index is unique within tenant and workspace.
`model_versions.name` is a display value, while the approved `model_key`
also appears on distinct derived models. Neither is a declared registered
model identity for grouping arbitrary versions. A history implementation must
preserve existing deployment references to exact immutable version IDs.

The next public flow needs a shared family/version identity and bounded
history contract from DEV, followed by the S-202 consumer owned by Web.
Define authorized access to retired metadata separately from admission to
serve or train with a version. Reuse the retained graph when that contract is
available. Verify multiple versions, retired metadata, immutable parent and
artifact bindings, stable paging, exact-version deployment references and
cross-workspace refusal through the declared route. This is still within
MOD-103. The dependency does not authorize a second lineage ledger or a
production approval synthesized from native CPU qualification. This review
adds no runtime dependency, model or dataset.

## Physical derived artifact verification

The inherited storage test requires a retained `cpu_verification` report and
its nine original model files. The producer also records its training input
provenance. The base-model bundle cannot substitute for the merged weights.
The historical producer was retained under the previous project's external
evidence volume. That volume was absent when this verification began. The
local retained Qwen3-0.6B download can be checked against the embedded pinned
catalog. A minimal replacement is one real CPU LoRA training step using
generated CC0 test rows, followed by the existing approved CPU preparation
worker and standalone reload. Use the published lock with wheel hashes and
block network access for both workers. Keep their reports and output outside
Git. [PEFT's checkpoint guide](https://huggingface.co/docs/peft/main/en/developer_guides/checkpoint)
describes the separate adapter and merged artifact formats.

The reconstruction uses the existing native training lock. The model is
Apache-2.0. [PyTorch](https://github.com/pytorch/pytorch/blob/main/LICENSE) is
BSD-3-Clause and [Transformers](https://github.com/huggingface/transformers/blob/main/LICENSE),
[PEFT](https://github.com/huggingface/peft/blob/main/LICENSE), and
[Accelerate](https://github.com/huggingface/accelerate/blob/main/LICENSE)
are Apache-2.0. These permit commercial and offline use and redistribution
under their notice terms. No package, asset, or dataset is added to the
distribution.

Run the full-file storage test against the actual CPU output. Then use the
Admin HTTPS fixture to check lineage and the same published file inventory
together. Keep actual CPU production, scoped SQL metadata, and browser
acceptance distinct. A file mutation must fail storage verification and
replay. A conflicting report reference must fail the existing Admin detail
read. The metadata route does not hash large files per request.

### Retained CPU acceptance

The retained base matched all nine files in Qwen3-0.6B revision
`c1899de289a04d12100db370d81485cdf75e47ca` at 1,519,207,673 bytes.
On macOS arm64, CPython 3.12.15 and all 37 packages from the existing
native lock ran the protected one-step training worker and then the CPU
preparation worker under kernel network denial. The inherited harness used
the runtime and contracts from `779592a6` in a disposable layout. Its RSS probe used
the locked psutil package because the macOS network sandbox blocks the
set-ID `ps` executable.

The one-step training result SHA-256 was
`d971dfa90d4203a33636b20e54900482f2baa9e6b24cb80831536c8686863485`.
The preparation produced nine BF16 files totaling 1,208,042,441 bytes.
The merged weights SHA-256 was
`4a1168c713717d70f57bbbf0d44ef2cb902a4bdac759a4c0624fbe6ba7fbf974`.
The untouched preparation report SHA-256 was
`f8cb63963210b9af5886624e107b05386bc8a01894eb0293fbe7c3cd9f4539d3`.
Both models scored one of three on the test holdout and the production
quality threshold failed.

`TestActualCpuDerivedModelOrdinaryCopyCompleteHashesRestartReplayAndTamperRefusal`
verified full ordinary copies, reopening, exact replay, and refusal to
verify or adopt an altered copy. The same report and files passed the
Admin lineage HTTPS fixture on disconnected PostgreSQL 18.6 with pgvector
0.8.7. It matched the Admin catalog to the original report and inventory,
reopened and replayed the private copy, and matched all 152 transferred
ranges and the assembled hashes of all nine files. It exercised the same
lineage read and refusal cases used by the Web acceptance. PostgreSQL
publication identities and approval in this fixture are synthetic. The raw
CPU report and all transferred model files are real. This run establishes
artifact integrity and retained lineage readback, with GPU qualification
and product approval outside its scope.

Set `PROJECT_ONE_ACTUAL_PREPARATION_OUTPUT` to the exact absolute directory
containing the CPU `result.json` and its `model` directory. Run the named
storage test with `-tags model_lifecycle_derived_storage_integration`. With
the same output and `PROJECT_ONE_TEST_DSN_FILE` pointing to a disposable
PostgreSQL fixture, run `TestActualAdminModelDetailDatasetLineageHTTPS`
with `-tags repository_integration` in
`services/control/internal/modules/models`. The HTTP fixture without the
producer directory continues to serve the metadata-only Web acceptance.

### Bounded qualification rehearsal

The retained 1/3 outcome was [independently recalculated](evaluation-reproducibility.md#evl-103-retained-preparation-quality-decision).
Keep the exact 8,000-basis-point accuracy floor and zero-regression
requirement. Production preparation creation and the node command require
the NVIDIA BF16 profile. Review checks the authorized actor, persisted
request, artifact, report, and authenticated physical stop before publishing.
The inherited positive CPU review fixture sets a 3,000-basis-point floor
and substitutes production identity and physical stop. It cannot establish
qualification at the original floor.

Use one bounded native rehearsal of the real workers. Freeze generated
support-routing examples with the dataset publication-preparation processor
and bind the exact manifest, train and validation bytes to training. Keep
the original three holdout cases unchanged and outside both partitions.
Train against the recipe's exact instruction prefix and completion labels.
This matches the existing completion-only loss and the [upstream SFT
interface](https://huggingface.co/docs/trl/sft_trainer#train-on-completion-only).
Use the [pinned Qwen release](https://huggingface.co/Qwen/Qwen3-0.6B/tree/c1899de289a04d12100db370d81485cdf75e47ca)
and the same locked packages and license conditions as the earlier run.
The generated source rows are CC0. There are no new dependencies or
external data. The worker uses the local model and tokenizer with network
access denied, consistent with the [upstream offline loading interface](https://huggingface.co/docs/transformers/v5.19.0/installation#offline-mode).

Keep the trial independent of any simulated production approval. The
qualification trial uses one protected training run and one protected
preparation run. Replay once with the authorized publication export to
verify the binding. Keep the recorded quality verdict in both runs. A
passing CPU run still needs a fresh production-profile attempt, physical
stop and authorized review on the same lineage before either item can close.

### Qualified CPU result and authorized dataset publication

No quality-passing report at the original threshold was found in the
retained local producer outputs or the inspected test-server paths. The
new rehearsal used source from `3d261ffe`, locked CPython 3.12.15 on macOS
arm64, and the approved Qwen files from the earlier run. It froze 24
generated rows in eight independent source groups. The seed
`00112233445566778899aabbccddeeff` and a 1,250-basis-point validation
fraction selected 21 training and three validation rows, balanced across
the three labels in each partition. The original three holdout prompts and
source IDs were absent from both. Neither the holdout nor the quality
policy changed between runs.

The training worker ran 24 optimizer steps, rank 8, a learning rate of
`0.0005`, a 96-token training limit, seed 42 and two threads. The parent
enforced a 6 GiB RSS ceiling and 240-second wall limit for training. The
preparation worker used the same RSS ceiling and a 120-second wall limit.
Both workers ran under kernel network denial. On replay, training took
11.387 seconds and peaked at 3,925,049,344 bytes RSS. Preparation took
11.632 seconds and peaked at 4,363,960,320 bytes RSS. The limit applies to
this native CPU fixture.

The signed-in Portal actor imported and published the same source through
the datasets service on PostgreSQL 18.6 with pgvector 0.8.7 and pinned
CPython 3.12.15. Both containers shared a network namespace with only
loopback. The test found the publication audit and read the two exact
partition files back through `VersionFile`. The publication's manifest
differed from the standalone worker's manifest even though the partitions
were byte-identical. The bounded replay used the authorized export's
version ID, tenant, workspace, manifest and partition bytes. The native
training result repeated the version and manifest identifiers. The native
preparation request bound that training job, attempt and adapter bytes.

Both runs produced baseline 1/3, candidate 3/3, and a passing gate under
the unchanged 8,000/0 policy. The two adapters, all nine model files
(1,208,042,441 bytes), normalized predictions and generated-output hashes
matched across runs. Request bindings and timing fields differed. The
independent Go decoder verified the original request-bound reports and
refused tampered verdicts. The Go tensor verifier confirmed the 56
changed target tensors and the merged file hash. The production command
test rejected promotion of a native verification request.

| Frozen replay evidence | SHA-256 |
| --- | --- |
| Generated source | `961ec26524c833881ded802656a20b753e855a686494cd0c9c9759eca78dd116` |
| Authorized version manifest | `134bedd3dd44e53bf1ed10f1eae0ac5638ebb3395218cc586604b8eb695a936d` |
| Train partition | `3eeeb12fe713677b1394fdd1aed5c52a721f2be74c52abd34feae63b4a5a8089` |
| Validation partition | `a35d91989ab8a6defa821375306c7dcaaefb1397eb8355d606395f3f6e307d05` |
| Original three-row holdout | `0fd36fecf2b79bfd5e23a09cf0c66d395304824d5ae18ca4be8d01a23f9b09af` |
| Protected training result | `42f0790680294637f8248a37a405cec920ee6d1d6c3b678248946b769781796f` |
| LoRA adapter tensors | `a46897f648b94c94a3fe55dff81c15a082e1a427126f958e3075a8feee2bf06b` |
| Merged BF16 weights | `07f5338cd5727efdd0e44ff64e8cf92b0ce6903cb9dd464c9763438c6c61e482` |
| Preparation result | `5b244e7c87060e0afab4c4dcd57387f46964d4e37506ad96827df82a0b5ecf47` |

The private evidence root retains the source generator, original holdout,
publication overlay, authorized manifest and partitions, protected worker
requests and reports, merged files and independent verification logs. The
replay copy of `run.sh` takes the absolute replay directory, locked Python
executable and approved model directory. Run it under the same kernel
network-denial profile in a fresh private scope. Use the retained
`compare.py` with CPython 3.12 and the evidence root to verify the existing
pair without retraining. `replay/preparation/report-path` points to the
unaltered report and adjacent model inventory.

The local hardware and the authorized test host do not provide a production
NVIDIA BF16 worker. The disposable database was removed after its export.
The native training and preparation requests exercised the protected
workers. The production training queue, physical stop, and Admin review
were outside this rehearsal. A production acceptance run must use the
authorized version and exact quality policy in a fresh NVIDIA attempt,
then retain the verified physical stop and audited Admin decision before
checking the derived model's lineage.

### Deployment admission for the accepted adapter

The [PEFT checkpoint format](https://huggingface.co/docs/peft/main/developer_guides/checkpoint)
stores adapter parameters separately from the base model.
[vLLM's documented LoRA path](https://docs.vllm.ai/en/stable/features/lora/)
loads an explicit adapter alongside the base. The existing ownsolid
deployment contract instead uses a ready model version. It can serve a
reviewed merged model with a catalog digest and a serving recipe. Using
the accepted training upload ID as a model version would bypass that
handoff. Serving an adapter alongside a base would require a separate
runtime and registry contract. Keep the current reviewed model path and
its existing deployment admission. This test adds no dependencies or
data. PEFT and vLLM use Apache-2.0. The approved Qwen revision, existing
locked dependencies and CC0 generated rows retain the terms above.

Use the same retained CPU output as the training experiment replay. After
the authenticated fixture stop, read the accepted experiment and the
scoped adapter source from the same job. Match the exact command, upload
ID, result digest and file inventory. Recheck the retained bytes. Confirm
the pinned base resolves in the registry, then try deploying the accepted
upload ID under the authenticated Admin. The deployment service must
return `RESOURCE_NOT_FOUND` twice with the same idempotency key and leave
the deployment, node-task and GPU-lease tables unchanged for the target.
This exercises the real PostgreSQL registry and deployment admission
without constructing a fake ready derived version. Fixture node facts
and the training production envelope remain synthetic, and the accepted
adapter bytes are the retained CPU worker's bytes.

A positive production handoff requires a fresh approved NVIDIA run,
authenticated physical stop, and an audited Admin review registering the
exact merged result. The deployment owner must then observe that version's
catalog digest and serving recipe on a capable node. Runtime image and
dependency lock provenance and observed hardware remain with their
owners. The native CPU comparison above remains separately reproducible
without those production receipts.

The acceptance subtest ran under PostgreSQL 18.6 in a Docker network
namespace with only loopback. The pinned base was present, and both
deployment requests returned the expected refusal without changing any of
the three target-node counts. To replay, set
`PROJECT_ONE_ACTUAL_TRAINING_OUTPUT` to the retained worker's absolute
output directory, `PROJECT_ONE_ACTUAL_DATASET_DIRECTORY` to the authorized
publication export, and `PROJECT_ONE_APPROVED_MODEL_DIRECTORY` to the
retained base configuration directory. From the repository root run
`bash services/control/internal/modules/training/testdata/replay_accepted_experiment.sh`.
The runner creates and removes its own PostgreSQL fixture.

## Model family and bounded version history contract (DEV-101 / MOD-103)

Reviewed 2026-11-13. This work follows the [remaining model version history
boundary](#remaining-model-version-history-boundary) and retained publication
graph above. [MLflow registry concepts](https://mlflow.org/docs/latest/ml/model-registry/)
separate a registered model from its versions and mutable aliases. Its
[version search API](https://mlflow.org/docs/latest/api_reference/rest-api.html#search-modelversions)
bounds results and continues with a page token. [Kubeflow Hub's registry](https://www.kubeflow.org/docs/components/hub/overview/)
also separates model, version and artifact metadata. These are design references;
neither product becomes an ownsolid dependency.

Keep ModelVersion.id and deployment model_id as the existing exact immutable
version ID. The ready-only ModelVersion and AdminModelDetail schemas keep
state=ready. The isolated v1 prototype defines separate historical metadata
shapes with ready/retired state and an explicit model-owner family UUID. Metadata
visibility, including ready state, never grants execution permission. Deployment
and training retain their current exact-version and artifact admission.

The proposed Admin history path selects an existing exact version ID and an
authorized workspace. The model owner resolves that version's established family
UUID and returns it with the exact anchor ID.
One page returns exact version ID, workspace, display name, model_key, kind,
catalog digest and creation time. Historical detail reuses the existing manifest
and dataset-lineage shapes and retained graph validation. It adds no provenance
store and does not scan physical model bytes while listing metadata.

Use created_at DESC, id DESC as the proposed stable keyset order, 1..100 rows
with the server's configured default, a required nullable next_cursor and optional
ready/retired filtering. Bind continuation to anchor version, family, workspace,
filter, current account/audience and scope. Each page rechecks current authority; it is not a
whole-history snapshot. No total count or implicit page aggregation is promised.
The model owner must provide the cursor codec, policy and complete response bound.

Reject grouping by display name, model_key, catalog digest or derivation parent.
Distinct immutable IDs remain distinct even when they share a digest. Derived
lineage does not imply family membership. The current public model has no ordinal
version number; this candidate does not invent one or reuse a resource revision.
No latest/name/alias selection replaces an exact deployment reference.

### Domain inputs before publication

The model owner must establish a durable family ID scoped to the installed tenant
and workspace, an explicit legacy mapping/backfill, and atomic membership rules
for first registration, reimport after retirement and derived publication.
Prefer immutable membership once established, with corrections owned and audited
by that domain. DEV cannot infer these identities from current metadata.
Adding an optional family field to an existing closed response would require
strict old-schema consumers to update. The chosen prototype instead uses the
new history response to return family identity and leaves all existing Admin,
Portal and list schemas unchanged. The new retained detail checks the family
and exact version together.

The owner must also provide current-authority retained ready/retired metadata
reads, complete cursor/response policy and actual error outcomes. DEV can then
finalize public OpenAPI, generated Go/Python and Help; the model owner retains
storage and domain authorization, the original route owner registers authenticated
dispatch, and Web retains S-202. The isolated prototype is
unregistered and changes no product API, SDK, model store or execution admission.

### Verification and licenses

Test two explicit families sharing a name/model_key, distinct IDs sharing a
digest, ready/retired rows, immutable parent/artifact bindings, empty and final
pages, tied-time keysets, cursor filter/scope refusal, live role/session revocation
and cross-workspace refusal. A permitted retired-metadata read must coexist with
refusal of a new deployment/training request for that exact retired ID. It must
never substitute a newer version. Invalid retained graphs release no partial
metadata or lineage. Generated wire tests must preserve exact UUIDs, null cursor,
filters and omitted policy defaults. Real PostgreSQL, original dispatcher and
S-202 qualification remain separate from prototype wire checks.

The isolated revision-2 prototype generated 52 Go/Python files with the
existing pinned oapi-codegen 2.8.2 and openapi-python-client 0.29.1 pipeline,
without changing product contracts or generated directories. Seven Python
HTTPX MockTransport tests and three Go in-process RoundTripper tests passed;
the Go cases also passed the race detector on Linux amd64. They preserve the
exact anchor/version/family UUIDs, required null continuation, filters, omitted
policy defaults and explicit zero/empty invalid inputs, and decode Problem
responses. Legacy ready detail/list shapes remain unchanged. These controlled
replies establish codec feasibility, not server authorization, persisted family
membership, keyset correctness or execution admission.

No dependency, model, dataset or third-party code is added. Existing pinned SDK
tool licenses and commercial, redistribution and offline notice requirements
remain those in [sdk.md](sdk.md#licenses). The primary references are read for
design only. No public operation or roadmap item is completed by this note.

## Durable family domain contract

Reviewed 2026-11-13 for the model-owner implementation. Follow
[ADR 0081](../decisions/0081-retain-explicit-model-family-membership.md).
The MLflow distinction between registry identities and immutable versions above
is useful; its name-based registration is deliberately not ownsolid's grouping
rule. Each legacy version gets its own random persisted family UUID, without
rewriting its version ID or deployment references. Default new imports, reimports
and derived publications also get separate families. Joining requires a current
Admin's explicit exact anchor in the same version-publication transaction.

Use PostgreSQL's [deferred scoped foreign keys](https://www.postgresql.org/docs/18/ddl-constraints.html)
for membership, its [BEFORE INSERT trigger](https://www.postgresql.org/docs/18/trigger-definition.html)
for default registration and a [transaction advisory lock](https://www.postgresql.org/docs/18/explicit-locking.html#ADVISORY-LOCKS)
over the exact target version for both default and explicit selection. The trigger
is part of the unnumbered candidate DDL. It activates only when the migration owner
admits that DDL; existing Go publication paths do not probe for optional tables or
silently skip membership. Explicit selection borrows the existing publication
transaction, rechecks Admin/session/workspace authority, records an existing audit
event and never commits on its own. Different concurrent choices fail rather than
reassigning membership. Deferred constraints refuse an orphan staged selection.

Backfill must not alter a table while its deferred foreign-key events are pending.
A real PostgreSQL minimal transaction reproduced that failure in the initial DDL.
Create the cyclic constraints before population, add the predecessor version's
required-membership FK as `NOT VALID`, populate both maps, then use named
[SET CONSTRAINTS IMMEDIATE](https://www.postgresql.org/docs/18/sql-set-constraints.html)
to finish this domain's checks before [validating the predecessor FK](https://www.postgresql.org/docs/18/sql-altertable.html).
Restore only these named constraints to deferred mode. No invalid FK is left after
admission, and unrelated owners' constraint modes are unchanged. Candidate downgrade
refuses after any family has been adopted; formal rollback must retain those UUIDs.
An isolated PostgreSQL transaction verified this ordering with an existing version,
the complete candidate DDL, a new default registration and forced constraint checks.
It observed two distinct families and rolled the entire prototype back successfully.

Retained detail checks the selected exact version and resolved family together
before decoding its catalogue or graph. A different family's damaged metadata must
still return `RESOURCE_NOT_FOUND` under the requested anchor. Add that negative
case alongside same-family ready/retired detail and unknown-version reads.

History resolves an exact anchor, uses the existing retained catalogue and lineage
checks, and orders by PostgreSQL [row comparison](https://www.postgresql.org/docs/18/functions-comparisons.html#ROW-WISE-COMPARISON)
on `(created_at,id)` descending. Fetch at most `limit+1` entries and validate the
whole selected page before publishing it. The configured `runtime.ui.page_size`
is the omitted limit; explicit limits are 1..100. Empty cursors and filters are
invalid. Ready/retired filtering changes navigation only. Each page takes current
actor/session and shared workspace fences; this is not a frozen history snapshot.

The v1 cursor is at most 512 ASCII bytes: canonical unpadded base64url JSON plus
an HMAC-SHA-256 signature with a model-history-specific domain separator. Reuse the
installed Identity HMAC key and [constant-time comparison](https://pkg.go.dev/crypto/hmac).
Bind its scope digest to account, session, auth epoch, audience, tenant, workspace,
local site/cell, family, exact anchor and state filter. Persist no cursor secrets or page results.
Key rotation invalidates old cursors and requires restarting navigation. Cursor
positions retain UTC microseconds and canonical nonzero UUIDs.

Wire ceilings are fixed protocol bounds: 4 KiB per metadata item, 512 KiB for a
complete 100-item page including continuation, and 128 KiB for retained detail.
The schema already bounds names to 100 characters, manifests to 16 KiB and retained
input snapshots to 64 KiB. Metadata pages omit manifests and lineage; detail uses
the existing manifest and dataset references. Check the complete encoded envelope
before returning it. These ceilings are not host resource or deployment defaults.

Acceptance requires real PostgreSQL legacy backfill, fresh/explicit registration,
retired reimport, derived publication, transaction/audit rollback, conflicting and
matching concurrent selections, exact tied-time pages and tampered/scope-swapped
cursors. Revoke the actor/session and disable or change workspace membership between
pages. Same-name/model-key families and same-digest distinct versions remain
separate. Retired metadata must coexist with exact-version execution refusal.
Missing or damaged retained graph data returns no partial page or detail. Unknown
commit outcomes suppress projections. No public API, root wiring or S-202 result
is claimed by these domain tests.

No dependency, model or dataset is added. Existing Go standard library and Google
UUID are BSD-3-Clause, pgx is MIT and PostgreSQL uses the PostgreSQL License, as
recorded in [the cleanup research](model-import-cleanup.md#mod-121-abandoned-upload-retention).
They permit commercial/offline use and redistribution with notices. The official
registry and SQL documentation are design references only.

### Service and wire policy handoff

The staged model-owner entry points are:

| Entry | Inputs and result |
| --- | --- |
| `StageVersionFamilyWithin` | Borrowed publication transaction, current authenticated Admin, requested workspace, exact new version ID, optional exact anchor, reason and server request ID; returns the persisted family UUID. An empty anchor selects a new independent family. The caller inserts the version and commits the whole reviewed/import publication, or suppresses the result on any failure. |
| `History` | Current authenticated Admin, requested workspace, exact anchor and typed optional limit/cursor/state; returns `workspace_id`, `family_id`, `anchor_version_id`, bounded `items` and required nullable `next_cursor`. |
| `HistoricalVersion` | Current authenticated Admin, requested workspace, exact anchor and exact member version; returns retained metadata, family and anchor IDs, the existing manifest and optional existing dataset lineage. |
| `VersionHistoryPolicy` | Configured UI page default, maximum limit 100, cursor 512 bytes, page envelope 512 KiB and detail envelope 128 KiB. Metadata items have the fixed 4 KiB ceiling described above. |

Every metadata item contains `id`, `workspace_id`, `name`, `model_key`, `kind`,
`catalog_digest`, `state` and UTC `created_at`. The new historical state vocabulary
is `ready`/`retired`; the stored `deleted` state and all existing DTOs remain intact.
The complete body bound includes the existing `{data: ...}` envelope. No ordinal,
count, latest alias, artifact-path authority or automatically aggregated history
is returned. Default and explicit registration both use the existing version insert;
the admitted DDL makes its membership atomic. Explicit selection must precede it.

New history is Admin-only and requires an active installation workspace. A platform
Admin's current authority spans that installation; removing an ordinary membership
does not demote that Admin. Session, account, Admin-role and workspace revocation
are checked again on every page. There is no new Portal history permission. Omitted
limit selects policy; explicit zero is invalid. Omitted filter selects both retained
states; explicit empty filter is invalid. Omitted cursor starts navigation; explicit
empty, stale-key, tampered or differently bound cursor is invalid. Limits may change
between pages without changing the bound anchor or filter. The current cursor has
no independent grant lifetime and remains subject to the live session and key.

Domain failures return a zero page/detail, never a partial `data` projection.
`INVALID_CURSOR` requires restarting from the exact anchor. `VALIDATION_FAILED`
rejects explicit invalid options. Existing `AUTH_SESSION_EXPIRED`, `ACCESS_DENIED`
and `RESOURCE_NOT_FOUND` retain current authority/scope meanings.
`MODEL_HISTORY_UNAVAILABLE` identifies an unadmitted backend/schema, unavailable
signing key or invalid installed pagination policy. `MODEL_FAMILY_CONFLICT` refuses
a different immutable membership choice. `MODEL_FAMILY_INVALID` and
`MODEL_HISTORY_INVALID` require repairing retained metadata from a verified source.
Existing `MODEL_FILE_INVALID` and `MODEL_DERIVED_CATALOG_INVALID` identify invalid
catalogue or retained lineage. Storage and typed transaction failures retain the
existing normalization and suppress projections, including unknown commit outcomes.

The independent DEV draft `6a2dd96fb392444de755db1cf5bfb771867064b0`
was checked against producer `2b4aaae5` and the unchanged domain sources.
Its `domain-draft/handoff.json` names exactly these five unmapped codes; none
requires a new domain error or DTO. Their existing stable meanings are:

| Domain code | Existing cause and continuation |
| --- | --- |
| `MODEL_HISTORY_UNAVAILABLE` | The backend or candidate schema is unavailable, or installed history pagination/signing prerequisites are invalid. Repair installation prerequisites before restarting history; never substitute an empty page or inferred family. |
| `MODEL_HISTORY_INVALID` | Retained metadata, the encoded envelope or an internally encoded cursor violates the domain's invariants. Repair the verified retained source or implementation; never release a partial projection. A malformed client cursor instead returns `INVALID_CURSOR`. |
| `MODEL_FAMILY_INVALID` | An exact retained version lacks a valid established family/root membership, or that binding is inconsistent. Restore the verified membership; never assign a new family during a read. |
| `MODEL_IDENTITY_REQUIRED` | The service lacks its Identity/Workspaces owners, or those owners do not share the same Identity instance. This is an installation composition failure, not an expired or unauthenticated caller. |
| `MODEL_STORAGE_MISMATCH` | The configured owners use inconsistent stores, or explicit publication receives a pool/wrong-dialect executor instead of the matching borrowed transaction. Repair composition; never retry against a guessed store. |

The three history/family codes survive `modelReadError`; the two setup failures
return from repository validation before `readActor` starts its transaction.
Explicit family selection also refuses an invalid borrowed executor directly.
All failed historical reads return the zero DTO, and failed selection returns no
family ID. The publication caller must also suppress a staged ID if its later
commit fails or has an unknown outcome. Existing current-authority checks and typed
transaction normalization remain separate. IAM must admit or normalize the five codes without confusing
composition failure with caller authentication or returning success metadata.

The draft's eight metadata fields, exact anchor/member selectors, flat retained
detail, omitted options and required nullable continuation match the domain.
This source/shape review adds no runtime result or public route. The draft's
unregistered OpenAPI and generated codec inputs remain DEV-owned and untouched;
the earlier PostgreSQL and native results retain their original source/platform.

These are module results, not newly registered HTTP Problems. DEV must admit the
public request/response and Problem schemas and generated clients. The original
route owner must bind the server-authenticated actor and resolved installation
scope, preserve the body ceilings and current request deadline, and run real
dispatcher acceptance. ARC must admit the unnumbered DDL, scoped role grants,
catalog checks, backup/restore and data-preserving downgrade policy. Web then
composes S-202 without changing legacy ready-only list/detail consumers.

### PostgreSQL family domain acceptance

On 2026-11-13, Linux amd64 source `95dc2891`, based on `8b17a295`, ran
against the schema 60 lineage, PostgreSQL 18.6 and pgvector 0.8.7 with
`--network none`. Backfill/lifecycle, bounded history/authority, retained derived
graph and verified derived registration roots passed. The selection transaction
root also passed explicit and default publication rollback, orphan commit refusal,
audit rollback and immutable membership controls. They exercised distinct IDs with
the same name and digest, unchanged deployment references, retired metadata with
exact-version execution refusal, tied timestamps, nullable continuation, invalid
options, current session/account/Admin-role/epoch/workspace refusal, cross-scope
refusal, damaged metadata/lineage and unknown-outcome projection suppression.

The two concurrent selections reached real advisory-lock contention and the correct
business outcomes. Their initial final assertion incorrectly included the second
publisher's separately audited login. Source `052a7b18` corrected that test-only
baseline, asserted the exact version's selection audit and passed both cases in
3.03 seconds: matching selection converged and conflicting selection returned
`MODEL_FAMILY_CONFLICT`, with one membership and one selection audit. Runtime and
candidate DDL sources were unchanged. The already-passed cases were retained.
The original cursor codec passed Linux's race detector, including all nine scope
dimensions, tampering, invalid payloads and key rotation; its sources remain unchanged.

Source `6abb7f16` then moved exact member selection ahead of retained metadata
decoding in detail reads. The affected history/detail subcase passed again in
1.53 seconds on a fresh disconnected PostgreSQL fixture. A damaged version in
another family and an unknown exact version both returned `RESOURCE_NOT_FOUND`
under the requested anchor; same-family retired detail and bounded navigation
still passed. This fourth fixture and its private inputs were also removed.

The initial DDL failure and its isolated corrected prototype remain preserved
separately from these passed cases. Each disposable PostgreSQL fixture, temporary
store and private credential file was removed; original logs, source and binaries
remain preserved. These are domain/SQL and file-registration results using synthetic
review metadata and small synthetic weights, not model quality, production approval,
public dispatcher, installed migration or S-202 acceptance. MOD-103 remains open.

The same model and preparation sources, rebased as `684f07ef` onto `db7ee24a`,
passed native `make check` and `make build`. `make test` stopped in its browser
prerequisite: Portal passed 409 cases, skipped 10 and failed the English offline
Help contract-download case at `web/packages/shell/testing/help-flows.ts:165`.
It expected `/help/api` but received `/help/api?q=changePassword`; the runner
cancelled the sibling Console suite. Web and OpenAPI files were byte-identical
to that upstream baseline. The trace and failed invocation are retained. Because
browser tests are prerequisites, this invocation did not reach the Go, Rust or
Web unit-test recipes. A focused replay of that unchanged Portal case passed in
2.4 seconds. A separate continuation then passed all previously unreached Go
modules, locked offline Rust tests and Web unit tests on the same runtime sources
(`ba90c3a4`, documentation
only since `684f07ef`). The original whole `make test` failure is retained; a
complete browser-suite pass and public dispatcher acceptance are still unclaimed.

After integrating the dataset-snapshot and training cell-fence changes from
`d0ab719a`, source `b4e222a7` again passed `make check` and `make build`.
The complete browser prerequisite reproduced the same Help URL assertion in
Simplified Chinese while the English case passed. Portal again reported 409
passed, 10 skipped and one failed, and Console was cancelled. The new baseline's
Web files were unchanged. A separate Control-wide `go test ./... -count=1` then
passed, including Models, model preparation, dataset and training tests. Reuse
the unchanged modules' earlier unit results and the scoped PostgreSQL evidence;
neither failed whole `make test` invocation is a pass. Keep this candidate
unpublished until the required whole-repository gate can pass.

The original Web owner subsequently published `44ab5229`, correcting the shared
Help assertion to observe the settled saved filter, input and content. Its two
Portal controls failed the former bare-URL assertion only after observing the
real saved query; all four corrected Console/Portal English/Chinese cases passed
without skip or retry. These are Web-owner results on its recorded source, not a
new MODEL whole gate. Integrate the published correction and current main before
M's next complete check/build/test. Preserve both earlier failed complete runs
and the original source/platform of the PostgreSQL and native unit evidence.
Formal schema/role/catalog admission, HTTP dispatch and S-202 remain separate.

### Corrected native full gate

Source `2783a46fa8990daa0792f11ee764c46a7795ef36`, tree
`fd479f8e54563061274ec898923d9b16dcdccc0a`, on base `f11aa11f`
passed complete `make check`, `make build` and `make test`, each with exit zero,
on Darwin arm64 with Go 1.27.3, Python 3.12, Node 26.13.0/pnpm 12.16.1 and
Rust 1.99.1. The original Makefile completed its browser prerequisite and every
Go, Rust and Web unit-test recipe. Console passed 1,191 cases with 28 existing
installation-dependent skips; Portal passed 410 with 10 such skips. All four
corrected English/Chinese Help cases passed inside these complete suites.
The retry configuration stayed zero and no skip or assertion was removed.

The single blocking kernel wait took 662.728 seconds; execution independently
took 511.522 seconds. The original permanent carrier and inherited FD were retained
through fresh resource checks and the complete gate. The short APFS scratch began
with 99.7 percent available, preserving the object-store threshold. Both entrances
were freshly built from this exact source; all 1,230 output files were hashed and
rechecked. The asset manifest SHA-256 is
`f42290cb1cc5298f017f97a2f86a1822e3611e9a6441c1396eafea8f87a4b6c5`.
The bounded parent result SHA-256 is
`0da3d19840ef3c06ccea36b80a510a8af0a53e803237c07789d9090ffbdb8f3a`.
The owned process group exited, FD closed, carrier identity and source remained
unchanged, scratch was detached and removed, and both preview ports were free.

Both earlier failed complete runs remain retained. This new gate qualifies its
recorded native source only; it does not relabel earlier PostgreSQL sources or
establish a new SQL, installed, hardware or S-202 result. The family DDL remains
an unnumbered candidate and the history reads remain unregistered. MOD-103 stays
open until ARC schema/role/catalog, IAM dispatch, DEV public contracts and Web
integration are actually admitted and verified.

### Formal lineage and restricted Control adaptation

Review input is ARC's unpublished `0f47a66bcb84c585b00bb1a92bf30f455017c1c9`,
not a released schema. It appends platform site/cell defaults and composite keys
to the reviewed domain SQL in migration 61; the original candidate inputs remain
unchanged. The domain's initial-version/member cycle stays deferred, membership
stays immutable, and populated downgrade still refuses before any object drops.
The added foreign keys ensure related rows agree; they do not restrict a read to
the server's local cell. New historical reads, explicit selection and retirement
must also enforce that local scope, including the existing retained derivation
and input rows. A matching snapshot must not authenticate a foreign-cell artifact.
Bind navigation to the resolved local site
and cell as well as the existing actor/anchor/filter dimensions. Neither identity
comes from request input. No public DTO changes are required.

Follow PostgreSQL's [composite and deferred constraints](https://www.postgresql.org/docs/18/ddl-constraints.html)
and [named constraint timing](https://www.postgresql.org/docs/18/sql-set-constraints.html).
Run normal family tests on the actual `Migrate` lineage, never apply testdata over
an already-migrated schema. Use the existing explicit predecessor-60 fixture for
backfill and migration rollback, retaining its exact prefix/version fence. A
pgx [nested transaction](https://pkg.go.dev/github.com/jackc/pgx/v5@v5.11.0#Tx)
provides a real savepoint inside an owned rollback test; normal upgrade calls
still use the real pool and exact catalog admission. Read the numbered downward
asset only for explicit downgrade refusal, not as a replacement migration path.

For service acceptance, create a fresh runtime through ARC's existing
`LoadRuntime`/`Prepare`/`Check` operations, then connect separately as that actual
Control login. Do not duplicate GRANT catalogs, use `SET ROLE` as a substitute
login, or expose credentials in diagnostics. Privileged fixture corruption uses
the retained operator pool explicitly; all business calls use the restricted
pool. ARC remains responsible for predecessor-principal grant resume and its OID,
authenticator, exact-catalog, DDL and journal fences. Consistently foreign
family/member/version records must not produce history or permit retirement;
mismatched scope must fail the real composite keys without partial state.

Prepare coverage for nonempty 60-to-61 backfill with unchanged exact IDs and
deployment references, local scope on all three rows, explicit/default publication
rollback, orphan/audit/concurrent outcomes, current-authority keysets, retired
read versus execution refusal, derived retained graphs and populated downgrade.
The untouched platform catalog pins currently describe 60: ARC must collect and
register actual 61 fingerprints before these positive tests can pass. Never relax
that check or manufacture a digest. No compile, SQL, native or full-gate result
is claimed by this source adaptation; the earlier 2783/dba acceptance stays exact.

No dependency, model or dataset is added. This uses the existing Go/UUID BSD-3-Clause,
pgx MIT and PostgreSQL licenses above. The pinned go-yaml 3.0.5 fixture serializer
retains its upstream [MIT and Apache notices](https://github.com/yaml/go-yaml/blob/v3.0.5/LICENSE).
All permit commercial/offline use and redistribution with their notices. Records
and credentials are synthetic and disposable; original migration/role ownership,
formal HTTP/API and S-202 qualification remain separate.

The owner adaptation retains seven selected SQL roots:

- `TestActualModelFamilyFormalBackfillAndLifecycle`: actual predecessor-60
  version/ledger fence, nested real-Migrate rollback, normal migration/replay,
  unchanged version rows and deployment references, scope consistency, retained
  retirement and exact populated-down refusal.
- `TestActualModelFamilyHistoryPagesAndAuthority`: existing bounded pages,
  filter/cursor, current-authority, cross-workspace, damaged metadata and
  unknown-outcome controls, now through the real restricted Control login.
- `TestActualModelFamilySelectionTransactions`: existing explicit/default
  rollback, orphan/audit failure, immutable triggers and real concurrent choices.
- `TestActualModelFamilyRetainedDerivedGraph` and
  `TestActualModelFamilyDerivedRegistration`: existing retained graph and verified
  file publication, with explicit operator-only corruption, foreign-cell artifact
  refusal/recovery and restricted DML.
- `TestActualModelFamilyRestrictedPrincipal`: journal SELECT, actual DDL,
  TRUNCATE, trigger and protected-head refusals, followed by retained retirement.
- `TestActualModelFamilyFormalCellScope`: both mismatched composite references
  and fully consistent foreign-site/foreign-cell graphs, no partial SQL state,
  refused history/selection/execution/retirement and subsequent local recovery.

The cursor unit root now prepares eleven binding dimensions, adding site/cell;
its earlier nine-dimension result remains evidence of the original source only.
This changes no wire fields or public DTO. The new fixture invokes ARC's public
role operations; it implements no grant table list or role-creation SQL. It owns
its random Control principal, private generated credential/configuration and
disposable schema. Login identity, strict runtime admission and all three local
scope projections are asserted before normal family cases. Both outer actor
construction and synthetic metadata remain test fixtures, not installed UI use.

For the joint native allocation, provide the existing private
`PROJECT_ONE_TEST_DSN_FILE` and `PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE`
references for the same disposable loopback `_test` PostgreSQL instance, plus
`PROJECT_ONE_REPOSITORY_ENGINE=postgresql` and the existing exact owned
`PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA`. The repository reference must be the
explicit URI accepted by the original RuntimeInput parser, including its actual
transport policy. Preserve the original setup limits or supply the original
protected setup-policy file; do not extend a process budget implicitly.
Derived registration also needs the existing
`PROJECT_ONE_REPOSITORY_MODEL_FILES_DIRECTORY` of approved pinned non-weight files.
No credential value belongs in a command or output. The owner root selection is
`^TestActualModelFamily`; compilation, these SQL cases and the eleven-dimension
cursor unit root remain unrun for this adaptation.

ARC must register actually observed 61 catalog fingerprints before these
positive migration/Control cases can run. Its formal empty-down, nonlocal
predecessor rollback and original-principal grant-resume tests remain mandatory
joint entries; MODEL does not replace them with a broader allowlist or new role
orchestrator. Retain the existing strict predecessor-60 inventory and every
original migration asset. Broader legacy inventory/import/cache/transfer cell
coverage remains with the corresponding open MOD-101/MOD-102/MOD-109 paths;
this slice changes only history, explicit family selection, retained-graph
validation, retirement and exact-version execution guards. Actual public routes and offline user
flows still require IAM/DEV/Web and their original qualification.

To replay, configure the private PostgreSQL DSN and fresh owned schema as described
by the [repository fixture](../../services/control/internal/testing/repositoryfixture/README.md).
Set `PROJECT_ONE_REPOSITORY_MODEL_FILES_DIRECTORY` to the pinned approved base's
non-weight files for the derived-registration fixture. The tests embed and apply
the unnumbered DDL only inside their disposable schemas.

```sh
go -C services/control test -p=1 -tags repository_integration ./internal/modules/models \
  -run '^TestActualModelFamily' -count=1 -v
```
