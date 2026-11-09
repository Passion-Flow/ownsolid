# Models and registry

Module reviewed: 2026-11-09. See [model registry research](../../research/model-registry.md).

Model assets: import, versions, lineage, conversion and retirement, for every model type.

- Owns: `services/control/internal/modules/models`, `modelpreparations`, `runtimes/model-lifecycle`.
- Depends on: delivery-and-upgrades (offline media), network-and-storage.

## P1 Enterprise release

- [ ] MOD-101 Import any Hugging Face format model from local media or an internal mirror, with checksums and resumable upload (P1) - Catalog: F062
- [ ] MOD-102 Model catalog with type (chat, embedding, rerank, vision, speech, image), size, context, license and capabilities (P1) - Catalog: F063
- [ ] MOD-103 Versions, artifacts and lineage: weights, adapters, tokenizer, template, training run, evaluation (P1) - Catalog: F064, F099
- [ ] MOD-104 Per-model serving defaults such as chat-template options (for example disabling thinking mode) (P1)
- [ ] MOD-105 LoRA merge and format conversion with recorded inputs and outputs (P1) - Catalog: F065
- [ ] MOD-106 Quantization jobs producing serving variants (P1) - Catalog: F066, F175
- [ ] MOD-107 Model retirement with dependency check across deployments, apps and keys (P1) - Catalog: F585
- [ ] MOD-108 Model file and code review: scan for executable code and unsafe formats before import (P1) - Catalog: F583
- [ ] MOD-109 Model distribution to nodes through the in-site registry with pre-warm (P1) - Catalog: F642
- [ ] MOD-110 Model licence register: licence, commercial use, redistribution, attribution and use restrictions per model and version; deployments and task runs are refused with the reason when the installation's declared use is not permitted (P1) - [Deployment admission and consumer handoff](../../research/model-license-admission.md)

- [ ] MOD-113 Catalogue index: names, publishers, parameter counts, context lengths, licences, tags and hashes for both registries, shipped with the product and refreshed from a signed bundle ([ADR 0036](../../decisions/0036-show-every-option-and-fetch-models-offline.md)) (P1)
- [ ] MOD-114 Search and filter the catalogue with the network disconnected, covering Hugging Face and ModelScope in one list (P1)
- [ ] MOD-115 Fetch manifest: pick models, export exact repositories, revisions and expected hashes; `ownsolid fetch` turns one on a connected machine into one bundle (P1)
- [ ] MOD-116 Import a bundle, verifying every model against the hash its registry published before anything is written, and stage tokenizer files so no engine reaches a registry at serving time (P1)
- [ ] MOD-117 Relay mode: a mirror in a DMZ that the cluster reaches instead of the internet, with the same verification (P2)
- [ ] MOD-118 Direct download for development and for deployments that allow outbound traffic, with the same verification as a bundle and off unless configured (P2)
- [x] MOD-119 Model library page: your models, importing a bundle with resumable upload and verification shown, each model's own page and retiring it (S-201, S-202) (P1) - Evidence: a2cb8f4, 9bc583a, d296b9f; real Chromium imported, interrupted, resumed and retired the real Qwen3-0.6B on lane B's installation on `ownsolid-cpu`, 2026-11-07
- [x] MOD-120 Remove a finished import's upload and failed stages from the model store once its model is published, crash-safe, so a model takes its own size (P1) - Evidence: e81959df, bbf09930, [disconnected Linux amd64 acceptance](../../research/model-import-cleanup.md#linux-amd64-acceptance)

- [ ] MOD-121 Expire abandoned unfinished uploads under a configurable retention policy, report their expiry to operators and recover their staging space without touching active uploads or published models (P1)

## P2 Fleet operations

- [ ] MOD-201 Peer-to-peer model distribution across thousands of nodes with rack seeding (P2) - Catalog: F642
- [ ] MOD-202 Model and application bundle publishing (P2) - Catalog: F584
- [ ] MOD-203 Retention and backup policy for checkpoints, adapters and reports (P2) - Catalog: F068
- [ ] MOD-204 Compatibility declarations: engine, hardware, precision per model version (P2) - Catalog: F069

## P4 Breadth

- [ ] MOD-401 Distillation and small domain model recipes (P4) - Catalog: F140, F141, F143, F144
- [ ] MOD-402 Edge model release and quality regression (P4) - Catalog: F142, F586
- [ ] MOD-403 NVIDIA Model Optimizer and other compression toolkits (P4) - Catalog: F070

Catalog rows absorbed: F062 to F070, F099, F140 to F144, F175, F583 to F586, F642.
