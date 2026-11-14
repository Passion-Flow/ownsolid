#!/usr/bin/env bash

# OWNSOLID_OLD_MAC_EPOCH_CLOSED
printf '%s\n' 'Old Mac admission epoch is closed; await coordinated activation.' >&2
exit 78
set -euo pipefail
umask 077
run="$1"
lane="$2"
caffeinate -i -w "$$" &
awake=$!
cleanup() {
  status=$?
  trap - EXIT
  kill "$awake" 2>/dev/null || true
  printf '%s\n' "$status" > "$run/baseline-window-exit"
  exit "$status"
}
trap cleanup EXIT
cd "$lane"
git rev-parse HEAD > "$run/baseline-revision"
git diff --binary > "$run/baseline-test.patch"
cp web/packages/shell/testing/contract-download.ts "$run/baseline-contract-download.ts"
git diff --quiet -- web/packages/help/src/index.ts web/packages/shell/src/help/api-page.tsx
export npm_config_offline=true OWNSOLID_PLAYWRIGHT_PORT_OFFSET=12666
if lsof -nP -iTCP:16976 -sTCP:LISTEN; then exit 1; fi
set +e
(cd web/apps/console && PLAYWRIGHT_JSON_OUTPUT_FILE="$run/baseline-report.json" pnpm exec playwright test e2e/help.spec.ts --workers=1 --grep 'keeps a contract discoverable offline.*in en' --output "$run/baseline-results" --reporter=line,json > "$run/baseline.log" 2>&1)
result=$?
set -e
printf '%s\n' "$result" > "$run/baseline-test-exit"
test "$result" -eq 1
python3 - "$run" <<'PY'
from pathlib import Path
import json,sys
run=Path(sys.argv[1])
data=json.loads((run/'baseline-report.json').read_text())
assert data['stats']['unexpected']==1 and data['stats']['expected']==0, data['stats']
def errors(value):
    if isinstance(value,list):
        for item in value: yield from errors(item)
    elif isinstance(value,dict):
        if 'message' in value and isinstance(value['message'],str): yield value['message']
        for item in value.values(): yield from errors(item)
messages='\n'.join(errors(data))
assert 'Save the contract: identity' in messages and 'toBeVisible' in messages, messages[:2000]
print('Controlled old-loader regression reproduced: the identity download link is absent while offline')
PY
tail -24 "$run/baseline.log"
