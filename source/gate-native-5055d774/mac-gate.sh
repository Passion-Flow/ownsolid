#!/usr/bin/env bash
set -euo pipefail
umask 077
root=/Users/passion./.local/state/ownsolid/lane-rel/gate-native-5055d774
repo="$1"
work="$root/work"
sha=5055d774ec6941df008f21f7e69abd48bad3fab0
python=/Users/passion./.local/share/uv/python/cpython-3.12.14-macos-aarch64-none/bin/python3.12
df -h /
vm_stat | head -8
test "$(df -Pk / | awk 'NR==2 {print $4}')" -ge 15728640 || exit 75
if test ! -d "$work"; then
 git -C "$repo" worktree add --detach "$work" "$sha"
fi
cd "$work"
test "$(git rev-parse HEAD)" = "$sha"
test -z "$(git status --porcelain)"
export PATH="$root/bin:$PATH"
export GOPROXY=off GOSUMDB=off GOMAXPROCS=2 GOFLAGS=-p=1 CARGO_BUILD_JOBS=1 CARGO_NET_OFFLINE=true
export CARGO_TARGET_DIR="/Users/passion./.local/state/ownsolid/lane-rel/gate-native-410ed356/rust-target"
export NODE_OPTIONS=--max-old-space-size=4096
go version
rustc --version
node --version
pnpm --version
"$python" --version
printf 'GATE_SOURCE=%s\n' "$sha"
cleanup() {
 test ! -f tools/audit-verify/audit-verify || rm -- tools/audit-verify/audit-verify
}
trap cleanup EXIT
make web-install
make check PYTHON="$python"
echo MAKE_CHECK_PASSED
make build PYTHON="$python"
echo MAKE_BUILD_PASSED
cleanup
test -z "$(git status --porcelain)"
mkdir "$root/client"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=true -trimpath -o "$root/client/ownsolid" ./tools/cli/cmd/ownsolid
go version -m "$root/client/ownsolid" > "$root/client/build-metadata.txt"
file "$root/client/ownsolid"
grep -F 'vcs.revision=5055d774ec6941df008f21f7e69abd48bad3fab0' "$root/client/build-metadata.txt"
grep -F 'vcs.modified=false' "$root/client/build-metadata.txt"
GATE_ROOT="$root" "$python" - <<'PY'
import hashlib,json,os,subprocess
from pathlib import Path
root=Path(os.environ['GATE_ROOT'])
work=root/'work'
def sha(p): return hashlib.sha256(p.read_bytes()).hexdigest()
value={'format_version':1,'source_revision':subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip(),
'source_modified':False,'os':'linux','arch':'amd64','binary_sha256':sha(root/'client/ownsolid'),
'build_host':'darwin/arm64','go_version':subprocess.check_output(['go','version'],text=True).strip(),
'toolchain_binary_sha256':sha(Path(subprocess.check_output(['which','go'],text=True).strip()).resolve()),
'cli_product_tree':subprocess.check_output(['git','rev-parse','HEAD:tools/cli'],text=True).strip(),
'pinned_inputs':{p:sha(work/p) for p in ['go.work','tools/cli/go.mod','tools/cli/go.sum']},
'flags':['-buildvcs=true','-trimpath','CGO_ENABLED=0','GOOS=linux','GOARCH=amd64','GOFLAGS=-p=1'],
'network_scope':'Go proxy/sumdb, Cargo network and every pnpm install disabled; no runtime zero-egress conclusion',
'full_gate_host':'darwin/arm64','full_check_passed':True,'full_build_passed':True,
'installed_acceptance':False,'native_linux_startup_run':False}
(root/'client/client-manifest.json').write_text(json.dumps(value,indent=2)+'\n')
print(json.dumps({'client_sha256':value['binary_sha256'],'source':value['source_revision']}))
PY
test "$(git rev-parse HEAD)" = "$sha"
test -z "$(git status --porcelain)"
echo EXACT_COMBINED_CLI_MAC_GATE_PASSED
