#!/usr/bin/env bash
set -euo pipefail
umask 077
work=$1
repo=$2
base=$(cd "$(dirname "$0")" && pwd -P)
lock=/private/tmp/ownsolid-workstation-full-gate.lock
cleanup() {
 if [[ -f "$lock/owner" && $(cat "$lock/owner") == "$work" ]]; then rm "$lock/owner";rmdir "$lock";fi
}
trap cleanup EXIT
cd "$repo"
test "$(git rev-parse HEAD)" = a446ec580cf6dffc63df5d8269518f1ef8175d40
test -z "$(git status --porcelain)"
image=pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47
arch=$(docker image inspect "$image" --format '{{.Architecture}}')
[[ $arch == arm64 || $arch == amd64 ]]
printf 'Formal source %s\nHost %s\nPostgreSQL architecture %s\n' "$(git rev-parse HEAD)" "$(uname -sm)" "$arch"
mkdir -m 700 "$work/predecessor"
git archive 106ca52dd867bcb38f142948a52a1f4af41d1d8e | tar -xf - -C "$work/predecessor"
python3 - "$base" "$work" "$repo" <<'PY'
from pathlib import Path
import hashlib,json,sys
base,p,repo=map(Path,sys.argv[1:])
manifest=json.loads((base/'predecessor-fixtures/fixture-checksums.json').read_text())
for name,digest in manifest.items():
 raw=(base/'predecessor-fixtures'/name).read_bytes();assert hashlib.sha256(raw).hexdigest()==digest
 (p/name).write_bytes(raw);(p/name).chmod(0o600)
(p/'predecessor-input-sha256.json').write_text(json.dumps(manifest,indent=2)+'\n')
for name in ['catalog.sql','formal_restore_matrix_test.go','predecessor_migrate_test.go']: (p/name).write_bytes((base/name).read_bytes())
for prefix,root,name in [('current',repo,'formal_restore_matrix_test.go'),('predecessor',p/'predecessor','predecessor_migrate_test.go')]:
 (p/(prefix+'-overlay.json')).write_text(json.dumps({'Replace':{str(root/'services/control/internal/platform/database'/name):str(p/name)}}))
replay=(base/'replay.sh').read_text()
replay=replay.replace('repo=$work/repo','repo=${1:?exact formal source checkout}').replace('id=$(cat /proc/sys/kernel/random/uuid)',"id=$(uuidgen | tr 'A-Z' 'a-z')")
replay=replay.replace('--mount type=bind,src=/root/lane-arc-audit58.QQe1KRCn/database.test,dst=/predecessor.test,readonly','--mount "type=bind,src=$work/predecessor.test,dst=/predecessor.test,readonly"')
replay=replay.replace('TestAuditCandidateProductionMigratorAfterRestore','TestAcceptedSchema58ProductionMigrator')
marker='docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn -w /repo/services/control/internal/operations/databaseaccess'
assert marker in replay;replay=replay.split(marker)[0]
(p/'replay.sh').write_text(replay)
PY
export GOOS=linux GOARCH=$arch CGO_ENABLED=0 GOPROXY=off GOSUMDB=off GOMAXPROCS=2 GOFLAGS=-p=1
go -C "$work/predecessor/services/control" test -overlay "$work/predecessor-overlay.json" -c -o "$work/predecessor.test" ./internal/platform/database
go -C services/control test -overlay "$work/current-overlay.json" -c -o "$work/database.test" ./internal/platform/database
go -C tools/audit-verify build -o "$work/audit-verify" .
shasum -a 256 "$work/predecessor.test" "$work/database.test" "$work/audit-verify" > "$work/binary-sha256"
bash "$work/replay.sh" "$repo" | tee "$work/matrix.log"
if grep -F -- '--- SKIP' "$work/matrix.log";then exit 1;fi
printf 'All seven formal migration paths and four retained checkpoints passed\n'
