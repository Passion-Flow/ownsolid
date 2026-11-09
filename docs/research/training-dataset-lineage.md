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
