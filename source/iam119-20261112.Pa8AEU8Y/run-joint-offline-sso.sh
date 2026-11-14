#!/bin/sh
set -eu
root=$1
scratch=$2
selector=$3
repetitions=$4
cd "$root"
instance="ownsolid-iam119-$$-$(basename "$scratch" | tr '[:upper:]' '[:lower:]' | tr -cd 'a-z0-9-' | tail -c 32)"
created=false
cleanup() { if [ "$created" = true ]; then docker rm -f "$instance" >/dev/null 2>&1 || true; fi; }
trap cleanup EXIT
trap 'exit 130' INT TERM
docker run --pull never --detach --rm --network none --name "$instance" -e POSTGRES_HOST_AUTH_METHOD=trust -e POSTGRES_DB=iam119_recovery_test --tmpfs /var/lib/postgresql:rw,size=1g pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47 >/dev/null
created=true
for n in $(seq 1 60); do
  if docker exec -u postgres "$instance" pg_isready -h 127.0.0.1 -d iam119_recovery_test >/dev/null; then break; fi
  sleep 1
done
docker inspect --format='network={{.HostConfig.NetworkMode}} image={{.Image}}' "$instance"
docker exec -u postgres "$instance" psql -v ON_ERROR_STOP=1 -d iam119_recovery_test -c 'CREATE EXTENSION vector VERSION "0.8.7"' -c 'SHOW server_version'
docker cp "$scratch/sso.test" "$instance:/tmp/sso.test"
docker exec "$instance" mkdir -p /tmp/tree/api/openapi/v1/identity /tmp/tree/api/openapi/v1/sso /tmp/tree/services/control/internal/modules/sso
docker cp api/openapi/v1/identity/openapi.json "$instance:/tmp/tree/api/openapi/v1/identity/openapi.json"
docker cp api/openapi/v1/sso/openapi.json "$instance:/tmp/tree/api/openapi/v1/sso/openapi.json"
docker exec -u postgres -e 'PROJECT_ONE_TEST_DSN=postgresql://postgres@127.0.0.1:5432/iam119_recovery_test?sslmode=disable' -w /tmp/tree/services/control/internal/modules/sso "$instance" sh -c 'umask 077; exec /tmp/sso.test -test.v -test.run "$1" -test.count "$2"' probe "$selector" "$repetitions"
