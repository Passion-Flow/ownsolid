# Training computation and sealed resume state

[boundary.py](boundary.py) validates request/source/attempt identity, claims the private attempt, applies limits and writes bounded events/results. [configuration.py](configuration.py) owns typed training choices; generated configuration/processor files come from [training contracts](../../../contracts/training). [catalog.py](catalog.py) binds approved model identity.

[data.py](data.py) verifies the exact dataset files and tokenizes/collates them using shared data-processing validation. [engine.py](engine.py) performs pinned offline training and adapter materialization. [checkpoints.py](checkpoints.py) inventories/seals checkpoint bytes and validates exact source/execution/tenant/Workspace/job bindings for resume; a matching directory name is insufficient.

The package has no business credentials and cannot publish or approve an output. Control and agent independently validate its results. Cancellation, checkpoint conflict, output collision and unavailable storage preserve structured failure; do not make a CPU verification flag bypass production CUDA/device requirements.

Model-lifecycle inherits this runtime and shared helpers. Changes require synchronized contract generation, control/agent validators, image/lock/source manifests and resume tests across both consumers. Use [train.py](../train.py) and the [parent guide](../README.md); the admitted CPU process profile is `python3 -B tools/quality/training/core.py --profile process`. Approved model and real target training need their separate budgets and receipts.
