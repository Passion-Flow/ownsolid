# Serving

Running models as services on GPUs and CPUs, for every model type.

- Owns: `services/agent/internal/inference`, `services/control/internal/modules/deployments`, `distribution`.
- Depends on: models-and-registry, scheduling-and-quota, gpu-operations.

Reviewed 2026-11-07 against GPUStack, KServe, Kubernetes Deployments and vLLM documentation ([research](../../research/model-services.md)). The review added SRV-112, SRV-113 and SRV-207: a deployment had no name, no history to roll back to, and no way to send part of its traffic to a new version.

## P1 Enterprise release

- [ ] SRV-101 Engine contract for deploy, health, generate, cancel, metrics and unload, with engine-specific settings passed through validated configuration (P1) - Catalog: F155
- [ ] SRV-102 vLLM serving for any imported chat model, with recipes sized from model and GPU (P1) - Catalog: F157
- [ ] SRV-103 Embedding and rerank serving (P1) - Catalog: F174
- [ ] SRV-104 Read-only root filesystem with all cache paths (Triton, TorchInductor, CUDA) mounted writable (found on real GPU, 2026-11-04) (P1)
- [ ] SRV-105 Multiple replicas with health-based routing and rolling updates (P1) - Catalog: F181
- [ ] SRV-106 Autoscaling on queue depth and latency, including scale to zero (P1) - Catalog: F181, F614
- [ ] SRV-107 LoRA adapter serving with hot load and unload (P1) - Catalog: F176, F613
- [ ] SRV-108 CPU serving path for small models when no GPU is present (P1) - Catalog: F160, F188
- [ ] SRV-109 Streaming, cancellation and timeouts end to end (P1) - Catalog: F077, F171
- [ ] SRV-110 Verified on real NVIDIA hardware: deploy, chat, stream, LoRA (RTX 3090 run on 2026-11-04 used the inherited code; repeat on ownsolid) (P1)
- [ ] SRV-111 Model services pages: the list, a deployment's own page and the deploy flow (S-203, S-204, S-205) (P1)
- [ ] SRV-112 Name and describe a deployment, unique in its workspace, shown wherever it is listed (P1)
- [ ] SRV-113 Deployment revision history with the difference between revisions and rollback to one (P1)

## P2 Fleet operations

- [ ] SRV-201 Cache-aware and KV-aware routing across replicas (P2) - Catalog: F618
- [ ] SRV-202 Prefill and decode disaggregation (P2) - Catalog: F606, F607
- [ ] SRV-203 KV cache tiering and safety domains (P2) - Catalog: F178, F608, F609
- [ ] SRV-204 SGLang engine (P2) - Catalog: F158
- [ ] SRV-205 MindIE and vLLM-Ascend engines (P2) - Catalog: F166, F167
- [ ] SRV-206 Multi-GPU and multi-node tensor and pipeline parallel serving (P2) - Catalog: F179
- [ ] SRV-207 Canary rollout: send a share of a deployment's traffic to a new revision, promote or roll back (P2)

## P3 Multi-site

- [ ] SRV-301 Cross-site serving failover (P3)

## P4 Breadth

- [ ] SRV-401 Speech recognition and speech synthesis serving (P4) - Catalog: F173
- [ ] SRV-402 Vision and image generation serving (P4) - Catalog: F173, F612
- [ ] SRV-403 Speculative decoding and inference-time compute options (P4) - Catalog: F180, F610, F611
- [ ] SRV-404 Batch inference jobs (P4) - Catalog: F621

Other engines (Transformers, Xinference, llama.cpp, Ollama, TensorRT-LLM, Triton, LMDeploy,
MLX, OpenVINO, ONNX Runtime, TGI, KServe, Ray Serve) are in the compatibility matrix.
Catalog rows absorbed: F077, F155 to F182, F188, F606 to F614, F618, F621.
