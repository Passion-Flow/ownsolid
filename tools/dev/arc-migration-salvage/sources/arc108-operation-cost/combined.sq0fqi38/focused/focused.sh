set -euo pipefail
umask 077
arc_run=$1
arc_repo=$2
arc_name=arc108-cost-$(uuidgen | tr 'A-Z' 'a-z')
arc_mount=$(mktemp -d /private/tmp/osarc108.XXXXXXXX)
printf '%s\n' "$arc_name" > "$arc_run/container-name"
cleanup() {
 docker rm -f "$arc_name" >/dev/null 2>&1 || true
 docker volume rm "$arc_name" >/dev/null 2>&1 || true
 rm -f "$arc_run/pg-password" "$arc_run/test-dsn"
 if mount | grep -F "on $arc_mount (" >/dev/null;then hdiutil detach "$arc_mount" >> "$arc_run/cleanup.log" 2>&1;fi
 rmdir "$arc_mount"
}
trap cleanup EXIT
trap 'exit 143' TERM INT
hdiutil create -size 8g -fs APFS -type SPARSE -volname ownsolid-arc108 -o "$arc_run/scratch" > "$arc_run/create.log" 2>&1
hdiutil attach -nobrowse -mountpoint "$arc_mount" "$arc_run/scratch.sparseimage" > "$arc_run/attach.log" 2>&1
mkdir "$arc_mount/t";chgrp "$(id -g)" "$arc_mount/t";chmod 700 "$arc_mount/t"
export TMPDIR="$arc_mount/t/" GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOMAXPROCS=2 GOFLAGS=-p=1
cd "$arc_repo"
test -z "$(git status --porcelain)"
printf 'Reusing source-verified a91a817b race, vet and Linux binary; replaying corrected private fixture\n'
arc_image=pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47
test "$(docker image inspect "$arc_image" --format '{{.Architecture}}')" = arm64
od -An -N32 -tx1 /dev/urandom | tr -d ' \n' > "$arc_run/pg-password"
printf 'postgres://postgres:%s@127.0.0.1:5432/agent_observation_cost_test?sslmode=disable\n' "$(cat "$arc_run/pg-password")" > "$arc_run/test-dsn"
docker volume create "$arc_name" >/dev/null
docker run -d --name "$arc_name" --pull never --network none --platform linux/arm64 --cpus 2 --memory 1g --shm-size 128m \
 --label ownsolid.test=arc108-observation-cost --mount "type=volume,src=$arc_name,dst=/var/lib/postgresql" \
 --mount "type=bind,src=$arc_run,dst=/fixture" --mount "type=bind,src=$arc_repo,dst=/repo,readonly" \
 -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD_FILE=/run/fixture-password \
 --entrypoint bash "$arc_image" -ec 'install -o postgres -m 0400 /fixture/pg-password /run/fixture-password; exec docker-entrypoint.sh postgres -c "listen_addresses=127.0.0.1"' >/dev/null
for ((attempt=0;attempt<60;attempt++));do
 if [[ $(docker inspect "$arc_name" --format '{{.State.Running}}') != true ]];then docker logs --tail 50 "$arc_name";exit 1;fi
 if docker exec "$arc_name" pg_isready -q -h 127.0.0.1 -U postgres -d postgres;then break;fi
 sleep .5
done
docker exec "$arc_name" createdb -U postgres agent_observation_cost_test
docker exec "$arc_name" psql -XqAt -U postgres -d agent_observation_cost_test -v ON_ERROR_STOP=1 -c "CREATE EXTENSION vector VERSION '0.8.7'; REVOKE CREATE ON SCHEMA public FROM PUBLIC"
printf 'postgres-cost\n' > "$arc_run/stage"
docker inspect "$arc_name" --format 'network={{.HostConfig.NetworkMode}} image={{.Image}}' > "$arc_run/cost.log"
docker exec "$arc_name" uname -sm >> "$arc_run/cost.log"
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn -w /repo/services/control/internal/modules/nodes "$arc_name" /fixture/nodes.test -test.v -test.timeout=5m -test.run '^(TestComponentHeartbeatCostAtFleetSizes|TestInferenceHeartbeatStatementCost)$' >> "$arc_run/cost.log" 2>&1
! grep -E -- '--- (SKIP|FAIL)' "$arc_run/cost.log"
test "$(grep -c '^--- PASS: Test' "$arc_run/cost.log")" = 2
test -z "$(git status --porcelain)"
printf 'ARC108 actual observation costs and affected race/vet passed\n'
