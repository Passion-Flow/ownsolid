# Dataset processing runtime

[process.py](process.py) executes one inspect or publish-preparation attempt from an explicit protected request file. An explicit single-turn messages source maps to the canonical prompt/completion row during inspection. Publication consumes those canonical rows and materializes deterministic artifacts; Go datasets separately authorizes and commits the published version.

## Code and contract

The [package guide](projectone_data/README.md) documents parser, private boundary and publication-preparation ownership, including training's shared imports.

- [boundary.py](projectone_data/boundary.py): exact fields/IDs, private source/output paths, request limits and durable bounded result.
- [validation.py](projectone_data/validation.py): source/row validation, hashes and bounded errors.
- [publication.py](projectone_data/publication.py): deterministic train/validation materialization and manifest.
- [limits_generated.py](projectone_data/limits_generated.py): generated contract limits; change [contracts/datasets](../../contracts/datasets), not this file alone.

[pyproject.toml](pyproject.toml) specifies Python 3.12 and no external package dependencies. Training imports these helpers, so a semantic change also affects training and model-lifecycle source bindings.

## Execution and failures

The private request selects input hash, job/attempt identity, operation, seed/split policy and resource limits. Opt-in near-duplicate publication also binds the Go-owned line plan by SHA-256. The worker checks the plan before applying it and keeps it outside the version assets. A request with a test ratio requires that plan and writes manifest schema 2 with a separate test asset. Requests without a test ratio retain the original manifest and files. The worker rechecks the inspected digest, applies process limits and writes an exclusive bounded result with file/directory sync. Failure result creation is best effort if storage itself has failed; absence of a result is never acceptance.

[Control processor.go](../../services/control/internal/modules/datasets/processor.go) verifies installed programs, request/output paths and returned artifacts. Keep worker output preparation separate from SQL publication and authorization.

## Change and check

Synchronize dataset contract/generator, Go processor and publication validator, shared training imports, control image builder and tests. After pinned-toolchain and resource admission:

    python3 -B tools/quality/datasets/processor.py

Run from repository root with the configured external state/toolchain. The harness includes controlled core and actual child-process cases; Linux resource and full business/database/browser acceptance are separate profiles.

## Parquet source intake

[import_parquet.py](import_parquet.py) is the private decoder for the Go
[dataset intake boundary](../../services/control/internal/modules/datasets/source_import_parquet.go).
The worker opens a private local file, checks its footer and row-group
budgets, and emits the schema and one row at a time over its private pipe.
Linux also enforces the configured address-space limit.

For the CPython 3.12 Linux amd64 release target, use the
[Parquet wheel lock](requirements/linux-amd64-parquet.lock). On a connected
release builder, set `wheelhouse` to the staging directory and run from this
directory:

```sh
python3.12 -I -m pip --isolated --disable-pip-version-check download \
  --dest "$wheelhouse" --platform manylinux_2_28_x86_64 --implementation cp \
  --python-version 3.12 --abi cp312 --only-binary=:all: --require-hashes \
  -r requirements/linux-amd64-parquet.lock
```

In the disconnected Debian Bookworm build stage, install from the staged
wheel directory before switching to the runtime user:

```sh
python3.12 -I -m pip --isolated --disable-pip-version-check install \
  --no-index --find-links "$wheelhouse" --only-binary=:all: --require-hashes \
  -r requirements/linux-amd64-parquet.lock
```

Keep the installed wheel's licence and notice files in the distribution.
The optional `parquet` extra remains available for development on other
targets. The normal training processor uses the standard library. The
production image and user-facing upload lifecycle must enable the decoder
before serving Parquet requests.
