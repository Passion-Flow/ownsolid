# Model preparation runtime

[prepare.py](prepare.py) executes one protected preparation attempt without business credentials. It binds approved base-model, accepted adapter and held-out inputs, evaluates the declared recipe, materializes a merged model and returns a prepared result. Go modelpreparations separately owns review, publication and alias cutover.

## Code ownership

The [package guide](projectone_model_lifecycle/README.md) describes input/tensor verification, evaluation and merge boundaries within the inherited runtime.

- [configuration.py](projectone_model_lifecycle/configuration.py) owns recipe-derived typed settings and stable prompt identity.
- [boundary.py](projectone_model_lifecycle/boundary.py) validates request/private namespace, claims the attempt, applies limits and writes results.
- [inputs.py](projectone_model_lifecycle/inputs.py) validates file/tensor inventories, adapter/held-out identity and exact hashes.
- [evaluation.py](projectone_model_lifecycle/evaluation.py) implements classification/score/gate calculations.
- [engine.py](projectone_model_lifecycle/engine.py) runs pinned offline evaluation/merge and checks resulting tensors/files.

This directory deliberately imports sibling training and data-processing packages. There is no separate pyproject/lock: [the release builder](../../tools/release/model-lifecycle/build.py) inherits the verified training image/runtime and includes all three source trees plus required contracts. Treat these as one versioned dependency closure, not three interchangeable installations.

The builder can consume the distinct original training runtime-ready receipt and
return its own immutable CPU runtime-ready image before archive export, using
explicit healthy-Linux manufacturing inputs. Current source, base image, exact
packages, UID/cgroup and CPU import checks remain required. Follow the
[manufacturing procedure](../../docs/runbooks/model-lifecycle-manufacturing.md);
this readiness does not establish an actual model merge or GPU qualification.

## State and failures

Requests are bound to tenant/Workspace/preparation/attempt/training/held-out identities. Output must be empty and exclusively owned. The request is hashed and checked again before result publication; events and artifacts are bounded and synced. Cancellation, storage-full, output-conflict and processor failures retain structured outcomes. An evaluation gate or prepared file does not grant a model review or update a serving alias.

Offline model loading disables remote model code. Explicit CPU verification remains separate from real production CUDA device validation.

## Change and verify

Synchronize [contracts/model-lifecycle](../../contracts/model-lifecycle), control/modelpreparations, agent/modelpreparations, inherited training/data helper inputs, image recipe/source manifest, examples and recovery/target harnesses.

Existing strict controlled check from repository root with admitted pinned Python:

    python3 -B tools/quality/model-lifecycle/unit.py

Approved-model merge/generation, actual child cancellation/recovery, Go business acceptance, derived inference and target hardware remain separate checks. See [model-lifecycle validation](../../tests/validation/model-lifecycle).

## Replay a captured evaluation

Use the authorized frozen dataset `Version` JSON and its exact `validation.jsonl`
asset. Use the accepted preparation's original `result.json` and its retained
report SHA-256, the base model catalog, and the scorer recipe used by the
capture. Preserve the digest of the dataset version JSON under the same
authorized evidence handling. The command checks both pins, the workspace,
the validation asset's content, the capture's holdout digest, and the model,
recipe and per-case bindings.

Run from the repository root with the local Python runtime.

```sh
PYTHONPATH=runtimes/model-lifecycle:runtimes/data-processing:runtimes/training \
  python3 -B -m projectone_model_lifecycle.evaluation_report \
  --dataset-version version.json --dataset-version-sha256 "$version_sha256" \
  --validation validation.jsonl \
  --capture result.json --capture-sha256 "$accepted_report_sha256" \
  --model-catalog api/agent/v1/model-catalog.json \
  --recipe api/model-lifecycle/v1/recipe.json > evaluation.json
```

Use the report after the command succeeds. To re-score and compare with a
saved canonical report, repeat with `--check-report evaluation.json` and a
different output destination. A mismatch returns a nonzero exit and emits no
report. The version and capture digests are trust inputs obtained separately
from the files being checked. The replay command assumes the caller's existing
authority to obtain and retain those inputs.

The report scores the saved base-model labels and records their output hashes.
It keeps the preparation holdout ID separate from the published dataset
version. It records the captured execution profile and marks fresh inference
as unverified. It can reproduce the score from the captured labels, while
tokenization, generation and label normalization require separate inference
evidence. See the [replay design and acceptance](../../docs/research/evaluation-reproducibility.md#evl-101-offline-report-replay).

## Compare two captured evaluations

Keep the two canonical reports created by the replay command and the original
captures, independent capture SHA-256 pins, and corresponding model catalogs.
Both reports must use the same pinned frozen dataset version, validation file
and scorer. This supports published two-way and three-way manifests through
their validation asset. Each side can use its own model version and execution
profile.

```sh
PYTHONPATH=runtimes/model-lifecycle:runtimes/data-processing:runtimes/training \
  python3 -B -m projectone_model_lifecycle.evaluation_comparison \
  --dataset-version version.json --dataset-version-sha256 "$version_sha256" \
  --validation validation.jsonl --recipe api/model-lifecycle/v1/recipe.json \
  --baseline-report baseline.json --baseline-capture baseline-result.json \
  --baseline-capture-sha256 "$baseline_capture_sha256" --baseline-model-catalog baseline-model.json \
  --candidate-report candidate.json --candidate-capture candidate-result.json \
  --candidate-capture-sha256 "$candidate_capture_sha256" --candidate-model-catalog candidate-model.json \
  > comparison.json
```

The digest variables above refer to the accepted original captures. The command
re-scores each capture and checks the resulting bytes against its retained
report. Both reports must cover every frozen case with the same expected
label. A rejected input exits nonzero and emits no comparison.

The comparison records both model versions, profiles, scores and retained
report digests. Its integer `delta.correct` is candidate correct minus
baseline correct, with the shared case count in `delta.row_count`. The
per-case outcomes distinguish cases only one side got right, even when the
totals tie. Repeat the command with the same inputs to reproduce the bytes.
Fresh inference repeatability requires separate evidence from the actual
engine. Differences across execution profiles describe the captured runs.

## Replay controlled safety cases

Use the original six-row [synthetic dataset](tests/testdata/safety-regression-v1.jsonl)
and [fixture recipe](tests/testdata/safety-recipe-v1.json). Both files are
available under [CC0-1.0](https://creativecommons.org/publicdomain/zero/1.0/)
for offline use, commercial use and redistribution. They use fictional
requests, a fictional private note and invented marker strings.

Publish the JSONL with a two-way split and a 50% validation share. Each source
group has one row in each category. Retain the authorized Version JSON, its
independent SHA-256 pin and the exact validation asset. The recipe defines
the system instruction and a synthetic canary for a separate private context.
The validation file supplies the user prompt and expected case category.

For controlled replay, supply a JSON capture with these fields.

| Field | Value |
| --- | --- |
| `schema_version`, `kind` | `1`, `controlled_safety_fixture` |
| `capture_id`, `workspace_id` | Capture UUID and the frozen version's workspace UUID |
| `dataset_version_sha256`, `heldout_sha256` | SHA-256 of the exact Version JSON and validation asset bytes |
| `model_catalog_sha256` | Existing approved model catalog digest used as version metadata |
| `recipe_id`, `recipe_sha256` | `safety_regression_v1` and SHA-256 of the recipe as sorted compact UTF-8 JSON |
| `execution_profile` | Object with fixture `id` and integer `seed` from 0 to 2147483647 |
| `row_count`, `cases` | Count and array of all validation rows in frozen order |
| Each case | `source_id`, SHA-256 of the exact UTF-8 prompt as `prompt_sha256`, and controlled text as `output` |

Use a lowercase ASCII profile ID of at most 64 characters with letters,
digits, hyphens or underscores, beginning with a letter or digit. Keep
each output within 16 KiB of UTF-8. Retain the SHA-256 of the capture through
the same independent evidence handling as other replay inputs.

```sh
PYTHONPATH=runtimes/model-lifecycle:runtimes/data-processing:runtimes/training \
  python3 -B -m projectone_model_lifecycle.evaluation_report \
  --dataset-version version.json --dataset-version-sha256 "$version_sha256" \
  --validation validation.jsonl \
  --capture safety-capture.json --capture-sha256 "$safety_capture_sha256" \
  --model-catalog api/agent/v1/model-catalog.json \
  --recipe runtimes/model-lifecycle/tests/testdata/safety-recipe-v1.json \
  > safety-report.json
```

The report records the frozen version, model catalog, scorer, capture and
fixture profile. It records a literal canary hit even when the same output
contains refusal text. The refusal, injection and unsupported-action
markers use case-sensitive whole-output matches. Other outputs, including
empty text and transformed markers, are `unverified`. Each outcome has a
separate count and each output has a digest. The report omits the captured
text and supplies no overall safety pass. Use `--check-report` to verify
the report bytes on replay. The paired comparison currently supports the
classification scorer. Safety conclusions about a real model require
separate inference and semantic evaluation evidence.

To run the reference captures against an actual processor-frozen fixture
without a model engine, run from the repository root.

```sh
PYTHONPATH=runtimes/model-lifecycle:runtimes/data-processing:runtimes/training \
  python3 -B -m unittest discover -s runtimes/model-lifecycle/tests \
  -p test_evaluation_safety.py
```
