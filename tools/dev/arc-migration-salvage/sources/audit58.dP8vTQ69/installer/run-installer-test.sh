#!/usr/bin/env bash
set -euo pipefail
umask 077
scratch=$(cd "$(dirname "$0")" && pwd -P)
image=pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47
id=$(uuidgen | tr A-Z a-z)
container="arc-restore-${id}"
volume="arc-restore-${id}"
work=$(mktemp -d "$scratch/installer.XXXXXXXX")
mkdir "$work/config"
cleanup() {
    rm -f "$work/postgres-password"
    docker rm -f "$container" >/dev/null 2>&1 || true
    docker volume rm "$volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT
od -An -N32 -tx1 /dev/urandom | tr -d ' \n' > "$work/postgres-password"
docker volume create "$volume" >/dev/null
docker run -d --name "$container" --pull never --network none \
    --cpus 2 --memory 768m --pids-limit 128 --label ownsolid.test=restored-schema-upgrade \
    --mount "type=volume,src=$volume,dst=/var/lib/postgresql" \
    --mount "type=bind,source=$scratch/inputs,target=/inputs,readonly" \
    --mount "type=bind,source=$work/config,target=/fixture,readonly" \
    --mount "type=bind,source=$work/postgres-password,target=/run/bootstrap-password,readonly" \
    -e POSTGRES_DB=ownsolid -e POSTGRES_USER=ownsolid_migrator -e POSTGRES_PASSWORD_FILE=/run/fixture-password \
    --entrypoint bash "$image" -ec '
        install -o postgres -m 0400 /run/bootstrap-password /run/fixture-password
        exec docker-entrypoint.sh postgres -c "listen_addresses="
    ' >/dev/null
for _ in {1..60}; do
    if [[ $(docker exec "$container" cat /proc/1/comm) == postgres ]] &&
       docker exec "$container" pg_isready -q -U ownsolid_migrator -d ownsolid; then break; fi
    sleep 0.5
done
docker exec "$container" psql -XAt -v ON_ERROR_STOP=1 -U ownsolid_migrator -d ownsolid -c 'SELECT version()'
export TMPDIR="$(cd "${TMPDIR:-/tmp}" && pwd -P)/"
export GOFLAGS=-p=2 GOPROXY=off GOSUMDB=off
export OWNSOLID_SCHEMA_FIXTURE_CONTAINER="$container" OWNSOLID_SCHEMA_FIXTURE_DIR="$work/config"
export OWNSOLID_SCHEMA_FIXTURE_SQL56="$scratch/inputs/000056.up.sql" OWNSOLID_SCHEMA_FIXTURE_EXPECT="${1:?expected outcome}"
go -C deploy/offline test ./internal/install -run '^TestRestoredSchemaUpgrade$' -count=1 -v -timeout 6m
