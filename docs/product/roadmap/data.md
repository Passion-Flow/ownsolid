# Data

Datasets for training and evaluation. Document parsing for retrieval is DeepParser's job,
not ownsolid's.

- Owns: `services/control/internal/modules/datasets`, `mongodatasets`, `runtimes/data-processing`.
- Depends on: network-and-storage, identity-and-access.

Reviewed 2026-11-09 against [Datasets streaming](https://huggingface.co/docs/datasets/main/stream), [DataTrove](https://github.com/huggingface/datatrove), [OpenLineage version facets](https://openlineage.io/docs/spec/facets/dataset-facets/version_facet/), and [dataset cards](https://huggingface.co/docs/hub/datasets-cards). The intake and transformation path remains streaming, and lineage binds published versions. Dataset cards identified a gap in version-scoped usage rights.

## P1 Enterprise release

- [ ] DAT-101 Dataset import from files and object storage: JSONL, JSON, CSV, Parquet (P1) - Catalog: F090
- [ ] DAT-102 Training formats: plain text, messages, instruction, preference pairs (P1) - Catalog: F091
- [ ] DAT-103 Validation, cleaning and filtering with before and after samples (P1) - Catalog: F092
- [ ] DAT-104 Exact and near-duplicate removal (P1) - Catalog: F093 - [Near-duplicate publication](../../research/dataset-near-duplicates.md)
- [ ] DAT-105 Train, validation and test splits with leakage checks (P1) - Catalog: F095
- [ ] DAT-106 Dataset versions and lineage to training runs and models (P1) - Catalog: F099 - [Training lineage and model handoff](../../research/training-dataset-lineage.md)
- [ ] DAT-107 Tokenizer preview: lengths, truncation and template rendering (P1) - Catalog: F098

## P2 Fleet operations

- [ ] DAT-201 Sensitive data detection and masking with usage scope (P2) - Catalog: F094, F580
- [ ] DAT-202 Labeling with AI suggestions, review and dispute resolution (P2) - Catalog: F096, F582
- [ ] DAT-203 Data quality gates and drift reports (P2) - Catalog: F581
- [ ] DAT-204 Database sources: read-only export from MongoDB and relational databases into dataset versions (P2) - Catalog: F264, F280
- [ ] DAT-205 Dataset cards for immutable versions: source attribution, declared licence and usage rights, and audited access to the governing metadata from training and evaluation (P2)

## P4 Breadth

- [ ] DAT-401 Synthetic data from teacher models with provenance (P4) - Catalog: F097
- [ ] DAT-402 Multimodal datasets (P4) - Catalog: F100

Catalog rows absorbed: F090 to F103, F580 to F582.
