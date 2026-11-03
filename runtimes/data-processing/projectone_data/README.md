# Dataset parser and publication preparation

[boundary.py](boundary.py) validates exact request fields, private input/output namespaces, IDs and resource limits, then writes a bounded durable result. [validation.py](validation.py) parses source rows, rejects duplicate/invalid JSON and computes the inspection facts. [publication.py](publication.py) deterministically splits rows and writes immutable train/validation artifacts with their manifest.

These are the mechanics behind [process.py](../process.py). They do not authorize a Workspace or commit a dataset version. The Go dataset processor validates the returned bindings and separately owns SQL publication; absence of an output after storage failure is never success.

[limits_generated.py](limits_generated.py) is generated from [dataset contracts](../../../contracts/datasets). Training imports these helpers and model-lifecycle includes them through its inherited dependency closure, so source/behavior changes affect more than the standalone processor. Keep source digest, row semantics, split determinism, limit failures and both consumers synchronized.

The parent [runtime guide](../README.md) defines dependencies and execution. Focused executable boundary check from repository root: `python3 -B tools/quality/datasets/processor.py`, using the admitted pinned environment. Controlled parsing and actual child-process checks are distinct from Go database/browser acceptance.
