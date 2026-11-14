#!/usr/bin/env bash

# OWNSOLID_OLD_MAC_EPOCH_CLOSED
printf '%s\n' 'Old Mac admission epoch is closed; await coordinated activation.' >&2
exit 78
set -euo pipefail
umask 077
run="$1"
lane="$2"
lock=/private/tmp/ownsolid-workstation-full-gate.lock
mkdir "$lock"
printf '%s\n' "$run/help-consumer" > "$lock/owner"
caffeinate -i -w "$$" &
awake=$!
cleanup() {
  status=$?
  trap - EXIT
  kill "$awake" 2>/dev/null || true
  if test "$(cat "$lock/owner" 2>/dev/null)" = "$run/help-consumer"; then rm "$lock/owner"; rmdir "$lock"; fi
  printf '%s\n' "$status" > "$run/help-exit-status"
  exit "$status"
}
trap cleanup EXIT
cd "$lane"
test -z "$(git status --porcelain)"
df -h . > "$run/help-preflight"
git rev-parse HEAD > "$run/help-revision"
export npm_config_offline=true OWNSOLID_PLAYWRIGHT_PORT_OFFSET=12666
if lsof -nP -iTCP:16976 -iTCP:16977 -sTCP:LISTEN; then exit 1; fi
(cd web && pnpm --filter @ownsolid/console build > "$run/help-build.log" 2>&1)
for app in console portal; do
  (
    cd "web/apps/$app"
    PLAYWRIGHT_JSON_OUTPUT_FILE="$run/$app-help-report.json" pnpm exec playwright test e2e/help.spec.ts --workers=1 --output "$run/$app-help-results" --reporter=line,json > "$run/$app-help.log" 2>&1
  )
  tail -n 19 "$run/$app-help.log"
done
