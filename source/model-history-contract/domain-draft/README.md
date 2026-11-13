# Producer-bound history contract draft

Source 6a2dd96f on independent base 38aad603 consumes MODEL 2b4aaae5 and domain
343cdd9d. The complete Models baseline is byte-identical between those sources.
The SDK test fixture defines two unregistered Admin reads; no product API, Web,
model module or migration changed. Its state is prototype-unregistered.

Seven projection tests, nine controlled Python codecs, pinned generation of
81 Go/Python files and private TypeScript/Help generation/checks passed. Go
parses; the three prepared Go codec groups have not compiled or run. The old
revision-B Go pass does not qualify this revised input. No actual signed token,
HTTP dispatcher, current authorization, PostgreSQL or S-202 acceptance was run.

Recheck the committed fixture from work/ with the documented Python 3.12:

    python3.12 -m unittest discover -s tools/codegen/sdk/tests -p test_model_history_draft.py -v

The Linux controlled codec replay, inside a private no-external-network namespace:

    /root/lane-rel-tools/obs109-contract.Psjaj7Gp/python/bin/python /root/lane-rel-tools/model-history-prototype/domain-draft/wire/test_python_wire.py

Generation inputs, source hashes and real command/results are in generation.json,
projection-proof.json, python-wire-proof.json and handoff.json. The seven-file
unregistered-projection.patch applies to a private 2b4aaae5 baseline only. Do not
apply it to product Web or publish the draft while the routes/schema are absent.
Admin operations are excluded from Portal paths/summary; the actual generator
retains shared component definitions in the Portal raw contract.

The original Web owner first fixes the known Help URL browser assertion. MODEL
then rechecks/publishes its domain on current main; ARC admits formal schema,
roles and catalog; IAM registers actual dispatch/error admission or normalization;
DEV finalizes public contracts/SDK/Help; Web owns real S-202 and installed E2E.
No migration number is assigned here. model-owner-decisions.json lists concrete
remaining owner work. Preserve the independent 783567ea audit input and its
existing queued Mac entry; this prototype never invokes that or the CLI driver.
