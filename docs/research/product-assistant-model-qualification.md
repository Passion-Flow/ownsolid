# Product assistant model qualification (AGT-110)

The [gateway completion contract](../../api/openapi/v1/model-gateway/openapi.json)
provides one assistant text result and bounded usage. The public request
contract rejects arbitrary tool calls. The current assistant has no
user-scoped model route. See the
[model result handoff](app-lane-questions.md#agt-108-production-model-result-boundary).

[MLflow's code-based scorers](https://mlflow.org/docs/latest/genai/eval-monitor/scorers/#code-based-scorers)
use deterministic checks for exact matching and format validation.
[OWASP excessive agency](https://genai.owasp.org/llmrisk/llm062025-excessive-agency/)
keeps authorization in the downstream system even when the model appears
to follow policy. The assistant needs independent evidence for refusal,
positive capability, and enforcement by the live authenticated adapter.

Reuse the existing [EVL-106 replay](../../runtimes/model-lifecycle/README.md)
for the first qualification input. It verifies trusted capture and dataset
digests, authorized version and validation provenance, model catalog, fixed
recipe, and every observation before scoring literal outcomes. The APP
qualification step requires one or more observations for each recipe
category and an exact refusal for every observation. Missing categories or
any other outcome fail the prerequisite. This avoids upgrading an
unverified transformed marker or an incomplete sample. A successful
prerequisite still leaves the model tier unqualified. A live gateway run
on the intended profile and positive task evidence are needed for a
production tier. Using model-generated prose, a lexical similarity
threshold, or a second judge would add ambiguity and would bypass the
existing scorer's provenance.

Expose the model identity, profile and capture fingerprints, category
coverage and aggregate outcomes to the offline reviewer. Emit no raw
response, prompt or tool arguments. The qualification command has no
gateway connection, production registration or command execution path.
Reject malformed and changed inputs before emitting a result. Cover exact
refusals, an unverified response, missing category coverage and a tampered
capture. Reuse the archived actual Qwen3-0.6B observations from
[EVL-106](evaluation-reproducibility.md#disconnected-publication-and-generation-result)
to verify a model remains unqualified when all three outcomes are
unverified. This replay does not start an inference process.

No new dependency, model weights or dataset are distributed. The runner
uses [CPython](https://docs.python.org/3.12/license.html), with the PSF license,
and existing ownsolid replay modules. The retained source dataset and recipe
are [CC0-1.0](https://creativecommons.org/publicdomain/zero/1.0/).
The archived outputs were produced by the pinned
[Qwen3-0.6B model](https://huggingface.co/Qwen/Qwen3-0.6B/tree/c1899de289a04d12100db370d81485cdf75e47ca)
under Apache-2.0. These terms allow commercial use, offline use and
redistribution with applicable notices. The runner uses the previously
authorized synthetic captures from temporary storage.

## Offline replay command

Use the approved catalog and independently retained version and capture
digests. The optional report input verifies that the existing scorer still
produces the exact retained canonical report. A valid replay emits a decision
even if its evidence leaves the model unqualified. An invalid input exits
nonzero and emits no decision.

```sh
PYTHONPATH=runtimes/model-lifecycle:runtimes/data-processing:runtimes/training \
  python3 -B services/control/internal/modules/productagent/testdata/qualify_model.py \
  --dataset-version "$version_file" --dataset-version-sha256 "$version_sha256" \
  --validation "$validation_file" \
  --capture "$capture_file" --capture-sha256 "$capture_sha256" \
  --check-report "$retained_report" \
  --model-catalog api/agent/v1/model-catalog.json \
  --recipe runtimes/model-lifecycle/tests/testdata/safety-recipe-v1.json
```

## Evidence

The SCI archive on the authorized CPU test server retained the native
case file with SHA-256
`08090645ef3cdd5f1a36c58f6194e3afe6c1cff79651decf856fbb9d14dc1d96`.
All three response texts, prompt digests and case identities matched the
normalized replay capture
`24baa39fdc9b7f6dbd4265c336ee01bb0a10f422bc057bd400b8313c47add5ad`.
Two fresh CPython processes in Docker network mode `none` returned
byte-identical decisions and matched the retained scorer report
`1ad1a6caf099b24c74d5224c655cb0bb3df21e7d1d4476d64ea7658c2d9769b5`.
All categories were covered and all three outcomes were unverified.
The Qwen3-0.6B candidate remained unqualified. Both a local synthetic
control and the [independently retained SCI control](evaluation-reproducibility.md#exact-refusal-control-on-the-same-published-inputs)
satisfied only the safety prerequisite. The SCI replay matched its retained
canonical report. A modified capture with the prior digest returned
`EVALUATION_CAPTURE_MISMATCH`
and no output. Focused checks also covered missing category coverage and
an unrelated scorer.

## Native observation binding

The archived native case file and normalized safety capture describe the
same response bytes. Require an independent SHA-256 pin for the original
file before accepting the optional native replay evidence. Match every
case identity, prompt digest and response digest in the scorer's frozen
order. Recompute each digest from the archived response text. A trusted
file digest establishes the selected archive and the per-case checks
bind it to the validated replay. A synthetic exact-refusal capture must
fail against the native archive even when its case identities match.
Keep the raw text out of the result and mark the evidence as retained
native output replay. A controlled replay without the native archive
keeps its existing mode.

The [SCI gateway compatibility check](evaluation-performance-benchmark.md)
found that the retained direct requests carry fields rejected by the
current gateway. Adapting the body removes seed forwarding, so it
cannot establish an equivalent production profile. A separately
qualified gateway request, session-scoped admission, and positive
capability evidence are needed before assigning a production tier.
The APP runner uses the existing bounded file reader and strict JSON
decoding helpers. It adds no dependency, model or dataset.

Append `--native-observations "$raw_cases"` and
`--native-observations-sha256 "$raw_cases_sha256"` to the replay command
to enforce this binding. The three archived Qwen3-0.6B responses matched
on disconnected replay and remained unqualified. Pairing SCI's exact-refusal
control with the same native archive returned
`ASSISTANT_NATIVE_OBSERVATIONS_MISMATCH` and emitted no qualification
decision. Focused tests also rejected the wrong archive digest, reordered
cases and changed response bytes.
