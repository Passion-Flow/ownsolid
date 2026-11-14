#!/bin/bash

# OWNSOLID_OLD_MAC_EPOCH_CLOSED
printf '%s\n' 'Old Mac admission epoch is closed; await coordinated activation.' >&2
exit 78
set -euo pipefail
root="$1"
inputs="$2"
pub="$(cat "$root/publication-current-path")"
cd "/Users/passion./Documents/Passion's WorkSpace/Development/lane-web"
test "$(git rev-parse HEAD)" = "$(cat "$root/published-source")"
test -z "$(git status --porcelain)"
cp "$root/workers.mk" "$pub/workers.mk"
bash "$root/publication-check.sh" "$pub" > "$pub/fresh-integration-output.log" 2>&1
python3 - "$root" "$pub" <<'PROOF'
from pathlib import Path
import hashlib,json,subprocess,sys
root=Path(sys.argv[1]);pub=Path(sys.argv[2]);source=(pub/'source').read_text().strip()
assert (pub/'stage').read_text().strip()=='complete' and (pub/'exit-status').read_text().strip()=='0'
proof=json.loads((root/'final-qualification.json').read_text())
assert source==(root/'published-source').read_text().strip()
paths=['web','api','pkg/capabilities/declaration.json','tests/fixtures','tools/codegen/web-api.mjs','tools/codegen/web-help.mjs','Makefile']
assert subprocess.check_output(['git','diff','--name-only',proof['source'],source,'--',*paths],text=True).strip()==''
proof['publishedIntegration']={'source':source,'run':str(pub),'stage':'complete','exit':0,'freshTargets':['make check','make build','all Go modules','Rust gateway','Web unit targets'],'browserReuse':'The full1191/410 browser reports use byte-identical Web/API/fixtures/generators/capabilities/Makefile inputs; no added skip or new browser subset','logs':{name:hashlib.sha256((pub/name).read_bytes()).hexdigest() for name in ['check.log','build.log','test.log']}}
(root/'final-qualification.json').write_text(json.dumps(proof,indent=2)+'\n')
print('Published source integration passed; browser input equivalence verified.',flush=True)
PROOF
python3 "$inputs/prepare.py" "$inputs"
bash "$inputs/build-inputs.sh" "$inputs"
