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

### SRV-115 measurement freshness and integer headroom

The node stores when Control receives a hardware report. The report also
contains the agent's observation time and a separate time for each GPU.
A current heartbeat can carry old observations. A placement or new load
must have recent receipt and observation times. Use the already validated
node online window for both age and tolerated clock difference. A report
or GPU observation outside that window cannot establish available memory.
Keep this check in deployment admission and placement so existing gateway
routing and training's borrowed hardware read retain their behavior.

Retain the [approved Agent recipe](../../api/agent/v1/inference-recipe.json)
and its minimum total capacity. Before loading, use the ceiling of the
existing 80% requirement in whole MiB. For example, at 8,193 MiB total
the mathematical requirement is 6,554.4 MiB and the minimum report that
can satisfy it is 6,555 MiB. The previous integer truncation accepted
6,554 MiB. A derived load must also meet its existing absolute free
memory floor. This server-side check cannot lower either engine floor.
There are no new dependencies or data.

Verify that placement and creation both refuse a current receipt with an
old report, an old individual GPU sample, a sample too far in the future,
and a device one MiB below the ceiling. Exercise the exact threshold on
the same isolated installation. Do not infer an empirically qualified
runtime margin from these boundary checks. The full breakdown and
hardware qualification above remain open.

#### Offline verification

`TestRealTLSPlacementAndCreationRequireFreshCapacity` ran over Admin
HTTPS and real PostgreSQL 18.6 in an isolated Linux arm64 namespace.
The node protocol fixture sent current receipts with a stale report, a
stale GPU observation, and a GPU timestamp outside the future clock
window. Each refused before reserving a deployment. With a fresh
observation of an 8,193 MiB GPU, both routes refused 6,554 MiB free
and admitted exactly 6,555 MiB. The existing placement contract and
the actual pinned-model license admission also passed in the same
disconnected run. Hardware and generation were protocol fixtures.

The remaining public breakdown needs an additive contract update for
`PlacementGPU` and coordinated Admin consumers. The full claim needs
a measured reserve and full-context execution with the approved
engine image on a supported GPU with egress blocked.
