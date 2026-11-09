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
