#!/bin/bash

# OWNSOLID_OLD_MAC_EPOCH_CLOSED
printf '%s\n' 'Old Mac admission epoch is closed; await coordinated activation.' >&2
exit 78
set -euo pipefail
umask 077
run="$1"
cd "/Users/passion./Documents/Passion's WorkSpace/Development/lane-web"
export PYTHON="$(uv python find 3.12)" PYTHONDONTWRITEBYTECODE=1
export GOPROXY=off GOSUMDB=off GOMAXPROCS=3 GOFLAGS=-p=1
export CARGO_NET_OFFLINE=true CARGO_BUILD_JOBS=2 RUST_TEST_THREADS=2
export npm_config_offline=true VITEST_MAX_WORKERS=2 OWNSOLID_PLAYWRIGHT_PORT_OFFSET=24666
unset TMOUT PS1
export DEBUG=pw:webserver
if lsof -nP -iTCP:28976 -iTCP:28977 -sTCP:LISTEN; then exit 1; fi
df -h . > "$run/preflight-space"
vm_stat > "$run/preflight-memory"
caffeinate -i -w "$$" &
awake=$!
finish() { status=$?; if test "$status" -eq 0 && test "$(cat "$run/target-stage" 2>/dev/null || true)" != complete; then status=1; fi; trap - EXIT; kill "$awake" 2>/dev/null || true; printf '%s\n' "$status" > "$run/target-exit-status"; exit "$status"; }
trap finish EXIT
printf 'format\n' > "$run/target-stage"
"$PYTHON" - "$run" <<'FORMAT'
from pathlib import Path
import json,subprocess,sys
run=Path(sys.argv[1])
paths=[p.removeprefix('web/') for p in json.loads((run/'format-paths.json').read_text())]
with (run/'format.log').open('w') as log:
 subprocess.run(['pnpm','exec','prettier','--write',*paths],cwd='web',stdout=log,stderr=subprocess.STDOUT,check=True)
FORMAT
printf 'negative-control\n' > "$run/target-stage"
"$PYTHON" - "$run" <<'BEFORE'
from pathlib import Path
import hashlib,json,sys
r=Path(sys.argv[1])
for name,digest in json.loads((r/'old-console-dist-manifest.json').read_text()).items():
 assert hashlib.sha256((Path('web/apps/console/dist')/name).read_bytes()).hexdigest()==digest,name
BEFORE
set +e
(cd web/apps/console; pnpm exec playwright test e2e/audit-read-recovery.spec.ts --grep 'waits for deliberate recovery after lost-response in en$' --workers=2 --reporter=json --output="$run/old-build-results") > "$run/old-build.json" 2> "$run/old-build.log"
old_status=$?
set -e
printf '%s\n' "$old_status" > "$run/old-build-exit-status"
"$PYTHON" - "$run" <<'NEGATIVE'
from pathlib import Path
import json,re,sys
r=Path(sys.argv[1]); assert (r/'old-build-exit-status').read_text().strip()=='1'; report=json.loads((r/'old-build.json').read_text());stats=report['stats']
assert stats['unexpected']==2 and stats['expected']==0 and stats['skipped']==0,stats
errors=[]
def walk(suites):
 for suite in suites:
  for spec in suite.get('specs',[]):
   for test in spec['tests']:
    for result in test['results']:
     errors.extend(re.sub(r'\x1b\[[0-9;]*m', '', e.get('message','')) for e in result.get('errors',[]))
  walk(suite.get('suites',[]))
walk(report['suites'])
assert len(errors)==2 and all('toHaveLength' in e and 'Expected length: 1' in e for e in errors),errors
(r/'old-build-disposition.json').write_text(json.dumps({'expectedRegression':True,'stats':stats,'failure':'More than one audited request after a lost response; no new UI-message assertion was needed'},indent=2)+'\n')
NEGATIVE
printf 'generate\n' > "$run/target-stage"
make generate > "$run/generate.log" 2>&1
printf 'check\n' > "$run/target-stage"
make check > "$run/check.log" 2>&1
printf 'make check passed\n'
printf 'unit\n' > "$run/target-stage"
(cd web; pnpm --filter @ownsolid/console --filter @ownsolid/ui --filter @ownsolid/i18n --filter @ownsolid/help test) > "$run/unit.log" 2>&1
printf 'units passed\n'
printf 'build\n' > "$run/target-stage"
(cd web; pnpm --filter @ownsolid/console build) > "$run/build.log" 2>&1
printf 'target\n' > "$run/target-stage"
(cd web/apps/console; pnpm exec playwright test e2e/audit-read-recovery.spec.ts e2e/audit.spec.ts e2e/resource-pages.spec.ts --workers=4 --reporter=json --output="$run/target-results") > "$run/target.json" 2> "$run/target.log"
printf 'target browser checks passed\n'
printf 'states\n' > "$run/target-stage"
(cd web/apps/console; pnpm exec playwright test e2e/accessibility.spec.ts e2e/keyboard.spec.ts e2e/catalog-failure.spec.ts --grep 'audit (trail|activity) with (uncertain|partial) read recovery' --workers=4 --reporter=json --output="$run/state-results") > "$run/states.json" 2> "$run/states.log"
printf 'page state checks passed\n'
printf 'complete\n' > "$run/target-stage"
