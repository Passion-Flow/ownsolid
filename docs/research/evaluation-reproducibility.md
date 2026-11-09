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
