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

## EVL-101 offline report replay

The [dataset version reader](../../services/control/internal/modules/datasets/versions.go)
supplies an authorized frozen Version record with an immutable manifest identity
and an exact validation asset. The [retained preparation reader](../../services/control/internal/modules/modelpreparations/read_preparations.go)
supplies a report SHA-256 for the accepted captured output. Use their existing
records and the approved model catalog. The operator supplies the two trusted
digests with the local evidence. No new evaluation datastore or artifact
authorization mechanism is introduced.

Use the base-model observations of the existing v1 preparation result. Reuse
the approved catalog digest and the existing classification scorer. The
preparation holdout and the frozen dataset validation version are independent
provenance. Require the same workspace and byte-identical validation and
captured holdout digests before using the published version for replay. Keep
both identities in the report. Never substitute the training partition or
equate the holdout ID with the published version ID. Identical validation
partitions can belong to different published versions. The independently
retained version digest pins the selected version. The legacy capture records
the digest of the validation bytes it scored. The captured model catalog
digest and recipe identity must match the supplied catalog and scorer recipe.

Match every observation in frozen order by source ID and exact prompt SHA-256.
Verify the expected label against the frozen record and reject duplicate case
identities, extra cases, missing cases and invalid predicted labels. Recompute
integer scores and compare them with the captured base score. The capture
contains normalized labels and output digests. Replay scores those observations
without reproducing the original response text or independently rerunning its
normalization. Record the captured seed, device, computation precision,
generation settings, runtime identity and package versions. Mark fresh
inference as unverified. Keep durations and the current wall clock out of the
report. Serialize sorted compact UTF-8 JSON using the recipe's existing
encoding convention. This takes the invariance principle from
[RFC 8785](https://www.rfc-editor.org/rfc/rfc8785.html) without claiming JCS
encoding.

No new runtime dependency, model or dataset is distributed. CPython and the
existing ownsolid runtime modules are used offline. Verify byte-identical
output in fresh processes, comparison with a retained report, and rejection
of dataset, model, scorer, workspace and case mismatches. Use a real
processor-published validation asset and the checked-in pinned catalog in the
fixture. A disconnected PostgreSQL integration probe published six synthetic
rows through the dataset processor and read the frozen version and three
validation rows through the authorized control-plane readers. Two offline
replays of those inputs and synthetic predictions matched byte for byte at
two correct out of three. The probe rejected an incorrect version digest.
EVL-101 stays open for an authorized control-plane and UI workflow and real
inference repeatability.

## EVL-102 paired offline comparison

[MLflow compares runs](https://www.mlflow.org/docs/latest/genai/eval-monitor/comparing-evaluation-runs/)
on one dataset and scorer set. It matches rows by a stable request identity
or input hash and reports unpaired rows. The
[harness evaluator](https://github.com/EleutherAI/lm-evaluation-harness/blob/main/lm_eval/evaluator.py)
preserves document identity and prompt and target digests for per-case analysis.

Replay both retained EVL-101 reports against one independently pinned frozen
version and exact validation partition. Each side supplies its accepted capture
digest and its own model catalog. The catalog and capture must agree within
each side, so models can differ between sides. Verify the canonical report
bytes against the retained report after invoking the scorer. Match the frozen
version and scorer identity, including the implementation digest. Pair cases
by source ID and exact prompt SHA-256, with the same expected label on each
side. Emit the pairs in frozen order.

[DAT-105's published manifest](../../api/datasets/v1/processor.go) adds schema
version 2 and a separate sealed test partition. Both supported manifest
versions still bind the validation asset by name, digest and row count.
Accept either version when replaying the recorded validation digest.
Verify a three-way publication with the actual data processor and reject
the test partition when supplied in place of validation.

Reject incomplete or duplicate pairs, changed ground truth, a different
dataset version even when validation bytes coincide, and model or scorer
binding mismatches. A positional join risks attributing a prediction to the
wrong case. Comparing two totals or dropping unpaired cases would hide those
failures. Use integer counts and candidate-minus-baseline differences with
the shared denominator. Preserve both execution profiles and the digests of
both canonical reports. The result describes the retained predictions and
does not estimate statistical significance or attribute differences to the
model when other profile settings differ.

No new dependency, model weights or dataset are distributed. The comparison
uses CPython and existing ownsolid code, with synthetic captures for offline
verification. MLflow and the harness are references. Verify two independent
CLI runs with different Python hash seeds, both directions of the score
difference, and a zero difference with equal scores and changed predictions.
Reject missing, duplicate and altered cases, swapped catalogs, a changed
scorer and modified report bytes without emitting a comparison. Keep real
inference repeatability, authorized report delivery and UI integration open.

## EVL-106 deterministic safety regression

Use the threat categories from [OWASP prompt injection](https://genai.owasp.org/llmrisk/llm01-prompt-injection/),
[sensitive information disclosure](https://genai.owasp.org/llmrisk/llm022025-sensitive-information-disclosure/)
and [misinformation](https://genai.owasp.org/llmrisk/llm092025-misinformation/).
[garak](https://github.com/NVIDIA/garak/blob/main/docs/source/basic.rst)
separates probes from detectors and can detect a known payload string. The
[harness task guide](https://github.com/EleutherAI/lm-evaluation-harness/blob/main/docs/task_guide.md)
shows how to make exact-match rules explicit. These projects are design
references. No code, model, dataset or prompt text is taken from them.

Ship an original synthetic six-row fixture and its protocol under
[CC0-1.0](https://creativecommons.org/publicdomain/zero/1.0/). They may be
used offline, commercially and redistributed. They contain fictional user
requests, capabilities and private context, with no customer data or secrets.
Replay depends only on CPython and existing ownsolid code. The existing model
catalog is used only as version metadata. The fixture requires no model
weights and introduces no model license or other runtime dependency.

Use the same authorized frozen Version JSON, exact validation asset and
canonical report entry point as classification. In this fixture the frozen
completion names the case category, and the versioned recipe defines a
refusal marker, a private fixture canary and exact markers for following an
injection or claiming an unavailable action. Read a separately pinned
controlled capture of the output text. Match its frozen version digest,
workspace, recipe, model catalog and each case identity. Report the supplied
fixture profile identity and seed with a digest, and mark fresh inference
unverified. A preparation report's labels and output digests cannot establish
what text a response contained.

A literal canary anywhere in the output takes precedence. An exact refusal
marker confirms the fixture protocol refusal. Exact category-specific
markers identify the known injection and unsupported action responses.
Other text, empty text, encoded canaries and natural-language claims remain
unverified. The report contains case identities, output digests and integer
outcome counts. It omits captured text and canary values. There is no
aggregate safety pass and the classification comparison does not compare
these outcomes. Semantic judging and fresh inference require their own
evaluation evidence.

Use the data processor to freeze one of the complete fixture groups. Verify
byte-identical replay in disconnected processes and distinct profile
provenance. Exercise exact refusal, leakage mixed with refusal text, the
known injection and unsupported claim, and unverified alternatives. Reject
missing cases, wrong bindings and oversized output. Real model safety,
authorized capture delivery and UI remain open.

## EVL-103 retained preparation quality decision

[MLflow's result validator](https://mlflow.org/docs/latest/api_reference/_modules/mlflow/models/evaluation/validation.html)
checks a candidate's metric floor separately from its change against a baseline.
Use the existing ownsolid integer gate and the original preparation request.
The [Go result decoder](../../api/model-lifecycle/v1/result.go) independently
rebuilds both confusion matrices from the exact held-out cases and checks the
saved verdict. The [review code](../../services/control/internal/modules/modelpreparations/review_snapshot.go)
recalculates the gate under the persisted preparation configuration and returns
`MODEL_PREPARATION_QUALITY_FAILED` when the quality condition fails.

### Retained CPU capture

Replay the original files from the [retained CPU run](https://github.com/Passion-Flow/ownsolid/commit/50fb5b01).
The approved model is Qwen3-0.6B at revision
`c1899de289a04d12100db370d81485cdf75e47ca`. The run used the existing
Apache-2.0 model and generated CC0 rows documented in the
[artifact evidence](training-dataset-lineage.md#retained-cpu-acceptance).
Replay calculates scores from the small archived metadata and normalized
predictions.

| Frozen bytes | SHA-256 |
| --- | --- |
| Original `result.json` | `f8cb63963210b9af5886624e107b05386bc8a01894eb0293fbe7c3cd9f4539d3` |
| Original `request.json` | `238d7a378f215a65d7a1a4fe632f491762c09a992c31f89ad4351275c7bfec0d` |
| Original `heldout.jsonl` | `0fd36fecf2b79bfd5e23a09cf0c66d395304824d5ae18ca4be8d01a23f9b09af` |

On disconnected Linux, the EVL-101 scorer and EVL-102 pairing recomputed
both scores from the original request-bound holdout. The archive supplies
the preparation holdout identity. The published dataset report entry point
requires a separate authorized Version JSON. The Go decoder independently
checked the same request and held-out rows, plus the tokenizer template
pinned by the approved catalog.

The CPU profile used float32 computation and seed 42. Both models predicted
`technical` on all three rows and scored 1/3. The captured policy required
8000 basis points of accuracy and allowed zero basis points of regression.
The accuracy check failed at 10000 versus 24000 in the integer comparison.
The regression check passed with zero difference. The original verdict was
false, and changing only the recorded verdict to true made the Go decoder
return `MODEL_PREPARATION_REPORT_INVALID`. The production review error is a
source-level check. The authorized review flow remains open.

### Offline control

Use the existing synthetic comparison fixture from [EVL-102](https://github.com/Passion-Flow/ownsolid/commit/e13a27d3).
The fixture uses repository-authored prompts, controlled labels and model
catalog metadata. The processor freezes a three-way partition and both
reports bind its validation asset. Repeat the existing comparison entry point
with two Python hash seeds. The bytes matched, and the original gate accepted
the 2/3 to 3/3 direction and rejected the 3/3 to 2/3 direction under the same
8000/0 policy. The [existing Go gate vectors](../../api/model-lifecycle/v1/contract_test.go)
and Python gate agreed at both exact edges. Two correct out of three meets
6666 basis points and misses 6667. A one-of-three drop exceeds 3333 basis
points and fits 3334. Under the captured 8000/0 policy, both gate
implementations accepted a 4/5 tie at exactly 80% and rejected a 3/5 tie on
accuracy. A drop from 5/5 to 4/5 passed the accuracy check and failed
regression. These controlled scores establish gate behavior. The real
model quality result is the retained CPU capture above.

Run the contract vectors with
`go -C api test -count=1 ./model-lifecycle/v1 -run '^TestResourceAndConfigurationBoundsAndExactIntegerGate$'`.
Run the comparison fixture in
[test_evaluation_report.py](../../runtimes/model-lifecycle/tests/test_evaluation_report.py)
with the same source Python path as the
[offline replay command](../../runtimes/model-lifecycle/README.md#replay-a-captured-evaluation).
Keep EVL-103 open for the authorized release and interface workflow.

### Qualified CPU comparison at the same policy

The [qualification archive](https://github.com/Passion-Flow/ownsolid/commit/0a722cff)
contains two real native CPU preparation captures. On disconnected Linux,
the existing EVL-101 scorer, EVL-102 pairing and independent Go result
decoder each verified a base score of 1/3 and candidate score of 3/3 under
the same 8000/0 gate. Both captures had matching per-case predictions,
output digests and execution profiles. The Go decoder refused a changed
verdict on each original request. The holdout bytes match the earlier
quality refusal, so the policy and evaluation samples are fixed.

| Capture | Original result SHA-256 |
| --- | --- |
| Initial qualification | `0eff66dab1940c7b219b50dd3b7bf0976fbcff55c0ff526fbfb76e81ea4aaf78` |
| Replay bound to the authorized publication | `5b244e7c87060e0afab4c4dcd57387f46964d4e37506ad96827df82a0b5ecf47` |

The authorized training manifest and exact train and validation assets
match the retained training request and result. The preparation request
binds the training job, attempt, configuration and adapter inventory.
Neither the normalized holdout prompts nor their source IDs appear among
the 21 training and three validation rows. The train and validation assets
establish training lineage. The independently frozen holdout supplies
evaluation ground truth.

These are native CPU quality outcomes on three original CC0 cases.
Production acceptance requires the NVIDIA preparation profile, an
authenticated physical stop, and the authorized audited promotion flow.
See the [retained publication and artifact evidence](training-dataset-lineage.md#qualified-cpu-result-and-authorized-dataset-publication).
