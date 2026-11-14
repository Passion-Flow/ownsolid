#!/usr/bin/env bash
set -euo pipefail
umask 077
work=$1
repo=$2
mkdir -m 700 "$work"
name=arc-gw120-formal-$(uuidgen | tr 'A-Z' 'a-z')
cleanup() {
 docker rm -f "$name" >/dev/null 2>&1 || true
 docker volume rm "$name" >/dev/null 2>&1 || true
 rm -f "$work/pg-password" "$work/test-dsn"
}
trap cleanup EXIT
cd "$repo"
image=pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47
arch=$(docker image inspect "$image" --format '{{.Architecture}}')
[[ $arch == arm64 || $arch == amd64 ]]
printf 'Source %s\nMac host: %s\nPostgreSQL image architecture: %s\n' "$(git rev-parse HEAD)" "$(uname -sm)" "$arch"
export GOOS=linux GOARCH=$arch CGO_ENABLED=0 GOPROXY=off GOSUMDB=off GOMAXPROCS=2 GOFLAGS=-p=1
go -C services/control test -c -o "$work/distribution.test" ./internal/modules/distribution
go -C services/control test -c -o "$work/databaseaccess.test" ./internal/operations/databaseaccess
shasum -a 256 "$work/distribution.test" "$work/databaseaccess.test" > "$work/binary-sha256"
od -An -N32 -tx1 /dev/urandom | tr -d ' \n' > "$work/pg-password"
printf 'postgres://postgres:%s@127.0.0.1:5432/gateway_description_test?sslmode=disable\n' "$(cat "$work/pg-password")" > "$work/test-dsn"
docker volume create "$name" >/dev/null
docker run -d --name "$name" --pull never --platform "linux/$arch" --network none --cpus 2 --memory 1g --shm-size 128m \
 --label ownsolid.test=arc-gw120-formal \
 --mount "type=volume,src=$name,dst=/var/lib/postgresql" \
 --mount "type=bind,src=$work,dst=/fixture" \
 --mount "type=bind,src=$repo,dst=/repo,readonly" \
 -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD_FILE=/run/fixture-password \
 --entrypoint bash "$image" -ec 'install -o postgres -m 0400 /fixture/pg-password /run/fixture-password; exec docker-entrypoint.sh postgres -c "listen_addresses=127.0.0.1"' >/dev/null
for ((attempt=0;attempt<60;attempt++)); do
 if [[ $(docker inspect "$name" --format '{{.State.Running}}') != true ]]; then docker logs --tail 60 "$name";exit 1;fi
 if docker exec "$name" pg_isready -q -h 127.0.0.1 -U postgres -d postgres;then break;fi
 sleep 0.5
done
docker exec "$name" createdb -U postgres gateway_description_test
docker exec "$name" psql -Xq -U postgres -d gateway_description_test -v ON_ERROR_STOP=1 -c "CREATE EXTENSION vector VERSION '0.8.7'; REVOKE CREATE ON SCHEMA public FROM PUBLIC"
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn -w /repo/services/control/internal/modules/distribution "$name" /fixture/distribution.test \
 -test.v -test.timeout 5m -test.run '^TestAliasDescription' | tee "$work/distribution.log"
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn -w /repo/services/control/internal/operations/databaseaccess "$name" /fixture/databaseaccess.test \
 -test.v -test.timeout 5m -test.run '^(TestAliasDescriptionUpgradeResumesExistingRuntimePrincipal|TestUpgrade.*|TestActualRuntimeAccessPreparationAccountWorkspaceDMLAndNoDDL)$' | tee "$work/databaseaccess.log"
if grep -F -- '--- SKIP' "$work/distribution.log" "$work/databaseaccess.log";then exit 1;fi
printf 'Formal PostgreSQL and HTTPS regressions passed with no skips\n'
