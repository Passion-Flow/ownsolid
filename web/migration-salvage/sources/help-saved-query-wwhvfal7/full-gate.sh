#!/bin/bash

# OWNSOLID_OLD_MAC_EPOCH_CLOSED
printf '%s\n' 'Old Mac admission epoch is closed; await coordinated activation.' >&2
exit 78
set -euo pipefail
umask 077
run="$1"
cd '/Users/passion./Documents/Passion'"'"'s WorkSpace/Development/lane-web-acceptance-help-44ab5229'
test -z "$(git status --porcelain)"
test ! -e tools/audit-verify/audit-verify
export PYTHON="$(uv python find 3.12)" PYTHONDONTWRITEBYTECODE=1
export GOPROXY=off GOSUMDB=off GOMAXPROCS=3 GOFLAGS=-p=1
export CARGO_NET_OFFLINE=true CARGO_BUILD_JOBS=2 RUST_TEST_THREADS=2
unset TMOUT PS1
export DEBUG=pw:webserver
export VITEST_MAX_WORKERS=2 npm_config_offline=true OWNSOLID_PLAYWRIGHT_PORT_OFFSET=26366
if lsof -nP -iTCP:30676 -iTCP:30677 -sTCP:LISTEN; then exit 1; fi
if test -d /private/tmp/ownsolid-macos-full-gate.lock; then exit 1; fi
if test -e /private/tmp/ownsolid-macos-full-gate.lock && lsof -nP /private/tmp/ownsolid-macos-full-gate.lock; then exit 1; fi
ps -axo pid,ppid,etime,%cpu,comm > "$run/preflight-processes"
caffeinate -i -w "$$" &
awake=$!
web_mount="$(mktemp -d /private/tmp/ow-hf-XXXXXX)"
cleanup() {
  status=$?
  if test "$status" -eq 0 && test "$(cat "$run/stage" 2>/dev/null || true)" != complete; then status=1; fi
  trap - EXIT
  kill "$awake" 2>/dev/null || true
  if mount | rg -F " on $web_mount (" >/dev/null; then
    hdiutil detach "$web_mount" >> "$run/cleanup.log" 2>&1 || status=1
  fi
  if test -d "$web_mount" && ! mount | rg -F " on $web_mount (" >/dev/null; then
    rmdir "$web_mount" || status=1
    if test -f "$run/scratch.sparseimage"; then rm "$run/scratch.sparseimage"; fi
  fi
  if test -f tools/audit-verify/audit-verify; then
    "$PYTHON" - <<'CLEAN'
from pathlib import Path
p=Path('tools/audit-verify/audit-verify')
with p.open('rb') as stream:
    assert stream.read(4)==bytes.fromhex('cffaedfe')
p.unlink()
CLEAN
  fi
  printf '%s\n' "$status" > "$run/exit-status"
  date -u > "$run/finish"
  exit "$status"
}
trap cleanup EXIT
git rev-parse HEAD > "$run/source"
git rev-parse HEAD^{tree} > "$run/source-tree"
df -h . > "$run/preflight-space"
vm_stat > "$run/preflight-memory"
"$PYTHON" - <<'SPACE'
import shutil
if shutil.disk_usage('.').free < 15 * 1024**3:
    raise SystemExit('Under 15 GiB free; no heavy gate started')
SPACE
hdiutil create -size 12g -type SPARSE -fs APFS -volname ownsolid-help-full "$run/scratch" > "$run/volume-create.log"
hdiutil attach -nobrowse -owners on -mountpoint "$web_mount" "$run/scratch.sparseimage" > "$run/volume-attach.log"
mkdir -m 700 "$web_mount/tmp"
export TMPDIR="$web_mount/tmp/"
df -h "$TMPDIR" > "$run/scratch-space"
printf 'check\n' > "$run/stage"
make check > "$run/check.log" 2>&1
printf 'make check passed\n'
printf 'build\n' > "$run/stage"
make build > "$run/build.log" 2>&1
printf 'make build passed\n'
if test -f tools/audit-verify/audit-verify; then
  "$PYTHON" - <<'BUILT'
from pathlib import Path
p=Path('tools/audit-verify/audit-verify')
with p.open('rb') as stream: assert stream.read(4)==bytes.fromhex('cffaedfe')
p.unlink()
BUILT
fi
"$PYTHON" /Users/passion./.local/state/ownsolid/web-scopes/help-saved-query-wwhvfal7/verify-full-assets.py "$run"
printf 'test\n' > "$run/stage"
(umask 022; make -f Makefile -f "$run/workers.mk" test WEB_GATE_DIR="$run") > "$run/test.log" 2>&1
printf 'make test with full browser suites and worker caps passed\n'
git diff --check
test -z "$(git status --porcelain)"
printf 'complete\n' > "$run/stage"
