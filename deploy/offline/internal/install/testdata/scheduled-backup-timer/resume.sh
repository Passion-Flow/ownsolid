#!/usr/bin/env bash
set -euo pipefail
if [[ $# != 2 || ! $2 =~ ^[0-9a-f]{40}$ ]]; then
    echo 'Usage: bash resume.sh <clean ownsolid checkout> <verified ARC migration commit>' >&2
    exit 2
fi
source_root=$(cd "$1" && pwd -P)
arc_commit=$2
fixture=$(cd "$(dirname "$0")" && pwd -P)
: "${OWNSOLID_TIMER_EVIDENCE_DIRECTORY:?select an existing private evidence directory outside the checkout}"
evidence=$(cd "$OWNSOLID_TIMER_EVIDENCE_DIRECTORY" && pwd -P)
case "$evidence/" in "$source_root/"*) echo 'Evidence must stay outside the checkout' >&2; exit 2 ;; esac
python3 - "$evidence" <<'PYEVIDENCE'
import os
from pathlib import Path
import stat
import sys
info = Path(sys.argv[1]).stat()
if info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) & 0o077:
    raise SystemExit("Evidence directory must be owned by the caller with mode 0700")
PYEVIDENCE
host=${OWNSOLID_TIMER_HOST:-ownsolid-cpu}
git -C "$source_root" merge-base --is-ancestor "$arc_commit" HEAD
migration=services/control/migrations/postgresql/000058_audit_chain_head_scope
for suffix in up down; do
    migration_file="$migration.$suffix.sql"
    if ! git -C "$source_root" diff-tree --no-commit-id --name-only -r "$arc_commit" -- "$migration_file" |
            grep -Fxq "$migration_file" ||
        ! git -C "$source_root" cat-file -e "$arc_commit:$migration_file" ||
        ! git -C "$source_root" diff --quiet "$arc_commit" HEAD -- "$migration_file"; then
        echo 'The selected commit must carry the accepted ARC 000058 migration and match this checkout' >&2
        exit 75
    fi
done
test -z "$(git -C "$source_root" status --porcelain)"
source_commit=$(git -C "$source_root" rev-parse HEAD)
evidence=$(mktemp -d "$evidence/rel108.XXXXXXXX")
printf 'EVIDENCE_DIRECTORY=%s\n' "$evidence"
work=$(mktemp -d)
remote=
cleanup() {
    local status=$?
    if [[ -n $remote ]]; then
        ssh -o BatchMode=yes -o ConnectTimeout=12 "$host" "if test -d '$remote'; then rm -r -- '$remote'; fi" || true
    fi
    rm -r -- "$work"
    exit "$status"
}
trap cleanup EXIT
mkdir "$work/inputs"
python3 - "$source_root" "$fixture" "$work/overlay.json" <<'PY'
import json
from pathlib import Path
import sys
root, fixture, out = map(Path, sys.argv[1:])
path = root / "deploy/offline/internal/install/materialize_timer_fixture_test.go"
out.write_text(json.dumps({"Replace": {str(path): str(fixture / "materialize_test.go")}}))
PY
export GOOS=linux GOARCH=amd64 CGO_ENABLED=0
go -C "$source_root/deploy/offline" test -c -trimpath -overlay "$work/overlay.json" -o "$work/inputs/materialize.test" ./internal/install
go -C "$source_root/deploy/offline" build -trimpath -o "$work/inputs/ownsolid-install" ./cmd/install
go -C "$source_root/services/control" build -trimpath -o "$work/inputs/control" ./cmd/server
go -C "$source_root/tests/e2e" build -trimpath -o "$work/inputs/egress-recorder" ./cmd/egress-recorder
go build -trimpath -o "$work/inputs/probe" "$fixture/probe.go"
cp "$source_root/deploy/systemd/"ownsolid-backup.{service,timer} "$work/inputs/"
cp "$fixture/"{timer-acceptance,host-acceptance}.sh "$work/inputs/"
printf 'source_commit=%s\narc_commit=%s\nplatform=linux/amd64\npostgres_image=%s\n' "$source_commit" "$arc_commit" \
    'pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47' > "$work/inputs/build-inputs.txt"
(
    cd "$work/inputs"
    shasum -a 256 build-inputs.txt control egress-recorder probe host-acceptance.sh materialize.test \
        ownsolid-backup.service ownsolid-backup.timer ownsolid-install timer-acceptance.sh > SHA256SUMS
)
test "$(git -C "$source_root" rev-parse HEAD)" = "$source_commit"
test -z "$(git -C "$source_root" status --porcelain)"
stamp=$(date -u '+%Y%m%dT%H%M%SZ')
cp "$work/inputs/SHA256SUMS" "$evidence/$stamp-sha256.txt"
COPYFILE_DISABLE=1 tar --no-xattrs -C "$work/inputs" -czf "$work/inputs.tar.gz" .
cp "$work/inputs.tar.gz" "$evidence/$stamp-inputs.tar.gz"
remote=$(ssh -o BatchMode=yes -o ConnectTimeout=12 "$host" 'umask 077; mktemp -d /dev/shm/lane-del-rel108-success.XXXXXXXX')
case "$remote" in /dev/shm/lane-del-rel108-success.????????) ;; *) remote=; exit 1 ;; esac
ssh -o BatchMode=yes -o ConnectTimeout=12 "$host" "tar -xzf - -C '$remote' && chmod 0755 '$remote'" < "$work/inputs.tar.gz"
remote_inputs=$remote
remote=
ssh -o BatchMode=yes -o ConnectTimeout=12 -o ServerAliveInterval=5 -o ServerAliveCountMax=3 "$host" \
    "bash '$remote_inputs/host-acceptance.sh' '$remote_inputs'" 2>&1 | tee "$evidence/$stamp-linux.log"
