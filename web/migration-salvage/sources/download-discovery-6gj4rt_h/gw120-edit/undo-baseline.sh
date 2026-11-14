#!/bin/bash

# OWNSOLID_OLD_MAC_EPOCH_CLOSED
printf '%s\n' 'Old Mac admission epoch is closed; await coordinated activation.' >&2
exit 78
set -euo pipefail
run="$1"
cd "/Users/passion./Documents/Passion's WorkSpace/Development/lane-web"
export GOPROXY=off GOSUMDB=off GOMAXPROCS=3 GOFLAGS=-p=1 npm_config_offline=true OWNSOLID_PLAYWRIGHT_PORT_OFFSET=12666
scratch="$(mktemp -d /private/tmp/ow-wg-XXXXXX)"
trap 'rmdir "$scratch"' EXIT
(umask 022; export TMPDIR="$scratch/"; cd deploy/offline; go test ./internal/install -run '^(TestRestorePutsBackAMissingModelBeforeTheDatabaseChanges|TestCatalogCompose)' -count=1) > "$run/installer.log" 2>&1
python3 - "$run/dist-manifest.json" <<'CHECK'
from pathlib import Path
import sys,json,hashlib
manifest=json.loads(Path(sys.argv[1]).read_text())['files']
dist=Path('web/apps/console/dist')
assert manifest=={str(p.relative_to(dist)):hashlib.sha256(p.read_bytes()).hexdigest() for p in dist.rglob('*') if p.is_file()}
CHECK
cd web/apps/console
set +e
PLAYWRIGHT_JSON_OUTPUT_FILE="$run/report.json" pnpm exec playwright test e2e/alias-description.spec.ts --grep 'a lost Undo answer' --workers=1 --output "$run/results" --reporter=line,json > "$run/browser.log" 2>&1
status=$?
set -e
printf '%s\n' "$status" > "$run/baseline-exit"
test "$status" -eq 1
