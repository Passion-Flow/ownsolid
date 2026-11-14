#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")" && pwd -P)
deadline=$((SECONDS + 300))
quiet=0
while (( quiet < 3 )); do
  if ps -axo command= | grep -E '^([^ ]*/)?make (check|build|test)( |$)' >/dev/null ||
    lsof -nP -iTCP:4310 -iTCP:4311 -sTCP:LISTEN -t >/dev/null; then
    quiet=0
  else
    quiet=$((quiet + 1))
  fi
  if (( quiet == 3 )); then break; fi
  if (( SECONDS >= deadline )); then printf 'Mac whole-repository gate or browser ports remained occupied.\n'; exit 75; fi
  sleep 5
done
test "$(git branch --show-current)" = codex/audit58-integration
test -z "$(git status --porcelain)"
git fetch origin
git rebase origin/main
git rev-parse HEAD HEAD^{tree} origin/main > "$root/revision"
env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOPROXY=off go -C services/control list -deps -test -json ./internal/platform/database ./internal/modules/audit > "$root/control-inputs.json"
env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOPROXY=off go -C tools/audit-verify list -deps -json . > "$root/verifier-inputs.json"
parent=../lane-arc-diagnostics/audit58.dP8vTQ69
python3.12 "$root/compare-inputs.py" "$root" "$parent/clean-archive-proof.gaPWv6Z0/candidate.tar.gz"   ../lane-arc-diagnostics/postgres-handoff-inputs.uoimBPE6 | tee "$root/input-comparison.log"
arc_tmp=$(cd "${TMPDIR:-/tmp}" && pwd -P)
arc_python=$(command -v python3.12)
df -Pk "$arc_tmp" | awk 'NR==2 {percent=$5; sub(/%$/,"",percent); exit !(percent <= 68)}'
export TMPDIR="$arc_tmp" GOPROXY=off GOMAXPROCS=4 GOFLAGS=-p=2 CARGO_BUILD_JOBS=2 CARGO_NET_OFFLINE=true npm_config_offline=true
for target in check build test; do
  printf 'Running make %s at %s\n' "$target" "$(date '+%H:%M:%S %Z')"
  if ! make "$target" PYTHON="$arc_python" > "$root/$target.log" 2>&1; then
    tail -35 "$root/$target.log"
    exit 1
  fi
  printf 'Passed make %s at %s\n' "$target" "$(date '+%H:%M:%S %Z')"
done
