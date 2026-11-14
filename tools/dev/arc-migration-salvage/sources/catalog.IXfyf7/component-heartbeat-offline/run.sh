#!/usr/bin/env bash
set -euo pipefail
umask 077
probe=$(cd "$(dirname "$0")" && pwd -P)
image=pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47
id=$(uuidgen | tr A-Z a-z)
container="arc-offline-${id}"
volume="$container"
cleanup() {
    docker rm -f "$container" >/dev/null 2>&1 || true
    docker volume rm "$volume" >/dev/null 2>&1 || true
    python3 - "$probe" <<'PY'
from pathlib import Path
import sys
p = Path(sys.argv[1])
for name in ('pg-password', 'pg-dsn'):
    (p / name).unlink(missing_ok=True)
PY
}
trap cleanup EXIT
od -An -N32 -tx1 /dev/urandom | tr -d ' \n' > "$probe/pg-password"
printf 'postgres://postgres:%s@127.0.0.1:5432/arc_component_test?sslmode=disable\n' "$(cat "$probe/pg-password")" > "$probe/pg-dsn"
docker volume create "$volume" >/dev/null
docker run -d --name "$container" --pull never --network none --cpus 2 --memory 1g --pids-limit 128 \
    --mount "type=bind,source=$probe,target=/probe,readonly" \
    --mount "type=volume,src=$volume,dst=/var/lib/postgresql" \
    -e POSTGRES_DB=arc_component_test -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD_FILE=/run/fixture-password \
    --entrypoint bash "$image" -ec '
        install -o postgres -m 0400 /probe/pg-password /run/fixture-password
        exec docker-entrypoint.sh postgres -c "listen_addresses=127.0.0.1"
    ' >/dev/null
for _ in {1..60}; do
    if [[ $(docker exec "$container" cat /proc/1/comm) == postgres ]] &&
       docker exec "$container" pg_isready -q -h 127.0.0.1 -U postgres -d arc_component_test; then break; fi
    sleep 0.5
done
docker inspect "$container" --format 'network={{.HostConfig.NetworkMode}}'
docker exec "$container" psql -XAt -v ON_ERROR_STOP=1 -U postgres -d arc_component_test \
    -c "CREATE EXTENSION vector VERSION '0.8.7'; REVOKE CREATE ON SCHEMA public FROM PUBLIC"
docker exec --workdir /probe/services/control/internal/modules/nodes \
    -e PROJECT_ONE_TEST_DSN_FILE=/probe/pg-dsn \
    -e PROJECT_ONE_REPOSITORY_ENGINE=postgresql \
    -e PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE=/probe/pg-dsn \
    -e "PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA=m20_repository_$(openssl rand -hex 16)" \
    "$container" /probe/nodes.test -test.run '^(TestComponentHeartbeatCostAtFleetSizes|TestReadOnlyClaimChecksAuthorityOnce|TestInferenceHeartbeatStatementCost|TestActualBorrowedPreparationNodeSource)$' -test.v -test.timeout 5m
