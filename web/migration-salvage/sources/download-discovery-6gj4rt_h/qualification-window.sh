#!/usr/bin/env bash

# OWNSOLID_OLD_MAC_EPOCH_CLOSED
printf '%s\n' 'Old Mac admission epoch is closed; await coordinated activation.' >&2
exit 78
set -euo pipefail
umask 077
run="$1"
lane="$2"
python="$3"
caffeinate -i -w "$$" &
awake=$!
cleanup() {
  status=$?
  trap - EXIT
  kill "$awake" 2>/dev/null || true
  printf '%s\n' "$status" > "$run/qualification-exit"
  exit "$status"
}
trap cleanup EXIT
cd "$lane"
test -z "$(git status --porcelain)"
git rev-parse HEAD > "$run/qualification-revision"
df -h . > "$run/qualification-resources"
export npm_config_offline=true OWNSOLID_PLAYWRIGHT_PORT_OFFSET=12666
if lsof -nP -iTCP:16976 -iTCP:16977 -sTCP:LISTEN; then exit 1; fi
make check PYTHON="$python" > "$run/check.log" 2>&1
(cd web && pnpm build > "$run/build.log" 2>&1)
for app in console portal; do
  (
    cd "web/apps/$app"
    PLAYWRIGHT_JSON_OUTPUT_FILE="$run/$app-report.json" pnpm exec playwright test e2e/help.spec.ts --workers=1 --grep 'keeps a contract discoverable offline' --output "$run/$app-results" --reporter=line,json > "$run/$app.log" 2>&1
  )
  tail -n 12 "$run/$app.log"
done
