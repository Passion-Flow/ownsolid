#!/bin/bash
exit 75 # LANE_M_OLD_GATE_EPOCH_CLOSED
set -euo pipefail
umask 022
proof_root="$(cd "$(dirname "$0")" && pwd -P)"
proof="$(mktemp -d "$proof_root/gate.XXXXXXXX")"
cp "$proof_root/preflight.py" "$proof/preflight.py"
printf '%s\n' "$proof" > "$proof_root/latest-gate"
printf 'Gate evidence directory %s\n' "$proof"
lock=/private/tmp/ownsolid-workstation-full-gate.lock
cd "/Users/passion./Documents/Passion's WorkSpace/Development/lane-m"
python_path="$(uv python find 3.12)"
export PATH="$(dirname "$python_path"):$PATH"
export PYTHON="$python_path"
test -z "$(git status --porcelain)"
test "$(git branch --show-current)" = lane-m
test ! -e tools/audit-verify/audit-verify
test ! -e /private/tmp/ownsolid-macos-full-gate.lock
mkdir "$lock" || exit 75
printf '%s\n' "$proof" > "$lock/owner"
volume=""
volume_name="om$(openssl rand -hex 3)"
image="$proof/test-volume.sparseimage"
cleanup() {
 code=$?
 trap - EXIT
 if test -f tools/audit-verify/audit-verify; then
  mv tools/audit-verify/audit-verify "$proof/audit-verify"
 fi
 if test -n "$volume" && mount | grep -F " on $volume " >/dev/null; then
  hdiutil detach "$volume" > "$proof/volume-detach.log" 2>&1 || code=1
 fi
 if test "$code" != 0 && test "$(cat "$lock/owner" 2>/dev/null)" = "$proof"; then
  rm "$lock/owner"
  rmdir "$lock"
 fi
 if test "$code" = 0; then
  printf '%s\n' "$proof" > "$proof/gate-passed-lock-held"
 fi
 printf '%s\n' "$code" > "$proof/gate-exit-code"
 exit "$code"
}
trap cleanup EXIT
hdiutil create -size 8g -type SPARSE -fs APFS -volname "$volume_name" -o "$image" > "$proof/volume-create.log" 2>&1
hdiutil attach "$image" -nobrowse -plist > "$proof/volume-attach.plist"
volume="$(python3 - "$proof/volume-attach.plist" "$volume_name" <<'PY'
import pathlib,plistlib,sys
data=plistlib.loads(pathlib.Path(sys.argv[1]).read_bytes())
mounts=[entry['mount-point'] for entry in data['system-entities'] if 'mount-point' in entry]
if len(mounts)!=1 or mounts[0]!='/Volumes/'+sys.argv[2]:
 raise SystemExit('Unexpected test volume mount')
print(mounts[0])
PY
)"
mkdir "$volume/t"
export TMPDIR="$volume/t/"
export GOFLAGS=-p=2 GOMAXPROCS=3 CARGO_BUILD_JOBS=2
python3 "$proof/preflight.py" > "$proof/preflight-1.log"
sleep 5
python3 "$proof/preflight.py" > "$proof/preflight-2.log"
python3 - <<'PY' > "$proof/fetch.log" 2>&1
import os,subprocess,urllib.parse
args=['git','-c','http.version=HTTP/1.1']
proxy=os.environ.get('OWNSOLID_GIT_HTTP_PROXY')
if proxy:
 parsed=urllib.parse.urlsplit(proxy)
 if parsed.scheme!='http' or parsed.hostname not in ('127.0.0.1','localhost') or parsed.username or parsed.password or not parsed.port:
  raise SystemExit('A local unauthenticated proxy is required')
 args+=['-c','http.proxy='+proxy]
subprocess.run(args+['fetch','origin'],check=True,timeout=45)
PY
upstream="$(git rev-parse origin/main)"
git rebase "$upstream" > "$proof/rebase.log" 2>&1
git diff --exit-code 992e979f HEAD -- services/control/internal/modules/models services/control/internal/modules/deployments
git diff --check "$upstream"...HEAD
git rev-parse HEAD "$upstream" 'HEAD^{tree}' > "$proof/gate-revision"
git ls-tree --name-only HEAD:services/control/migrations/postgresql > "$proof/migration-lineage"
python3 tools/dev/roadmap_progress.py
test -z "$(git status --porcelain)"
printf '%s\n' check > "$proof/stage"
make check > "$proof/check.log" 2>&1
printf '%s\n' build > "$proof/stage"
make build > "$proof/build.log" 2>&1
printf '%s\n' test > "$proof/stage"
make test > "$proof/test.log" 2>&1
printf '%s\n' archive-unit > "$proof/stage"
GOPROXY=off GOSUMDB=off go -C services/control test -p=1 ./internal/modules/models -run '^(TestReviewArchiveRejectsUnsafeMembersBeforeExtraction|TestArchiveReviewReadsAcrossChunksWithoutAdvancingStream)$' -count=1 -v > "$proof/archive-unit.log" 2>&1
printf '%s\n' linux-build > "$proof/stage"
export GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOPROXY=off GOSUMDB=off
go -C services/control test -tags 'repository_integration,model_integration' -p=2 -c -o "$proof/models.test" ./internal/modules/models > "$proof/models-compile.log" 2>&1
go -C services/control test -p=2 -c -o "$proof/models-http.test" ./internal/modules/models/http > "$proof/models-http-compile.log" 2>&1
go -C services/control test -p=2 -c -o "$proof/deployments.test" ./internal/modules/deployments > "$proof/deployments-compile.log" 2>&1
git archive --format=tar HEAD | gzip -n > "$proof/source.tar.gz"
shasum -a 256 "$proof/"*.test "$proof/source.tar.gz" > "$proof/inputs.sha256"
cp "$proof_root/run-linux-acceptance.sh" "$proof/run-linux-acceptance.sh"
tar -czf "$proof/acceptance-inputs.tar.gz" -C "$proof" models.test models-http.test deployments.test source.tar.gz inputs.sha256 run-linux-acceptance.sh
printf '%s\n' passed > "$proof/stage"
