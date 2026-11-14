#!/usr/bin/env bash
set -euo pipefail
umask 077
repo=$(pwd -P)
root=$(cd "$(dirname "$0")" && pwd -P)
image=pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47
if ps -axo command= | grep -Eq '^([^ ]*/)?make (check|build|test)( |$)'; then
  printf 'Another Mac repository gate is active.\n'
  exit 75
fi
docker image inspect "$image" --format '{{.Os}}/{{.Architecture}}' | grep -qx linux/arm64
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOMAXPROCS=2 \
  go -C "$repo/services/control" test -p 1 -c -o "$root/nodes.test" ./internal/modules/nodes
name="arc-component-result-$(basename "$root" | cut -d . -f 2 | tr '[:upper:]' '[:lower:]')"
cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm "$name" >/dev/null 2>&1 || true
  rm -f "$root/pg-password" "$root/test-dsn"
}
trap cleanup EXIT
od -An -N32 -tx1 /dev/urandom | tr -d ' \n' > "$root/pg-password"
printf 'postgres://postgres:%s@127.0.0.1:5432/component_result_test?sslmode=disable\n' "$(cat "$root/pg-password")" > "$root/test-dsn"
docker volume create "$name" >/dev/null
docker run -d --name "$name" --pull never --network none --cpus 1 --memory 768m --shm-size 128m \
  --label ownsolid.test=arc-component-result \
  --mount "type=volume,src=$name,dst=/var/lib/postgresql" \
  --mount "type=bind,source=$root,target=/fixture,readonly" \
  --mount "type=bind,source=$repo,target=/repo,readonly" \
  -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD_FILE=/run/fixture-password \
  --entrypoint bash "$image" -ec '
    install -o postgres -m 0400 /fixture/pg-password /run/fixture-password
    exec docker-entrypoint.sh postgres -c "listen_addresses=127.0.0.1"
  ' >/dev/null
for _ in {1..60}; do
  if [[ $(docker exec "$name" cat /proc/1/comm) == postgres ]] &&
    docker exec "$name" pg_isready -q -h 127.0.0.1 -U postgres -d postgres; then break; fi
  sleep 0.5
done
docker inspect "$name" --format 'network={{.HostConfig.NetworkMode}}'
docker exec "$name" createdb -U postgres component_result_test
docker exec "$name" psql -Xq -U postgres -d component_result_test -v ON_ERROR_STOP=1 \
  -c "CREATE EXTENSION vector VERSION '0.8.7'; REVOKE CREATE ON SCHEMA public FROM PUBLIC"
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn -w /repo/services/control/internal/modules/nodes "$name" \
  /fixture/nodes.test -test.v -test.timeout 2m -test.count 1 \
  -test.run '^(TestComponentResultFencesOwnerWritesAtSettlement|TestComponentHeartbeatCostAtFleetSizes|TestM03EnrollmentReplayAndCredentialBoundaries)$'
