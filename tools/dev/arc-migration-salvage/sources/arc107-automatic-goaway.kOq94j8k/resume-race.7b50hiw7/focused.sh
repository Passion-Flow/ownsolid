set -euo pipefail
run=$1
repo=$2
lock=/private/tmp/ownsolid-workstation-full-gate.lock
mountpoint=$(mktemp -d /private/tmp/osarc.XXXXXXXX)
cleanup() {
 if mount | grep -F "on $mountpoint (" >/dev/null; then hdiutil detach "$mountpoint" >> "$run/cleanup.log" 2>&1;fi
 rmdir "$mountpoint"
 true # The parent owns the existing file lock; never unlink the shared inode.
}
trap cleanup EXIT
trap 'exit 143' TERM INT
hdiutil create -size 8g -fs APFS -type SPARSE -volname ownsolid-arc107-race -o "$run/scratch" > "$run/create.log" 2>&1
hdiutil attach -nobrowse -mountpoint "$mountpoint" "$run/scratch.sparseimage" > "$run/attach.log" 2>&1
mkdir "$mountpoint/t";chgrp "$(id -g)" "$mountpoint/t";chmod 700 "$mountpoint/t"
export TMPDIR="$mountpoint/t/" GOPROXY=off GOSUMDB=off GOMAXPROCS=2 GOFLAGS=-p=1
cd "$repo"
test -z "$(git status --porcelain)"
go -C services/control test -race -count=1 ./internal/platform/httpserver ./internal/platform/agentgateway ./internal/platform/config ./cmd/server > "$run/race.log" 2>&1
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go -C tests/e2e test -c -o "$run/e2e.test" > "$run/e2e-compile.log" 2>&1
test -z "$(git status --porcelain)"
printf 'ARC-107 complete affected-package race suites and Linux amd64 e2e compile passed\n'
