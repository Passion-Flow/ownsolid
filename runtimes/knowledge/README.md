# Offline knowledge encoder

[embed.py](embed.py) accepts one bounded JSON request on stdin, arms the process guard before loading model dependencies, and delegates to the installed CPU encoder. It returns chunk/vector JSON; it does not own document visibility, generation activation, retrieval permissions or answer authorization.

## Boundaries and inputs

The [encoder package guide](projectone_knowledge/README.md) maps process guards, request/result validation and generation compatibility obligations.

- [process_guard.py](projectone_knowledge/process_guard.py) bounds parent/wall lifetime using the caller's explicit process-control contract.
- [boundary.py](projectone_knowledge/boundary.py) validates the bounded request/result protocol.
- [engine.py](projectone_knowledge/engine.py) verifies pinned runtime packages and performs offline tokenizer/model encoding on CPU with remote code disabled.
- [contract_generated.py](projectone_knowledge/contract_generated.py) is generated from the [knowledge contract](../../contracts/knowledge).

[runtime.json](runtime.json), [pyproject.toml](pyproject.toml) and [architecture-specific locks](requirements) define the approved CPU stack. The model catalog and source digest are part of the Go processor boundary; a similarly named local model is not a substitute.

## Persistence and failure ownership

The encoder consumes text/model inputs and returns bounded vectors; Go [knowledge/processor.go](../../services/control/internal/modules/knowledge/processor.go) owns admitted execution/scratch and validates output dimensions, source digest, chunk bounds and normalization. Go rechecks current generation and authority after encoding before using the result.

SQL indexing/retrieval lives in control. PostgreSQL vector queries and MySQL's authorized streaming cosine scan have distinct performance characteristics; this encoder does not imply a shared ANN index or database behavior.

## Change and verify

Synchronize contract/generator, Go processor/indexing/search validation, frozen encoder catalog, CPU locks/image, control image foundation and deployment manifests. Changes to chunking/tokenization/dimension require explicit index-generation compatibility rather than silently reusing old vectors.

Actual isolated database/processor harness:

    python3 -B tools/quality/knowledge/database.py --fixture-manifest PATH --python PATH --model-manifest PATH

Supply real protected fixture/runtime/model references after resource admission. Encoder, real SQL, browser, answer gateway and target profile evidence are distinct; do not infer all from one harness result.
