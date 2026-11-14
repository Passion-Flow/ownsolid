# Offline encoder package

[process_guard.py](process_guard.py) arms parent/wall-lifetime controls before loading model dependencies. [boundary.py](boundary.py) strictly decodes the stdin request, validates dimensions/limits and verifies the returned vector/chunk envelope. [engine.py](engine.py) checks pinned runtime versions and performs offline CPU model/tokenizer encoding with remote model code disabled.

[contract_generated.py](contract_generated.py) comes from the [knowledge contract](../../../contracts/knowledge). Change that owner and generator together rather than editing a generated constant independently. Chunking, normalization, dimension or tokenizer changes require explicit compatibility with retained knowledge generations; silently reusing an old index is not safe.

The package returns computation results only. Control/knowledge owns private scratch, current document scope, generation activation and the post-encoding authority recheck. There is no direct SQL, user credential or external translation dependency here.

Use [embed.py](../embed.py) and the [parent runtime guide](../README.md) for admitted invocation and locks. Actual encoding and selected-database checks use `tools/quality/knowledge/encoder.py` and `database.py` with their protected model/runtime/fixture inputs. Unit protocol validity does not establish the installed model or full retrieval path.
