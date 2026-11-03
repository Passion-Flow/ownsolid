# Training runtime

[train.py](train.py) executes one admitted offline training attempt. It consumes a protected request, approved model and exact dataset inputs; produces bounded events, checkpoints, adapter artifacts and a result; and leaves business acceptance to Go.

## Owners

The [package guide](projectone_training/README.md) details request, dataset, computation and sealed-resume responsibilities shared with model preparation.

| Source | Responsibility |
| --- | --- |
| [boundary.py](projectone_training/boundary.py) | Request validation, private paths, attempt lock, process limits and result writing |
| [configuration.py](projectone_training/configuration.py) | Typed training configuration and runtime contract |
| [data.py](projectone_training/data.py) | Validated dataset/tokenization/collation, reusing data-processing |
| [catalog.py](projectone_training/catalog.py) | Approved model catalog identity |
| [engine.py](projectone_training/engine.py) | Pinned offline Transformers/PEFT execution and bounded artifacts |
| [checkpoints.py](projectone_training/checkpoints.py) | Sealed checkpoint inventory and exact resume identity |

Generated configuration/processor files come from [contracts/training](../../contracts/training). [runtime.json](runtime.json), [pyproject.toml](pyproject.toml) and [requirements](requirements) pin the execution/manufacturing profiles. Model-lifecycle inherits this runtime; dependency changes require its build and source binding to be reconsidered.

The existing release selector/builder supports explicit healthy-Linux
manufacturing inputs and an early CPU runtime-ready receipt. It preserves the
original 56-wheel lock, metadata/license validation and actual CPU image checks;
runtime readiness remains separate from archive/GPU/package qualification. See
the [manufacturing procedure](../../docs/runbooks/model-lifecycle-manufacturing.md).

## Attempt and recovery contract

The worker claims an exclusive attempt, refuses pre-existing output and uses private creation permissions. Event sequence/bytes and result/artifact limits are bounded. SIGTERM/SIGINT request cancellation; checkpoint resume must match sealed source/execution/tenant/Workspace/job identity, not merely find a checkpoint directory.

Offline model loading disables remote model code. Explicit CPU verification flags do not qualify the production CUDA path, which checks real device capabilities. The agent inspects actual installed image identity and workload boundaries; control independently accepts or reconciles results.

## Changes and verification

Update contracts/training and generated files, control/training request/result/state code, agent/training image/container/journal, deployment training recipes, locks and retained source manifests together.

Existing native CPU harness after explicit memory/toolchain/model admission:

    python3 -B tools/quality/training/core.py --profile process

Other named profiles are tiny, approved, resume and approved-cli. Run serially under their declared budgets. GPU, native Linux/cgroup and target-image execution have separate [validation harnesses](../../tests/validation/training); never promote CPU/emulated results to those qualifications.
