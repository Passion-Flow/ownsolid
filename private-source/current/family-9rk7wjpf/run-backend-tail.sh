#!/bin/bash
exit 75 # LANE_M_OLD_GATE_EPOCH_CLOSED
set -euo pipefail
umask 022
proof_root="$(cd "$(dirname "$0")" && pwd -P)"
proof="$(mktemp -d "$proof_root/tail.XXXXXXXX")"
printf '%s\n' "$proof" > "$proof_root/latest-tail"
cp "$proof_root/preflight.py" "$proof/preflight.py"
cd "$1"
python_path="$(uv python find 3.12)"
export PATH="$(dirname "$python_path"):$PATH" PYTHON="$python_path"
test -z "$(git status --porcelain)"
test ! -e tools/audit-verify/audit-verify
volume=""
volume_name="om$(openssl rand -hex 3)"
image="$proof/test-volume.sparseimage"
cleanup() {
 code=$?
 trap - EXIT
 if test -f tools/audit-verify/audit-verify; then mv tools/audit-verify/audit-verify "$proof/audit-verify"; fi
 if test -n "$volume" && mount | grep -F " on $volume " >/dev/null; then
  hdiutil detach "$volume" > "$proof/volume-detach.log" 2>&1 || code=1
 fi
 printf '%s\n' "$code" > "$proof/tail-exit-code"
 exit "$code"
}
trap cleanup EXIT
hdiutil create -size 8g -type SPARSE -fs APFS -volname "$volume_name" -o "$image" > "$proof/volume-create.log" 2>&1
hdiutil attach "$image" -nobrowse -plist > "$proof/volume-attach.plist"
volume="$(python3 - "$proof/volume-attach.plist" "$volume_name" <<'PY'
import pathlib,plistlib,sys
data=plistlib.loads(pathlib.Path(sys.argv[1]).read_bytes())
mounts=[e['mount-point'] for e in data['system-entities'] if 'mount-point' in e]
if mounts!=['/Volumes/'+sys.argv[2]]: raise SystemExit('Unexpected owned volume')
print(mounts[0])
PY
)"
mkdir "$volume/t"
export TMPDIR="$volume/t/" GOFLAGS=-p=2 GOMAXPROCS=3 CARGO_BUILD_JOBS=2
python3 "$proof/preflight.py" > "$proof/preflight-1.log"
sleep 5
python3 "$proof/preflight.py" > "$proof/preflight-2.log"
git rev-parse HEAD 'HEAD^{tree}' > "$proof/tail-revision"
printf '%s\n' portal-focus > "$proof/stage"
set +e
pnpm --dir web/apps/portal exec playwright test e2e/help.spec.ts --grep 'keeps a contract discoverable offline and retries its download in en$' --workers=1 --retries=0 --output "$proof/portal-focus-results" > "$proof/portal-focus.log" 2>&1
focus_code=$?
set -e
printf '%s\n' "$focus_code" > "$proof/portal-focus-exit-code"
printf '%s\n' go-test > "$proof/stage"
for module in api pkg/capabilities pkg/license pkg/operatorsocket tools/release/offline tools/dev/simulator deploy/offline services/agent services/control tests/e2e tools/audit-verify tools/cli; do
 (cd "$module" && go test ./... -count=1) >> "$proof/go-test.log" 2>&1
done
printf '%s\n' rust-test > "$proof/stage"
(cd services/gateway && cargo test --locked --offline) > "$proof/rust-test.log" 2>&1
printf '%s\n' web-unit > "$proof/stage"
(cd web && pnpm test) > "$proof/web-unit.log" 2>&1
printf '%s\n' tail-passed > "$proof/stage"
