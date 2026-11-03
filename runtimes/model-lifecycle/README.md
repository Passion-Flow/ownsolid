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
