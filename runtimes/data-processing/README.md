# Dataset processing runtime

[process.py](process.py) executes one inspect or publish-preparation attempt from an explicit protected request file. It validates the source and can materialize deterministic publication artifacts; Go datasets separately authorizes and commits the published version.

## Code and contract

The [package guide](projectone_data/README.md) documents parser, private boundary and publication-preparation ownership, including training's shared imports.

- [boundary.py](projectone_data/boundary.py): exact fields/IDs, private source/output paths, request limits and durable bounded result.
- [validation.py](projectone_data/validation.py): source/row validation, hashes and bounded errors.
- [publication.py](projectone_data/publication.py): deterministic train/validation materialization and manifest.
- [limits_generated.py](projectone_data/limits_generated.py): generated contract limits; change [contracts/datasets](../../contracts/datasets), not this file alone.

[pyproject.toml](pyproject.toml) specifies Python 3.12 and no external package dependencies. Training imports these helpers, so a semantic change also affects training and model-lifecycle source bindings.

## Execution and failures

The private request selects input hash, job/attempt identity, operation, seed/split policy and resource limits. The worker rechecks the inspected digest, applies process limits and writes an exclusive bounded result with file/directory sync. Failure result creation is best effort if storage itself has failed; absence of a result is never acceptance.

[Control processor.go](../../services/control/internal/modules/datasets/processor.go) verifies installed programs, request/output paths and returned artifacts. Keep worker output preparation separate from SQL publication and authorization.

## Change and check

Synchronize dataset contract/generator, Go processor and publication validator, shared training imports, control image builder and tests. After pinned-toolchain and resource admission:

    python3 -B tools/quality/datasets/processor.py

Run from repository root with the configured external state/toolchain. The harness includes controlled core and actual child-process cases; Linux resource and full business/database/browser acceptance are separate profiles.
