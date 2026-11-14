#!/bin/bash

# OWNSOLID_OLD_MAC_EPOCH_CLOSED
printf '%s\n' 'Old Mac admission epoch is closed; await coordinated activation.' >&2
exit 78
set -euo pipefail
root="$1"
cd "/Users/passion./Documents/Passion's WorkSpace/Development/lane-web"
export npm_config_offline=true OWNSOLID_PLAYWRIGHT_PORT_OFFSET=25666
unset TMOUT PS1
export DEBUG=pw:webserver
if lsof -nP -iTCP:29976 -sTCP:LISTEN; then exit 1; fi
(cd web/apps/console; pnpm exec playwright test e2e/node-page.spec.ts --grep 'Activity says when there is none' --workers=1 --reporter=json --output="$root/node-focused-results") > "$root/node-focused.json" 2> "$root/node-focused.log"
python3 - "$root/node-focused.json" <<'COUNT'
import json,sys
report=json.load(open(sys.argv[1]));assert report['stats']['expected']==1 and report['stats']['unexpected']==0 and report['stats']['skipped']==0,report['stats']
COUNT
bash "$root/run-full-and-publish.sh" "$root"
