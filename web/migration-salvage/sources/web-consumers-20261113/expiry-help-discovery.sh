#!/bin/bash

# OWNSOLID_OLD_MAC_EPOCH_CLOSED
printf '%s\n' 'Old Mac admission epoch is closed; await coordinated activation.' >&2
exit 78
set -euo pipefail
root="$1"
run="$2"
export PYTHON="$(uv python find 3.12)" PYTHONDONTWRITEBYTECODE=1
export VITEST_MAX_WORKERS=2 npm_config_offline=true OWNSOLID_PLAYWRIGHT_PORT_OFFSET=22666
unset TMOUT PS1
python3 "$root/expiry-help-discovery.py"
cd "/Users/passion./Documents/Passion's WorkSpace/Development/lane-web/web"
pnpm exec prettier --write apps/console/e2e/models.spec.ts packages/help/locales/en/console/model-library.md > "$run/format.log" 2>&1
cd apps/console
set +e
PLAYWRIGHT_JSON_OUTPUT_NAME="$run/old-build.json" pnpm exec playwright test models.spec.ts -g 'expired import replay opens' --workers=1 --reporter=json > "$run/old-build.log" 2>&1
baseline=$?
set -e
python3 - "$run" "$baseline" <<'VERIFY'
from pathlib import Path
import json,sys
r=Path(sys.argv[1]);data=json.loads((r/'old-build.json').read_text())
assert sys.argv[2]=='1'
assert data['stats']['expected']==1 and data['stats']['unexpected']==1 and data['stats']['flaky']==0 and data['stats']['skipped']==0,data['stats']
assert 'Model library' in (r/'old-build.log').read_text()
print('Old English heading failed discoverability; Chinese positive control passed')
VERIFY
bash "$root/expiry-target.sh" "$run/target"
cd ../../..
git diff --check
git diff --binary > "$run/self-review.diff"
python3 - "$run" <<'VERIFY'
import json,sys
from pathlib import Path
r=Path(sys.argv[1])
for name,expected,skipped in [('models.json',44,3),('states.json',8,0)]:
 s=json.loads((r/'target'/name).read_text())['stats']
 assert s['expected']==expected and s['skipped']==skipped and s['unexpected']==0 and s['flaky']==0,s
VERIFY
git add docs/research/model-import-cleanup.md web/apps/console/e2e/models.spec.ts web/packages/help/locales/en/console/model-library.md web/packages/help/src/gen/locales/en/console.json web/packages/help/src/gen/locales/en/console/model-library.json
git commit -m "MOD-121: Find expiry help by its visible lifecycle term" -m "Use the existing heading index without stemming or search-scope changes. The pinned old production heading fails the English expired query while Chinese remains a positive control. Verify check, units, 44 model browser cases with three unchanged skips and eight state checks. Installed expiry remains open."
git fetch origin
git rebase origin/main
bash "$root/consumer-full-clean.sh" "$run/full"
