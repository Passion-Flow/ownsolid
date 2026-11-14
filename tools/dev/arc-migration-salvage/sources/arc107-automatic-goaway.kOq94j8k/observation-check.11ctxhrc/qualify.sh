set -euo pipefail
run=$1
repo=$2
mountpoint=$(mktemp -d /private/tmp/osobs.XXXXXXXX)
cleanup() {
 if mount | grep -F "on $mountpoint (" >/dev/null;then hdiutil detach "$mountpoint" >> "$run/cleanup.log" 2>&1;fi
 rmdir "$mountpoint"
}
trap cleanup EXIT
trap 'exit 143' TERM INT
hdiutil create -size 8g -fs APFS -type SPARSE -volname ownsolid-arc107-observer -o "$run/scratch" > "$run/create.log" 2>&1
hdiutil attach -nobrowse -mountpoint "$mountpoint" "$run/scratch.sparseimage" > "$run/attach.log" 2>&1
mkdir "$mountpoint/t";chgrp "$(id -g)" "$mountpoint/t";chmod 700 "$mountpoint/t"
export TMPDIR="$mountpoint/t/" GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GOMAXPROCS=2 GOFLAGS=-p=1
cd "$repo"
test -z "$(git status --porcelain)"
go -C tests/e2e test -race -count=1 -v -run '^TestGateway(ObservationAdmission|ConnectionLogOrdering)$' . > "$run/race.log" 2>&1
go -C tests/e2e vet . > "$run/vet.log" 2>&1
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go -C tests/e2e test -c -o "$run/e2e.test" . > "$run/compile.log" 2>&1
test -z "$(git status --porcelain)"
