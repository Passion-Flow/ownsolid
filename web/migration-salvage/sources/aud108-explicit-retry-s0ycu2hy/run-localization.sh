#!/bin/bash

# OWNSOLID_OLD_MAC_EPOCH_CLOSED
printf '%s\n' 'Old Mac admission epoch is closed; await coordinated activation.' >&2
exit 78
set -euo pipefail
run="$1"
cd "/Users/passion./Documents/Passion's WorkSpace/Development/lane-web/web/apps/console"
export npm_config_offline=true OWNSOLID_PLAYWRIGHT_PORT_OFFSET=24666
unset TMOUT PS1
export DEBUG=pw:webserver
if lsof -nP -iTCP:28976 -sTCP:LISTEN; then exit 1; fi
pnpm exec playwright test e2e/localization.spec.ts --grep 'audit (trail|activity) with (uncertain|partial) read recovery' --workers=2 --reporter=json --output="$run/localization-results" > "$run/localization.json" 2> "$run/localization.log"
