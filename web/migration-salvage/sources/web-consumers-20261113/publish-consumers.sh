#!/bin/bash
set -euo pipefail
root="$1"
cd "/Users/passion./Documents/Passion's WorkSpace/Development/lane-web"
test -z "$(git status --porcelain)"
source="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["source"])' "$root/final-qualification.json")"
run="$(mktemp -d "$root/publication-XXXXXX")"
printf '%s\n' "$run" > "$root/publication-current-path"
git rev-parse HEAD > "$run/before"
git fetch origin > "$run/fetch.log" 2>&1
git rebase origin/main > "$run/rebase.log" 2>&1
git rev-parse origin/main > "$run/base"
git rev-parse HEAD > "$run/rebased-source"
git diff --name-only "$source" HEAD -- web api pkg/capabilities/declaration.json tests/fixtures tools/codegen/web-api.mjs tools/codegen/web-help.mjs Makefile > "$run/browser-input-diff"
if test -s "$run/browser-input-diff"; then
  printf 'Browser inputs changed after rebase; a fresh complete gate is required.\n'
  exit 73
fi
cp "$root/consumer-full-o21j686q/workers.mk" "$run/workers.mk"
bash "$root/publication-check.sh" "$run"
git log --reverse --format='%H %an <%ae> %s' origin/main..HEAD > "$run/original-author-series"
git format-patch --stdout origin/main..HEAD > "$run/original-author-series.patch"
git diff --check
test -z "$(git status --porcelain)"
git push origin HEAD:main > "$run/push.log" 2>&1
git rev-parse HEAD > "$root/published-source"
printf 'Fast-forward publication completed.\n'
