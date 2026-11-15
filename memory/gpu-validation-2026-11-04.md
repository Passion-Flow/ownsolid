---
name: gpu-validation-2026-11-04
description: "Real-GPU results on the rented RTX 3090 for the inherited vLLM and LoRA training paths, plus product gaps they exposed"
metadata:
  node_type: memory
  type: project
  originSessionId: 5b180534-1ea7-45f4-8f52-3433974e0450
  modified: 2026-11-03T19:45:39.650Z
---

On 2026-11-04 the inherited project-one model paths were run on the rented RTX 3090 (Ubuntu 24.04, driver 580, CUDA 13). Server artifacts sit under `~/ownsolid-dev` with containers prefixed `ownsolid-check-`.

**What worked:**
- Pinned vLLM 0.31.0 image, run with the agent's exact hardening (read-only rootfs, cap-drop ALL, read-only model mount, loopback port): ready in 56 s; chat and streaming work; Qwen3-0.6B at concurrency 4 gives about 187 tokens/s aggregate.
- Training engine with the exact CUDA lock, fully offline (`--network none`): 30 LoRA steps in about 9 s, 3 sealed checkpoints, a 4.5 MB adapter, base parameters frozen.
- The adapter served through vLLM `--enable-lora` scored 4/4 on held-out tickets, against 0/4 for the base model.

**Gaps for the migration:**
1. The agent's inference bridge network does not block egress; containers reached PyPI. The air-gap claim needs real egress denial, for example DOCKER-USER rules or an internal network with a proxy.
2. The base serving recipe omits `enable_thinking=false`, so chat replies start with `<think>`. Default chat template kwargs must be set per model.
3. A read-only rootfs needs TRITON_CACHE_DIR, TORCHINDUCTOR_CACHE_DIR and the other cache variables pointed at the cache mount, otherwise LoRA kernels fail.
4. In China, Hugging Face and Docker Hub are blocked. hf-mirror, ModelScope and registry mirrors work, and BuildKit hung on metadata through mirrors (pre-pull plus the classic builder worked). This supports offline bundles over online pulls.

**Why:** These were the first real-hardware runs of the inherited code. **How to apply:** Carry the fixes into the ownsolid inference and training modules, and keep this demo loop (train offline, deploy the adapter, compare) as a showcase. See [[dev-environment]] and [[positioning-research]].
