set -euo pipefail
umask 077
run=$1
repo=$2
name=arc-datproto-$(uuidgen | tr 'A-Z' 'a-z')
printf '%s\n' "$name" > "$run/container-name"
cleanup() {
 docker rm -f "$name" >/dev/null 2>&1 || true
 docker volume rm "$name" >/dev/null 2>&1 || true
 rm -f "$run/pg-password" "$run/test-dsn"
}
trap cleanup EXIT
trap 'exit 143' TERM INT
export GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GOMAXPROCS=2 GOFLAGS=-p=1
cd "$repo"
image=pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47
arch=$(docker image inspect "$image" --format '{{.Architecture}}')
[[ $arch == arm64 || $arch == amd64 ]]
GOOS=linux GOARCH=$arch CGO_ENABLED=0 go -C services/control test -c -o "$run/rollback.test" "$(dirname "$run")/rollback_probe_test.go" > "$run/compile.log" 2>&1
od -An -N32 -tx1 /dev/urandom | tr -d ' \n' > "$run/pg-password"
printf 'postgres://postgres:%s@127.0.0.1:5432/draft_rollback_test?sslmode=disable\n' "$(cat "$run/pg-password")" > "$run/test-dsn"
chmod 600 "$run/pg-password" "$run/test-dsn"
docker volume create "$name" >/dev/null
docker run -d --name "$name" --pull never --network none --platform "linux/$arch" --cpus 2 --memory 1g --shm-size 128m \
 --label ownsolid.test=arc-datproto-source --mount "type=volume,src=$name,dst=/var/lib/postgresql" \
 --mount "type=bind,src=$run,dst=/fixture" --mount "type=bind,src=$repo,dst=/repo,readonly" \
 -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD_FILE=/run/fixture-password \
 --entrypoint bash "$image" -ec 'install -o postgres -m 0400 /fixture/pg-password /run/fixture-password; exec docker-entrypoint.sh postgres -c "listen_addresses=127.0.0.1"' >/dev/null
for ((attempt=0;attempt<60;attempt++));do
 if [[ $(docker inspect "$name" --format '{{.State.Running}}') != true ]];then docker logs --tail 60 "$name";exit 1;fi
 if docker exec "$name" pg_isready -q -h 127.0.0.1 -U postgres -d postgres;then break;fi
 sleep .5
done
docker exec "$name" createdb -U postgres draft_rollback_test
docker exec "$name" psql -Xq -U postgres -d draft_rollback_test -v ON_ERROR_STOP=1 -c "CREATE EXTENSION vector VERSION '0.8.7'; REVOKE CREATE ON SCHEMA public FROM PUBLIC"
docker inspect "$name" --format 'network={{.HostConfig.NetworkMode}} image={{.Image}}' > "$run/postgres.log"
docker exec "$name" uname -sm >> "$run/postgres.log"
run_probe() {
 docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn -e ARC_DRAFT_UP_FILE=/fixture/up.sql -e ARC_DRAFT_ORIGINAL_DOWN_FILE=/fixture/original.down.sql -e ARC_DRAFT_SAFE_DOWN_FILE=/fixture/proposed.down.sql "$name" /fixture/rollback.test -test.v -test.timeout=90s -test.run "$1"
}
red=0
run_probe '^TestOriginalDownRetainsReceipt$' > "$run/red.log" 2>&1 || red=$?
[[ $red == 1 ]]
grep -F 'original down removed a retained import receipt; outer rollback preserved the reproduction data' "$run/red.log" >/dev/null
run_probe '^TestGuarded' > "$run/guarded.log" 2>&1
! grep -E -- '--- (SKIP|FAIL)' "$run/guarded.log"
for test in TestGuardedPristineDown TestGuardedRetainedReceipts TestGuardedPriorSnapshot TestGuardedFreshIsolation TestGuardedWaitsForCommittedWriter;do
 grep -F -- "--- PASS: $test" "$run/guarded.log" >/dev/null
done
printf 'Original unsafe down reproduced and rolled back; all guarded rollback cases passed\n'
