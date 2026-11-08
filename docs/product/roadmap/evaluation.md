# Evaluation

Module reviewed on 2026-11-09 against MLflow, lm-evaluation-harness, PyTorch and
vLLM. See [evaluation sets and reproducibility](../../research/evaluation-reproducibility.md).

Measuring models and applications before release, and gating releases on the result.

- Owns: `runtimes/model-lifecycle` evaluation, `services/control` evaluation APIs.
- Depends on: models-and-registry, data, serving.

## P1 Enterprise release

- [ ] EVL-101 Evaluation sets with versions, scorers and reports (P1) - Catalog: F148
- [ ] EVL-102 Model-against-model comparison on the customer's own set (P1) - Catalog: F067
- [ ] EVL-103 Release gates: a model or app version cannot be promoted below its threshold (P1) - Catalog: F650
- [ ] EVL-104 Production traces turned into regression cases (P1)
- [ ] EVL-105 Performance benchmark: latency, throughput and cost per model and hardware (P1) - Catalog: F150
- [ ] EVL-106 Safety and regression sets: injection, leakage, unsupported answers (P1) - Catalog: F149

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
