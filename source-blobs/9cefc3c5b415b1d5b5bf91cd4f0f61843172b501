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
