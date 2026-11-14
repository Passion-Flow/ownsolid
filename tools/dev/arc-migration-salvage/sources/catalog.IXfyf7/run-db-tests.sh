#!/usr/bin/env bash
set -euo pipefail
umask 077
scratch=$(cd "$(dirname "$0")" && pwd -P)
docker_context=$(docker context show)
image=pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47
id=$(uuidgen | tr A-Z a-z)
container="arc-pg-${id}"
volume="arc-pg-${id}"
host_port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')
fixture_dir=$(mktemp -d "$scratch/db-tests.XXXXXXXX")
cleanup() {
    rm -f "$fixture_dir/password" "$fixture_dir/dsn"
    docker --context "$docker_context" rm -f "$container" >/dev/null 2>&1 || true
    docker --context "$docker_context" volume rm "$volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT
od -An -N32 -tx1 /dev/urandom | tr -d ' \n' > "$fixture_dir/password"
docker --context "$docker_context" volume create "$volume" >/dev/null
docker --context "$docker_context" run -d --name "$container" --pull never \
    --cpus "${ARC_FIXTURE_CPUS:-1}" --memory "${ARC_FIXTURE_MEMORY:-512m}" --pids-limit 128 \
    --label projectone.owner=mvp-development --label projectone.milestone=M03 --label "projectone.fixture_id=$id" \
    -p "127.0.0.1:$host_port:5432" --mount "type=volume,src=$volume,dst=/var/lib/postgresql" \
    --mount "type=bind,source=$fixture_dir/password,target=/run/bootstrap-password,readonly" \
    -e POSTGRES_DB=ownsolid_test -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD_FILE=/run/fixture-password \
    --entrypoint bash "$image" -ec '
        install -o postgres -m 0400 /run/bootstrap-password /run/fixture-password
        exec docker-entrypoint.sh postgres -c "cluster_name=nodes-fixture-$1"
    ' bash "$id" >/dev/null
for _ in {1..60}; do
    if [[ $(docker --context "$docker_context" exec "$container" cat /proc/1/comm) == postgres ]] &&
       docker --context "$docker_context" exec "$container" pg_isready -q -U postgres -d ownsolid_test; then break; fi
    sleep 0.5
done
docker --context "$docker_context" exec "$container" psql -XAt -v ON_ERROR_STOP=1 -U postgres -d ownsolid_test \
    -c "CREATE EXTENSION vector VERSION '0.8.7'; REVOKE CREATE ON SCHEMA public FROM PUBLIC" >/dev/null
port=$(docker --context "$docker_context" port "$container" 5432/tcp)
printf 'postgres://postgres:%s@%s/ownsolid_test?sslmode=disable\n' "$(cat "$fixture_dir/password")" "$port" > "$fixture_dir/dsn"
export PROJECT_ONE_TEST_DSN_FILE="$fixture_dir/dsn"
export PROJECT_ONE_FIXTURE_CONTAINER="$container" PROJECT_ONE_FIXTURE_DOCKER_CONTEXT="$docker_context"
export TMPDIR="$(cd "${TMPDIR:-/tmp}" && pwd -P)/"
export GOFLAGS=-p=2 GOPROXY=off GOSUMDB=off
"$@"
