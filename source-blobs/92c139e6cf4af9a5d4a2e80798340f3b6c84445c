# Evaluation

Module reviewed on 2026-11-09 against MLflow, lm-evaluation-harness, PyTorch and
vLLM. See [evaluation sets and reproducibility](../../research/evaluation-reproducibility.md).

Measuring models and applications before release, and gating releases on the result.

- Owns: `runtimes/model-lifecycle` evaluation, `services/control` evaluation APIs.
- Depends on: models-and-registry, data, serving.

## P1 Enterprise release

- [ ] EVL-101 Evaluation sets with versions, scorers and reports (P1) - Catalog: F148
  - [Offline replay](../../../runtimes/model-lifecycle/README.md#replay-a-captured-evaluation)
    binds a published validation partition to an existing base-model capture and
    re-scores it. Disconnected replay against a PostgreSQL-published fixture
    reproduced the report from synthetic captures. The authorized product
    workflow and UI remain open.
  - [Persisted report integrity](../../research/evaluation-reproducibility.md#persisted-report-integrity-on-a-real-cpu-capture)
    reproduced a report from real CPU responses and refused changed outcomes.
    Repeatability evidence covers the fixed CPU safety profile.
  - [Authorized report service handoff](../../research/evaluation-reproducibility.md#authorized-report-service-handoff)
    identifies existing scoped readers and the missing authenticated report
    operation, immutable persistence and current-authority publication.
- [ ] EVL-102 Model-against-model comparison on the customer's own set (P1) - Catalog: F067
  - [Offline paired comparison](../../../runtimes/model-lifecycle/README.md#compare-two-captured-evaluations)
    replays two retained reports on one frozen validation version and records
    per-case differences. Authorized delivery, UI and real engine evidence
    remain open.
- [ ] EVL-103 Release gates: a model or app version cannot be promoted below its threshold (P1) - Catalog: F650
  - [Retained preparation replay](../../research/evaluation-reproducibility.md#evl-103-retained-preparation-quality-decision)
    confirms the real CPU refusal, two native CPU passes and both gate
    outcomes on frozen controls. Authorized production release and UI
    acceptance remain open.
- [ ] EVL-104 Production traces turned into regression cases (P1)
  - [Capture and publication boundary](../../research/evaluation-trace-regression.md)
    records which inputs an authorized trace needs to become an immutable
    regression case. The product capture and review flow remains open.
  - [Retained trace qualification](../../research/evaluation-trace-regression.md#retained-native-trace-qualification)
    verifies scoped conversion and immutable publication of three reviewed
    cases from a real CPU preparation trace in a disconnected fixture.
- [ ] EVL-105 Performance benchmark: latency, throughput and cost per model and hardware (P1) - Catalog: F150
  - [Transport baseline](../../research/evaluation-performance-benchmark.md)
    independently checks the retained gateway summary. Representative engine,
    hardware and cost acceptance remain open.
  - [Native CPU generation windows](../../research/evaluation-performance-benchmark.md#retained-native-cpu-generation-windows)
    independently recompute the per-case timings and continuation token rates
    from two real CPU preparation captures.
  - [Disconnected direct CPU serving](../../research/evaluation-performance-benchmark.md#disconnected-direct-cpu-serving)
    recalculated 16 real vLLM HTTP observations at two concurrency levels.
    Authorized gateway usage, GPU and cost acceptance remain open.
  - [Gateway request and receipt compatibility](../../research/evaluation-performance-benchmark.md#gateway-request-and-receipt-compatibility)
    verifies two retained inputs against the gateway parser and records the
    conditions for joining real serving results to an authorized receipt.
  - [Authorized CPU gateway pair](../../research/evaluation-performance-benchmark.md#authorized-cpu-gateway-pair)
    matched two real model outputs, token counts and scoped acknowledged
    receipts. UI, GPU, detached signing and cost acceptance remain open.
- [ ] EVL-106 Safety and regression sets: injection, leakage, unsupported answers (P1) - Catalog: F149
  - [Deterministic safety replay](../../../runtimes/model-lifecycle/README.md#replay-controlled-safety-cases)
    scores controlled outputs on a frozen synthetic fixture for injection,
    private context disclosure and unsupported requests. Real model safety,
    authorized capture delivery and UI remain open.
  - [Native CPU adversarial qualification](../../research/evaluation-reproducibility.md#native-cpu-adversarial-qualification)
    published the frozen validation group and repeated three real responses
    byte for byte. All three were unverified and the originals were retained.
  - [Same-input refusal control](../../research/evaluation-reproducibility.md#exact-refusal-control-on-the-same-published-inputs)
    produced three observed exact refusals from controlled marker text. The
    three original native responses remain unverified.

EVL-101 records the immutable dataset, scorer and model revisions, seed and execution
profile. Re-scoring the same captured outputs must give the same scores. EVL-102
pairs cases by dataset revision and case identity. Fresh inference is repeatable
only on a tested execution profile that pins the engine, hardware, precision and
generation settings. A report identifies any profile where repeatability is unverified.

## P2 Fleet operations

- [ ] EVL-201 Agent and workflow task evaluation with traces (P2) - Catalog: F145, F600
- [ ] EVL-202 Retrieval evaluation for DeepParser-backed apps (P2) - Catalog: F146
- [ ] EVL-203 Human review queues for evaluation results (P2)

## P4 Breadth

- [ ] EVL-401 Standard benchmark suites: lm-evaluation-harness, OpenCompass, EvalScope (P4) - Catalog: F147, F151 to F153
- [ ] EVL-402 External experiment trackers (P4) - Catalog: F154

Catalog rows absorbed: F067, F145 to F154, F600, F650.
