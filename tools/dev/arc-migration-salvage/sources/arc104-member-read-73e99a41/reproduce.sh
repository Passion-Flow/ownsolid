#!/bin/bash
# Usage: bash reproduce.sh /absolute/path/to/lane-arc
set -euo pipefail
umask 077
dir=$(cd -- "$(dirname -- "$0")" && pwd -P)
worktree=$(cd -- "$1" && pwd -P)
image=sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47
[[ $(docker image inspect -f '{{.Id}} {{.Os}}/{{.Architecture}}' "$image") == "$image linux/arm64" ]]
run=$(mktemp -d "$dir/run.XXXXXX")
printf '%s\n' "$run" | tee "$dir/latest-run"
name=arc104-member-read-$(basename "$run" | cut -d. -f2 | tr '[:upper:]' '[:lower:]')
started=
cleanup() {
    rc=$?
    trap - EXIT
    if [[ -n $started ]]; then
        docker logs --timestamps "$name" > "$run/postgres.log" 2>&1 || rc=1
        docker inspect -f '{{.HostConfig.NetworkMode}} {{.HostConfig.Memory}} {{.State.OOMKilled}}' "$name" > "$run/container.txt" || rc=1
        docker rm -f -v "$name" >/dev/null || rc=1
    fi
    rm -f "$run/pg-password" "$run/dsn"
    printf '%s\n' "$rc" > "$run/exit"
    exit "$rc"
}
trap cleanup EXIT
python3 - "$dir" "$worktree" "$run" <<'PY'
import json,secrets,sys
from pathlib import Path
src,repo,out=map(Path,sys.argv[1:])
password=secrets.token_hex(24)
(out/'pg-password').write_text(password+'\n')
(out/'dsn').write_text('postgres://postgres:'+password+'@127.0.0.1:5432/arc104_members_test?sslmode=disable\n')
virtual=repo/'services/control/internal/modules/cellclusters/arc104_member_read_fault_test.go'
(out/'overlay.json').write_text(json.dumps({'Replace':{str(virtual):str(src/'member_read_fault_test.go')}}))
PY
git -C "$worktree" rev-parse HEAD > "$run/source-head"
GOPROXY=off GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go -C "$worktree/services/control" test \
    -tags repository_integration -overlay "$run/overlay.json" -c -o "$run/member-read.test" ./internal/modules/cellclusters
docker run -d --pull=never --network none --memory 512m --cpus 1 --pids-limit 128 --name "$name" \
    --mount "type=bind,src=$run,dst=/fixture,readonly" -e POSTGRES_PASSWORD_FILE=/fixture/pg-password \
    -e POSTGRES_DB=arc104_members_test "$image" -c listen_addresses=127.0.0.1 -c log_connections=on \
    -c log_disconnections=on -c log_lock_waits=on -c deadlock_timeout=50ms \
    -c log_min_error_statement=panic -c log_error_verbosity=terse -c 'log_line_prefix=%m [%p] %a %e ' >/dev/null
started=1
for i in $(seq 1 40); do
    if docker exec "$name" pg_isready -q -h 127.0.0.1 -d arc104_members_test; then break; fi
    sleep 1
done
docker exec "$name" pg_isready -h 127.0.0.1 -d arc104_members_test
docker exec --user postgres "$name" psql -X -v ON_ERROR_STOP=1 -d arc104_members_test \
    -c "CREATE EXTENSION vector WITH SCHEMA public VERSION '0.8.7'"
docker exec --user 0 -e PROJECT_ONE_TEST_DSN_FILE=/fixture/dsn "$name" /fixture/member-read.test \
    -test.run '^TestARC104MembersCommitDiagnosis$' -test.v -test.timeout=90s -test.count=3 2>&1 | tee "$run/test.log"
