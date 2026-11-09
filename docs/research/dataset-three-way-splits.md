# Dataset train, validation, and test splits (DAT-105)

[GroupShuffleSplit](https://scikit-learn.org/stable/modules/generated/sklearn.model_selection.GroupShuffleSplit.html) sizes held-out partitions by group and accepts a reproducible random seed. [Hugging Face Datasets](https://huggingface.co/docs/datasets/package_reference/main_classes#datasets.DatasetDict) exposes named partitions. [Google's dataset split guidance](https://developers.google.com/machine-learning/crash-course/overfitting/dividing-datasets) reserves test data for final evaluation and identifies duplicates across partitions as a source of leakage. Build on the existing [exact and near-prompt grouping](dataset-near-duplicates.md) and its protected line plan.

Scikit-learn ([BSD-3-Clause](https://github.com/scikit-learn/scikit-learn/blob/main/COPYING)) and Datasets ([Apache-2.0](https://github.com/huggingface/datasets/blob/main/LICENSE)) are references only. The implementation uses the [Go standard library](https://go.dev/LICENSE) (BSD-3-Clause) and existing [CPython](https://docs.python.org/3/license.html) (PSF License). Tests use [PostgreSQL](https://www.postgresql.org/about/licence/), [pgvector](https://github.com/pgvector/pgvector/blob/master/LICENSE), [pgx](https://github.com/jackc/pgx/blob/master/LICENSE) (MIT), [google/uuid](https://github.com/google/uuid/blob/master/LICENSE) (BSD-3-Clause), and synthetic records. These licences permit commercial and offline use and redistribution under their notice terms. No dependency, model, or dataset is added to the release.

## Publication and compatibility

- Keep the two-way publication request, manifest schema 1, asset list, seed ranking and bytes. Select three-way publication through Go options. Freeze the test ratio in the existing job policy and bind it to the idempotency fingerprint. Both automatic attempts and explicit retries use the frozen ratio.
- Accept 100 to 5,000 basis points per held-out partition and a combined maximum of 9,000. Ratios target group counts. Round each requested count half-up, then reserve at least one group for every partition. Assign validation first, test next and training last in the seeded ordering. For very small datasets or large groups, actual row proportions can differ from the targets.
- Form one union of source IDs, exact duplicate edges and near-prompt edges before allocating any partition. Three-way publication uses the bounded near-prompt plan even when exact removal is selected. In that mode it retains near-matching rows while assigning them to the same partition. An explicit near-removal selection uses the DAT-104 safeguards. Fail with `DATASET_SPLIT_INSUFFICIENT_GROUPS` if fewer than three connected groups remain. Budget exhaustion fails closed.
- Use an independent SHA-256 ranking domain and manifest schema 2 with `source-union-sha256.v2`, `test_basis_points`, `test_groups`, `test_rows`, and the additional `test.jsonl` asset. Go reconstructs split assignments and validates all asset bytes before commit. Snapshot and restore verify both schemas, including the sealed test asset and its file-budget contribution. The training source reader continues to resolve only `train.jsonl` and `validation.jsonl`, and sees their exact counts and hashes.
- Splitting random rows would separate connected examples. Calling a two-way group splitter twice introduces an additional ranking and allocation contract. Adding an unversioned extra file would break older strict manifest readers. The three-way path keeps the existing bounded publication size and does not change streaming intake.

## Verification and DEV/Web handoff

The disconnected PostgreSQL and CPython fixture exercised the same-source group, a transitive near-prompt bridge, both deduplication choices, three nonempty partitions, and rejection when the groups merged below three. An expired attempt after a failed audit reproduced the exact manifest and partition bytes, and explicit retry kept the selected ratio and removal policy. The fixture restored the test asset and refused changed snapshot bytes. A separate comparison with the prior processor produced six byte-identical default artifacts. Contract tests reject mixed manifest versions and missing test assets.

The retained public read contract distinguishes manifest schemas 1 and 2 and
includes the sealed test asset. The Admin and Portal publication route uses
the same job operation and accepts an optional integer `test_basis_points`.
Omission retains the original schema 1 bytes and idempotency fingerprint.
An explicit value opts into schema 2 and must be 100 to 5,000 basis points.
The combined validation and test ratio cannot exceed 9,000. The existing
strict [Go JSON decoder](https://pkg.go.dev/encoding/json#Decoder.DisallowUnknownFields)
and dataset boundary reject null, duplicates, unknown keys and non-integer
values. A handler-local optional integer distinguishes omission from an
explicit zero before the internal option reaches its default sentinel.
Using a plain integer in the request would make an invalid explicit zero
silently select the original publication. A new endpoint would duplicate
authorization and idempotency behavior. No new dependency is introduced.

DEV owns the public write contract and generated clients. Publish them with
the handler and the existing split-capable worker. Test-set selection for
downstream evaluation and the S-503 dataset UI have separate owners.

On Linux amd64, the production HTTPS dispatcher and actual Python processor
passed against PostgreSQL 18.6 with pgvector 0.8.7 in a loopback-only network
namespace. The fixture used both Admin and Portal sessions. It verified
legacy default reads, strict input refusal, three nonempty partitions,
same-source isolation, idempotent replay and changed-ratio conflict. An
injected processor exit left a retryable job, and the real processor then
published all three partitions using the original ratio. Downloaded test
bytes, the response digest header and the retained manifest agreed. A
foreign workspace could not download the test asset, and insufficient
groups produced no version.

With the [PostgreSQL and CPython fixture](../development/environment.md)
configured, run the focused HTTPS and service tests:

```sh
go -C services/control test -tags dataset_processor_integration ./internal/modules/datasets -run '^(TestActualDatasetTLSContractsLargeUnicodeRowsCSRFExportAndReadiness|TestActualThreeWayPublication.*)$'
```
