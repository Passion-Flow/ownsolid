set -euo pipefail
umask 077
run=$1
repo=$2
name=arc-net102-$(uuidgen | tr 'A-Z' 'a-z')
printf '%s\n' "$name" > "$run/container-name"
cleanup() {
 docker rm -f "$name" >/dev/null 2>&1 || true
 docker volume rm "$name" >/dev/null 2>&1 || true
 rm -f "$run/pg-password" "$run/test-dsn"
}
trap cleanup EXIT
trap 'exit 143' TERM INT
cd "$repo"
image=pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47
arch=$(docker image inspect "$image" --format '{{.Architecture}}')
[[ $arch == arm64 || $arch == amd64 ]]
GOOS=linux GOARCH=$arch CGO_ENABLED=0 go -C services/control test -c -o "$run/server.test" ./cmd/server > "$run/compile.log" 2>&1
od -An -N32 -tx1 /dev/urandom | tr -d ' \n' > "$run/pg-password"
printf 'postgres://postgres:%s@127.0.0.1:5432/registry_source_test?sslmode=disable\n' "$(cat "$run/pg-password")" > "$run/test-dsn"
chmod 600 "$run/pg-password" "$run/test-dsn"
docker volume create "$name" >/dev/null
docker run -d --name "$name" --pull never --network none --platform "linux/$arch" --cpus 2 --memory 1g --shm-size 128m \
 --label ownsolid.test=arc-net102-source --mount "type=volume,src=$name,dst=/var/lib/postgresql" \
 --mount "type=bind,src=$run,dst=/fixture" --mount "type=bind,src=$repo,dst=/repo,readonly" \
 -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD_FILE=/run/fixture-password \
 --entrypoint bash "$image" -ec 'install -o postgres -m 0400 /fixture/pg-password /run/fixture-password; exec docker-entrypoint.sh postgres -c "listen_addresses=127.0.0.1"' >/dev/null
for ((attempt=0;attempt<60;attempt++));do
 if [[ $(docker inspect "$name" --format '{{.State.Running}}') != true ]];then docker logs --tail 60 "$name";exit 1;fi
 if docker exec "$name" pg_isready -q -h 127.0.0.1 -U postgres -d postgres;then break;fi
 sleep .5
done
docker exec "$name" createdb -U postgres registry_source_test
docker exec "$name" psql -Xq -U postgres -d registry_source_test -v ON_ERROR_STOP=1 -c "CREATE EXTENSION vector VERSION '0.8.7'; REVOKE CREATE ON SCHEMA public FROM PUBLIC"
docker inspect "$name" --format 'network={{.HostConfig.NetworkMode}} image={{.Image}}' > "$run/postgres.log"
docker exec "$name" uname -sm >> "$run/postgres.log"
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn -w /repo/services/control/cmd/server "$name" /fixture/server.test -test.v -test.timeout 3m -test.run '^TestRegistrySiteSourceStartupAdmission$' >> "$run/postgres.log" 2>&1
if grep -F -- '--- SKIP' "$run/postgres.log";then exit 1;fi
grep -F -- '--- PASS: TestRegistrySiteSourceStartupAdmission' "$run/postgres.log" >/dev/null
test -z "$(git status --porcelain)"
