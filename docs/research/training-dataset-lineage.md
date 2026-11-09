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
