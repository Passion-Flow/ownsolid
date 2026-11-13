#!/bin/bash
set -euo pipefail
root="$1"
cd "$root"
compiler="$(python3 -c 'import json; print(json.load(open("binding.json"))["worktree"])')/web/node_modules/typescript/bin/tsc"
node "$compiler" --noEmit --project tsconfig.json > typecheck.log 2>&1
python3 run.py --selftest > output-selftest.log 2>&1
node input-selftest.mjs > input-selftest.log 2>&1
node node_modules/@playwright/test/cli.js test --config playwright.config.ts --list > discovery.log 2>&1
node node_modules/@playwright/test/cli.js test --config help.config.ts --list > help-discovery.log 2>&1
node node_modules/@playwright/test/cli.js test --config scope.config.ts --list > scope-discovery.log 2>&1
printf 'Installed client typecheck, input selftests and discovery passed; no live run.\n'
