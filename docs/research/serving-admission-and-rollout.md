# Serving admission and safe rollout

## Conservative admission

The existing placement preview and create path both use the approved recipe.
It requires an 8 GiB GPU and about 80% of its memory free before load. The
agent pins vLLM 0.31.0 with a 75% per-instance GPU allocation target, a
2,048-token context, and up to four simultaneous sequences. The difference
between the free-memory check and the allocation target reserves some room
for changes between the observation and runtime startup. The observation
still needs to be fresh and the agent must probe again before allocating.

The [vLLM arguments](https://docs.vllm.ai/en/v0.31.0/configuration/engine_args/)
separate context, sequences, cache dtype and device memory utilization. The
[NVIDIA Triton Model Analyzer](https://docs.nvidia.com/deeplearning/triton-inference-server/user-guide/docs/user_guide/model_analyzer.html)
measures GPU memory under different instance and batching configurations.
Use verified model data for an explainable lower bound, then qualify a
conservative runtime margin on the pinned engine and hardware. Unqualified
combinations cannot receive a positive fit result.

For the [approved Qwen3-0.6B revision](https://huggingface.co/Qwen/Qwen3-0.6B/blob/c1899de289a04d12100db370d81485cdf75e47ca/config.json),
the verified weights file is 1,503,300,328 bytes. For conventional grouped-query
attention at two bytes per cache element, four full contexts would consume
`2 × 28 layers × 8 KV heads × 128 dimensions × 2 bytes × 2048 tokens × 4 sequences`,
or 896 MiB of raw KV data. Runtime allocations, block rounding, workspace,
activation and kernel overhead add to it. Use an engine-specific model for
other attention layouts. Parameter count times bytes per parameter alone
omits both those allocations and the cache.

The preview and admission must compute the same minimum and expose the input
revision, dtype, context, concurrency, margin and observed free memory.
Test below and at the threshold, stale and missing observations, unsupported
architectures, arithmetic bounds and races with the runtime's new probe.
Before a positive claim, measure peak memory and complete a full-context run
on supported hardware with registry egress blocked. The S-203 screen needs
that breakdown through a shared contract (SRV-115).

## Rollout and rollback

[Kubernetes rolling updates](https://kubernetes.io/docs/tasks/run-application/update-deployment-rolling/)
separate surge capacity from allowed unavailability.
[EndpointSlice conditions](https://kubernetes.io/docs/concepts/services-networking/endpoint-slices/)
distinguish an endpoint that can finish existing traffic from a ready target
for new traffic. [KServe canaries](https://kserve.github.io/website/docs/model-serving/predictive-inference/rollout-strategies/canary)
give old and candidate revisions distinct traffic shares.

For SRV-105 and SRV-113, reserve candidate capacity without releasing the
serving revision. Warm the candidate from a verified local model and tokenizer,
verify its generation and keep it unroutable until ready. Switch new traffic
using a generation-checked route update. Keep the old endpoint available to
existing streams until they drain and leave its recorded revision available
for rollback. If a candidate fails before promotion, the current route and
streams continue. After promotion, route new requests back only when a
previous target is verified. If no spare capacity exists, refuse a
zero-interruption update and report what capacity it needs. An in-place
restart would interrupt existing requests before there is a verified fallback.

Acceptance needs real concurrent streams across a successful cutover and a
failed candidate, a stale health report, duplicate controller actions, a
controller restart, and loss of an acknowledgement after the route changes.
No previous revision or active lease may be released before the route and
drain state agree. SRV-207 extends this to configurable traffic shares.

## License and ownership

No dependencies or data are added by this review. The existing vLLM project
and the cited Qwen model are Apache-2.0, with commercial and offline use and
redistribution subject to their license and notice terms. The engine image
includes other software with separate terms and needs the delivery
inventory required by DEL-110. Triton, Kubernetes and KServe are design
references here. Agent runtime and gateway route/drain behavior are outside
lane M. The current deployment schema owns one retained deployment per node
and exposes no revision rollback or replica routing contract.
