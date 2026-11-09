# EVL-105 serving benchmark evidence

The [vLLM benchmark guide](https://docs.vllm.ai/en/v0.31.0/benchmarking/cli/)
separates end-to-end request latency, time to first token, inter-token
latency and output throughput. Record the actual client measurement
points, workload and concurrency with every profile. Store individual
request observations so percentiles can be recalculated, and pin the
model revision, serving recipe, artifact digests, device and rate inputs.
[OpenTelemetry HTTP metrics](https://opentelemetry.io/docs/specs/semconv/http/http-metrics/)
describe server request duration as a separate signal from the client
measurement. The benchmark design references vLLM under
[Apache-2.0](https://github.com/vllm-project/vllm/blob/v0.31.0/LICENSE).
This evidence check uses the existing CPython standard library under the
[PSF license](https://docs.python.org/3/license.html) and repository-authored
synthetic fixture data. It adds no dependency, dataset or model artifact.

## Retained gateway transport baseline

[GW-112](https://github.com/Passion-Flow/ownsolid/commit/504161e1)
records a real Rust gateway, Go HTTPS admission and PostgreSQL 18.6
with pgvector 0.8.7 on loopback. The HTTP inference and tokenizer were
synthetic. The client measured nonstreaming latency through bounded
response body completion. The fixture used the nearest-rank p50, p95
and p99 of successful measured responses. Direct and gateway each used
12 warm-up and 96 measured calls at each concurrency. The test asserted
durable, unheld success receipts for all 216 gateway calls.

An independent read of the retained aggregate log confirmed zero
refusals, equality of offered and completed rates, ordered percentiles
and the arithmetic for each gateway-minus-direct percentile delta.
The archived overlay is the source for the receipt assertions and
measurement points. The aggregate log contains the summary values.
Per-request samples are required to recalculate the percentiles.

| Concurrency | Gateway req/s | Gateway p50 / p95 / p99 in ms |
| ---: | ---: | ---: |
| 1 | 70.3 | 13.61 / 18.90 / 29.14 |
| 2 | 80.3 | 23.83 / 32.18 / 38.65 |

| Retained file | SHA-256 |
| --- | --- |
| Profile log | `2f23d100b9620d1205954bf0073c8d68972dad79a8ee9af1103c3a529563b039` |
| Fixture Go overlay | `681046c3be86202f6208b0662b8c3423c2b59846e963847cedb1cec9e3819f8f` |

Keep this result scoped to transport and admission. To accept EVL-105,
run a representative licensed model through the authorized serving
path on identified hardware with a pinned workload. Retain per-request
timings, observed token counts and signed usage, and supply an explicit
rate basis for any cost result. A customer-provided rate and measured
usage are distinct inputs. The current Linux test host has no GPU,
and the retained native CPU preparation trial measures a different
workload.

## Retained native CPU generation windows

The [qualified preparation captures](evaluation-reproducibility.md#qualified-cpu-comparison-at-the-same-policy)
contain six per-model observations across two native macOS arm64 CPU
runs of the same three CC0 holdout cases. Both use the pinned
Qwen3-0.6B base, the derived model, float32 computation, seed 42 and
two threads. A disconnected readback independently verified both
report digests and matched case identities, prompts, expected labels
and generated output digests across the two runs.

The [worker timing loop](../../runtimes/model-lifecycle/projectone_model_lifecycle/engine.py)
starts after prompt tokenization and measures tensor setup, serial
generation and output decoding. Each recorded continuation has two
tokens. The rate divides the six continuation tokens by the sum of
the three measured intervals for that model and run.

| Capture | Model | Per-case windows in ms | Median ms | Tokens per measured second |
| --- | --- | --- | ---: | ---: |
| Initial | Base | 140, 89, 88 | 89 | 18.93 |
| Initial | Derived | 241, 114, 345 | 241 | 8.57 |
| Authorized publication replay | Base | 208, 97, 98 | 98 | 14.89 |
| Authorized publication replay | Derived | 104, 133, 110 | 110 | 17.29 |

The same output digests accompanied substantial timing variation.
These retained windows cover one tiny serial preparation workload.
Representative gateway latency, sustained throughput, device
comparison and cost remain open.

## Disconnected direct CPU serving

The [vLLM 0.29.0 serving guide](https://docs.vllm.ai/en/v0.29.0/serving/online_serving/)
and [benchmark CLI](https://docs.vllm.ai/en/v0.29.0/benchmarking/cli/)
separate the chat endpoint, completed throughput and client latency. Use a
bounded standard-library client to retain every observed time and token count.
The [CPU setup](https://docs.vllm.ai/en/v0.29.0/getting_started/installation/cpu/)
provides OpenMP and KV-cache controls. The existing Qwen model and vLLM
image are Apache-2.0. The image also supplies PyTorch under BSD-3-Clause,
Transformers, tokenizers and safetensors under Apache-2.0, and the packaged
Triton `3.7.0+git270e696d` under MIT. The existing
[model and runtime evidence](evaluation-reproducibility.md#native-cpu-adversarial-qualification)
records the pinned artifacts and notices. These licenses permit commercial
and offline use and redistribution subject to their notice terms. The test
reuses the cached image and original synthetic prompts. It ships no artifact.

Linux amd64 used an Intel Xeon Platinum 8168, the pinned nine-file
Qwen3-0.6B revision `c1899de289a04d12100db370d81485cdf75e47ca`, and CPU wheel
`vllm 0.29.0+cpu` with PyTorch `2.13.0+cpu` and Transformers `5.16.1`.
The read-only container had network mode `none`, no published ports, a
four-CPU quota, four OpenMP threads, 8 GiB memory and a 1 GiB KV cache.
The engine used float32,
eager execution, a 256-token context, a maximum of two active sequences,
seed 42 and disabled prefix caching. NUMA placement used the container's
default kernel policy after the explicit binding request was refused.

Three initial starts produced no observations. The first tried to write
Triton's default cache on the read-only root. The second wrote into a
`noexec` temporary mount. The third loaded the compiled launcher from an
executable cache, then failed to load a separate temporary shared object.
Inspection of the image's CPU driver confirmed it uses Python's temporary
file directory for that object. An isolated probe loaded a shared object
under the final settings. The passing run set both `TMPDIR` and
`TRITON_CACHE_DIR` within one dedicated 256 MiB executable tmpfs with
`nosuid,nodev`. The root stayed read-only and the general `/tmp` stayed
`noexec`.

A direct loopback client sent two warm-up calls, then the same eight
nonstreaming prompts at each concurrency. Requests used greedy decoding,
seed 42, thinking disabled and at most 24 completion tokens. The engine
reported 20 to 21 prompt tokens per measured request. All 18 requests
returned a well-formed completion and internally consistent token usage.
The archived observations record the request and output digests and the
monotonic interval through the full response body. An independent readback
recomputed the nearest-rank percentiles and checked peak overlap. The
API startup log uses `0.29.0`; the independent verifier checked the
CPU wheel's full version with its `+cpu` suffix.

| Concurrency | Completed / measured | P50 / P95 latency in ms | Requests/s | Output tokens/s |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 8 / 8 | 3344.077 / 3483.805 | 0.313 | 6.999 |
| 2 | 8 / 8 | 3568.120 / 3712.689 | 0.560 | 12.534 |

Both groups returned 179 output tokens. The eight paired output digests
matched. The raw observation SHA-256 is
`ceace0a1253b196142fa739cf8766f930f85086dfe9ed06a0acf704a1d8911bc`.
With eight measured calls per group, P95 is the longest observed response.
This run covers direct HTTP on one shared CPU host. Authorized gateway
admission, signed usage, GPU measurements and rates for cost remain open.
