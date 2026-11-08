# EVL-101 and EVL-102 evaluation sets and reproducibility

## Upstream patterns

[MLflow evaluation datasets](https://mlflow.org/docs/latest/genai/datasets/)
pair a set of cases with expected outputs and reuse the set to compare model
or application versions. Its
[code-based scorers](https://mlflow.org/docs/latest/genai/eval-monitor/scorers/)
allow metrics with defined inputs instead of a second inference service.
[lm-evaluation-harness](https://github.com/EleutherAI/lm-evaluation-harness/blob/main/docs/interface.md)
records seeds for each random number generator, supports prediction-only runs
and preserves per-case observations for analysis. All six P1 items cover
these dataset, comparison, gating, regression, performance and safety
workflows. The roadmap states the repeatability requirements under EVL-101 and
EVL-102.

[PyTorch reproducibility](https://docs.pytorch.org/docs/2.15/notes/randomness.html)
describes differences between releases, hardware and CPU/GPU execution and
provides deterministic algorithms where supported. Its deterministic mode can
fail when an operation has no deterministic implementation.
[vLLM reproducibility](https://docs.vllm.ai/en/stable/usage/reproducibility/)
also requires specific scheduling or batch-invariance settings and limits its
guarantee to the same hardware and vLLM version. A seed does not fix changes in
numerical precision, kernels or request batching.

## Implementation boundary

Use immutable ownsolid dataset versions and model digests. Bind the scorer
version and its settings to each evaluation. For the first path, use
deterministic code-based classification with integer counts and compare
baseline and candidate on the same cases. The existing model preparation
recipe already calculates integer counts and gates against basis points.
Its Go report validator rechecks the case identities and totals before
acceptance. Its inference loop uses greedy decoding with one sequence at a
time and seeds the configured engine. It has no general deterministic
inference guarantee and does not establish one across CPU and CUDA.
Durations are measurements and can differ between runs.

Capture each prediction and its identity under the dataset access policy.
Replay with the exact scorer and recorded outputs. Separately qualify fresh
inference on an offline profile that pins the model, dataset, seed, runtime
image, tokenizer, prompt template, generation settings, hardware, driver,
precision and batch policy. Repeat the run with new processes and compare the
per-case output digests and integer scores before claiming that profile is
repeatable. Expose mismatched profiles and unsupported deterministic operations
as explicit outcomes. Results from an unqualified profile can describe one run,
with no repeatability claim.

Keep a model judge out of the first exact-scoring path. It would add another
inference stack and its own sources of variability. Avoid comparing only
aggregate scores, which can agree even when individual results changed. Reuse
the existing model and dataset boundaries once the versioned registries expose
them. An evaluation-only import store or promotion path would bypass their
ownership and access controls.

## Dependencies and acceptance

This design adds no runtime dependency. The existing pinned PyTorch and
Transformers code is distributed under the
[PyTorch BSD-style license](https://github.com/pytorch/pytorch/blob/main/LICENSE)
and [Transformers Apache-2.0 license](https://github.com/huggingface/transformers/blob/main/LICENSE).
PEFT is already used by model preparation and has an
[Apache-2.0 license](https://github.com/huggingface/peft/blob/main/LICENSE).
These code licenses permit offline use, commercial use and redistribution
with the required notices. Model weights, accelerator libraries and customer
datasets retain separate terms. The first evaluation path uses approved
customer inputs or synthetic test cases. MLflow and lm-evaluation-harness
are references and are not imported or distributed.

Verify replay against the same captured predictions after a fresh process
start. Include unknown labels, missing or duplicate case IDs, changed
dataset/model/scorer digests, cross-workspace reads, an exact threshold
boundary and an inference run interrupted before all cases complete. Fail
incomplete comparisons and report profile drift before interpreting score
differences. A successful replay proves the scoring calculation on the saved
predictions. Two real isolated inference runs with matching per-case outputs
are required to qualify a fresh inference profile.
