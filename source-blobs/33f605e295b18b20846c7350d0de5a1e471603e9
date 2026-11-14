# Training runtime

[train.py](train.py) executes one admitted offline training attempt. It consumes a protected request, approved model and exact dataset inputs; produces bounded events, checkpoints, adapter artifacts and a result; and leaves business acceptance to Go.

## Owners

The [package guide](projectone_training/README.md) details request, dataset, computation and sealed-resume responsibilities shared with model preparation.

| Source | Responsibility |
| --- | --- |
| [boundary.py](projectone_training/boundary.py) | Request validation, private paths, attempt lock, process limits and result writing |
| [configuration.py](projectone_training/configuration.py) | Typed training configuration and runtime contract |
| [data.py](projectone_training/data.py) | Validated dataset/tokenization/collation, reusing data-processing |
| [preview.py](projectone_training/preview.py) | Bounded tokenizer preview for authorized dataset samples |
| [catalog.py](projectone_training/catalog.py) | Approved model catalog identity |
| [engine.py](projectone_training/engine.py) | Pinned offline Transformers/PEFT execution and bounded artifacts |
| [checkpoints.py](projectone_training/checkpoints.py) | Sealed checkpoint inventory and exact resume identity |

Generated configuration/processor files come from [api/training/v1](../../api/training/v1). [runtime.json](runtime.json), [pyproject.toml](pyproject.toml) and [requirements](requirements) pin the execution/manufacturing profiles. Model-lifecycle inherits this runtime; dependency changes require its build and source binding to be reconsidered.

The release image must install dependencies from the pinned wheelhouse and
include the approved catalogue under api/agent/v1/model-catalog.json. Images
using the inherited contracts/agent/v1 layout can still supply the catalogue.
The worker checks the selected file against the request's catalogue digest.
The [runtime delivery research](../../docs/research/training-runtime-delivery.md)
describes the release and Agent verification gates.

## Attempt and recovery contract

The worker claims an exclusive attempt, refuses pre-existing output and uses private creation permissions. Event sequence/bytes and result/artifact limits are bounded. SIGTERM/SIGINT request cancellation; checkpoint resume must match sealed source/execution/tenant/Workspace/job identity, not merely find a checkpoint directory.

Each checkpoint receipt covers the adapter, optimizer, scheduler, trainer step,
and RNG files. The worker publishes the receipt only after validating the file
layout and syncing the checkpoint data and directories. Resume requires the
receipt digest supplied by the orchestrator. The last checkpoint accepted by
control is the recovery point if the node is lost.

Offline model loading disables remote model code. Explicit CPU verification flags do not qualify the production CUDA path, which checks real device capabilities. The agent inspects actual installed image identity and workload boundaries; control independently accepts or reconciles results.

## Offline tokenizer preview

A trusted caller can pass sampled canonical data-processing rows and the
selected model catalogue SHA-256 to `projectone_training.preview.preview_samples`.
Use the installed local model path and the applicable hash-bound training
lock under `requirements/`. The callable accepts up to eight samples and
64 KiB of combined UTF-8 source IDs, prompts and completions. Use the
training token budget of 32 to 2,048 tokens, within the model context.
The result separates the rendered prompt, the original completion and EOS.
The completion count includes EOS. A sample above the budget reports the
zero-based index and section of the first token outside the budget.

To exercise the real tokenizer, run the training tests from the repository
root in the pinned environment with network access denied. Set
`OWNSOLID_TEST_QWEN_TOKENIZER_DIRECTORY` to the approved local Qwen3-0.6B
snapshot and set `PYTHONPATH=runtimes/training:runtimes/data-processing`.
The test only copies the five tokenizer assets. See the
[DAT-107 research and handoff](../../docs/research/training-tokenizer-preview.md)
for release configuration and UI responsibilities.

## Changes and verification

Keep the [training API](../../api/training/v1), control and Agent validators,
and runtime image in sync when changing the request or output format.

Run the checkpoint and catalogue tests from the repository root. They
use synthetic checkpoint files and the Python standard library:

    PYTHONPATH=runtimes/training:runtimes/data-processing python3 -B -m unittest discover -s runtimes/training/tests

A product recovery run requires a training installation and an accepted
checkpoint. Cancel or interrupt a running job after control has accepted a
checkpoint, then request recovery for that checkpoint. Verify that the new
attempt advances from the recorded optimizer step and that control accepts its
final artifacts. Run with external network access disabled. CPU fixture tests
cover the receipt path independently of the production NVIDIA execution.
