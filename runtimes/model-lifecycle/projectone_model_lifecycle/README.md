# Preparation inputs, evaluation and merge

[boundary.py](boundary.py) validates the protected request, exact attempt/source binding, exclusive output namespace and bounded event/result lifecycle. [configuration.py](configuration.py) derives typed recipe settings and prompt identity. [inputs.py](inputs.py) verifies inventories, adapter/tensor envelopes and held-out rows before expensive computation.

[evaluation.py](evaluation.py) owns prediction-label interpretation, scores and declared gate calculations. [engine.py](engine.py) performs pinned offline evaluation/merge, cancellation checks and standalone tensor/output validation. A computed gate is evidence for the Go review owner; it does not approve a model or move an alias.

This package imports the sibling training/data-processing helpers and inherits the approved training runtime. It has no independent lockfile/environment. Update [model-lifecycle contracts](../../../contracts/model-lifecycle), the [release closure](../../../tools/release/model-lifecycle/build.py), control/agent preparation validators and retained source hashes together when any shared input semantics change.

Use [prepare.py](../prepare.py) and the [parent guide](../README.md). Focused controlled check: `python3 -B tools/quality/model-lifecycle/unit.py`. Real approved-model generation/merge, interruption, output acceptance and target GPU execution remain separate admitted profiles; a synthetic scoring test must not be labeled end-to-end preparation.
