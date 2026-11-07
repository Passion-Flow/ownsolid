# Training and fine-tuning

Fully offline training jobs, from small LoRA fine-tunes to large distributed runs.

- Owns: `services/control/internal/modules/training`, `services/agent/internal/training`, `runtimes/training`.
- Depends on: data, models-and-registry, scheduling-and-quota.

## P1 Enterprise release

- [ ] TRN-101 One training job contract for UI, CLI and API with logs, cancel, resume and artifacts (P1) - Catalog: F105, F119
- [ ] TRN-102 LoRA and QLoRA fine-tuning for any imported causal model, not only Qwen3 (P1) - Catalog: F111
- [ ] TRN-103 Full-parameter supervised fine-tuning on one node (P1) - Catalog: F109, F110
- [ ] TRN-104 Checkpoints with integrity seals and resume after failure (P1) - Catalog: F587
- [ ] TRN-105 Experiment records: base model, data version, hyperparameters, seed, lockfile, image digest, hardware (P1) - Catalog: F106
- [ ] TRN-106 Live training metrics and comparison between runs (P1) - Catalog: F120
- [ ] TRN-107 Train, then deploy the result in one flow, with before and after comparison (P1)
- [ ] TRN-108 Isolated runtime images per framework, built offline with pinned locks (P1) - Catalog: F121
- [ ] TRN-109 Notebook and remote development environments separated from production jobs (P1) - Catalog: F593
- [ ] TRN-110 Verified on real NVIDIA hardware: offline LoRA run, adapter served (RTX 3090 run on 2026-11-04 used the inherited code; repeat on ownsolid) (P1)
- [ ] TRN-111 Training compute record, part of the experiment record and built after TRN-105: tokens processed, operations by 6NT with a hardware ceiling, lineage and spent totals, declared base compute and configured reference values ([research](../../research/training-compute-record.md)) (P1)

## P2 Fleet operations

- [ ] TRN-201 Distributed training: DDP, FSDP2 and DeepSpeed across nodes (P2) - Catalog: F118, F125, F130
- [ ] TRN-202 Elastic training with automatic restart on node failure (P2) - Catalog: F589
- [ ] TRN-203 Budgeted hyperparameter search (P2) - Catalog: F591
- [ ] TRN-204 Distributed topology preflight before launch (P2) - Catalog: F588
- [ ] TRN-205 Training to quantized deployment consistency check (P2) - Catalog: F592

## P4 Breadth

- [ ] TRN-401 Preference optimization: DPO, reward models, PPO, GRPO (P4) - Catalog: F113, F114, F115
- [ ] TRN-402 Continued pretraining and pretraining from scratch (P4) - Catalog: F108, F122
- [ ] TRN-403 Multimodal, embedding, reranker and diffusion fine-tuning (P4) - Catalog: F116, F117, F124
- [ ] TRN-404 Agent reinforcement learning with multi-role resources (P4) - Catalog: F123, F590

Framework adapters (LlamaFactory, ms-swift, Axolotl, Unsloth, Megatron, verl, OpenRLHF,
TRL, Accelerate, Sentence Transformers, Diffusers) are in the compatibility matrix.
Catalog rows absorbed: F104 to F139, F587 to F594.
