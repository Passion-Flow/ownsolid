#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")" && pwd -P)
deadline=$((SECONDS + 210))
quiet=0
while (( quiet < 3 )); do
  if ps -axo command= | grep -E '^([^ ]*/)?make (check|build|test)( |$)' >/dev/null; then
    quiet=0
  else
    quiet=$((quiet + 1))
  fi
  if ((SECONDS >= deadline)); then printf 'Mac whole-repository gate remained occupied.\n'; exit 75; fi
  sleep 5
done
test "$(git branch --show-current)" = codex/audit58-catalog
test -z "$(git status --porcelain)"
arc_tmp=$(cd "${TMPDIR:-/tmp}" && pwd -P)
arc_python=$(command -v python3.12)
df -Pk "$arc_tmp" | awk 'NR==2 {percent=$5; sub(/%$/,"",percent); exit !(percent <= 68)}'
git rev-parse HEAD HEAD^{tree} > "$root/revision"
export TMPDIR="$arc_tmp"
for target in check build test; do
  printf 'Running make %s at %s\n' "$target" "$(date '+%H:%M:%S %Z')"
  if ! make "$target" PYTHON="$arc_python" > "$root/$target.log" 2>&1; then
    tail -35 "$root/$target.log"
    exit 1
  fi
  printf 'Passed make %s at %s\n' "$target" "$(date '+%H:%M:%S %Z')"
done
