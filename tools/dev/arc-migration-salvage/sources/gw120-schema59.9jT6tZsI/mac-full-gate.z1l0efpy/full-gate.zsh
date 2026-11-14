set -euo pipefail
umask 022
arc_run=$1
arc_worktree=$2
arc_lock=/private/tmp/ownsolid-workstation-full-gate.lock
cd "$arc_worktree"
test "$(cat "$arc_lock/owner")" = "$arc_run"
test "$(git branch --show-current)" = codex/gw120-schema59-integration
test -z "$(git status --porcelain)"
git rev-parse HEAD HEAD^{tree} origin/main > "$arc_run/revision"
arc_mount=$(mktemp -d /private/tmp/osarc.XXXXXXXX)
printf '%s\n' "$arc_mount" > "$arc_run/mountpoint"
cleanup() {
  if mount | grep -F "on $arc_mount (" >/dev/null; then
    hdiutil detach "$arc_mount" >> "$arc_run/cleanup.log" 2>&1 || return 1
  fi
  rmdir "$arc_mount" || return 1
  if [ -f "$arc_lock/owner" ] && [ "$(cat "$arc_lock/owner")" = "$arc_run" ]; then
    rm "$arc_lock/owner"
    rmdir "$arc_lock"
  fi
}
trap cleanup EXIT
hdiutil create -size 8g -fs APFS -type SPARSE -volname ownsolid-arc-gate -o "$arc_run/scratch" > "$arc_run/volume-create.log" 2>&1
hdiutil attach -nobrowse -mountpoint "$arc_mount" "$arc_run/scratch.sparseimage" > "$arc_run/volume-attach.log" 2>&1
mkdir "$arc_mount/t"
chgrp "$(id -g)" "$arc_mount/t"
chmod 700 "$arc_mount/t"
export TMPDIR="$arc_mount/t/"
export PYTHON="$(uv python find 3.12)"
export GOPROXY=off GOSUMDB=off GOMAXPROCS=2 GOFLAGS=-p=1
export CARGO_NET_OFFLINE=true CARGO_BUILD_JOBS=2 RUST_TEST_THREADS=2 VITEST_MAX_WORKERS=2
test "$(stat -f %g "$TMPDIR")" = "$(id -g)"
test ! -e tools/audit-verify/audit-verify
export npm_config_offline=true npm_config_prefer_offline=true
test "$(pnpm config get offline)" = true
date -u > "$arc_run/start"
(cd web && pnpm install --offline --frozen-lockfile --ignore-scripts) > "$arc_run/dependency-bootstrap.log" 2>&1
make check > "$arc_run/check.log" 2>&1
printf 'make check passed\n'
make build > "$arc_run/build.log" 2>&1
printf 'make build passed\n'
if [ -f tools/audit-verify/audit-verify ]; then
  test -z "$(git ls-files -- tools/audit-verify/audit-verify)"
  rm tools/audit-verify/audit-verify
fi
arc_port=$("$PYTHON" - <<'PY'
import random
import socket
from contextlib import ExitStack
for candidate in random.SystemRandom().sample(range(49000, 63000, 2), 80):
    try:
        with ExitStack() as sockets:
            for port in (candidate, candidate + 1):
                for family, host in ((socket.AF_INET, "127.0.0.1"), (socket.AF_INET6, "::1")):
                    sock = sockets.enter_context(socket.socket(family))
                    sock.bind((host, port))
        print(candidate)
        break
    except OSError:
        continue
else:
    raise SystemExit("No private browser ports available")
PY
)
export OWNSOLID_PLAYWRIGHT_PORT_OFFSET=$((arc_port - 4310))
printf '%s %s\n' "$arc_port" "$((arc_port + 1))" > "$arc_run/ports"
cat > "$arc_run/browser-concurrency.mk" <<'MAKE'
test-web-browser: web-install
	@cd web && pnpm --filter ./apps/console run test:browser --workers=4
	@cd web && pnpm --filter ./apps/portal run test:browser --workers=1
MAKE
make -f Makefile -f "$arc_run/browser-concurrency.mk" test > "$arc_run/test.log" 2>&1
printf 'make test passed\n'
git diff --check
test -z "$(git status --porcelain)"
git rev-parse HEAD > "$arc_run/ok"
date -u > "$arc_run/finish"
