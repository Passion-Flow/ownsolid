#!/usr/bin/env bash
set -euo pipefail
umask 077
scratch=$(cd "$(dirname "$0")" && pwd -P)
repo=$(cd "${1:?exact production source checkout}" && pwd -P)
image=pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47
id=$(cat /proc/sys/kernel/random/uuid 2>/dev/null || uuidgen | tr A-Z a-z)
name="arc-audit58-$id"
cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm "$name" >/dev/null 2>&1 || true
  python3 - "$scratch" <<'PY'
from pathlib import Path
import sys
p=Path(sys.argv[1])
for f in ('pg-password','test-dsn'):
    (p/f).unlink(missing_ok=True)
PY
}
trap cleanup EXIT
od -An -N32 -tx1 /dev/urandom | tr -d ' \n' > "$scratch/pg-password"
printf 'postgres://postgres:%s@127.0.0.1:5432/audit_scope_test?sslmode=disable\n' "$(cat "$scratch/pg-password")" > "$scratch/test-dsn"
docker volume create "$name" >/dev/null
docker run -d --name "$name" --pull never --network none --cpus 2 --memory 1g --shm-size 128m \
  --label ownsolid.test=arc-audit58 \
  --mount "type=volume,src=$name,dst=/var/lib/postgresql" \
  --mount "type=bind,source=$scratch,target=/fixture" \
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
docker exec "$name" uname -sm
docker exec "$name" createdb -U postgres audit_scope_test
docker exec "$name" psql -Xq -U postgres -d audit_scope_test -v ON_ERROR_STOP=1 \
  -c "CREATE EXTENSION vector VERSION '0.8.7'; REVOKE CREATE ON SCHEMA public FROM PUBLIC"

docker exec "$name" createdb -U postgres integrated_fresh_test
docker exec "$name" psql -Xq -U postgres -d integrated_fresh_test -v ON_ERROR_STOP=1 -c "CREATE EXTENSION vector VERSION '0.8.7'"
for version in 55 58; do
  docker exec "$name" createdb -U postgres "integrated_${version}_test"
  docker exec "$name" pg_restore -U postgres --exit-on-error --dbname "integrated_${version}_test" "/fixture/schema${version}.dump"
done
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn -e AUDIT_FIXTURE_PREFIX=integrated \
  "$name" /fixture/database.test -test.v -test.timeout 4m \
  -test.run '^TestAuditCandidateProductionMigratorAfterRestore$/^integrated_(fresh|55|58)_test$'
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn \
  "$name" /fixture/database.test -test.v -test.timeout 4m \
  -test.run '^(TestEveryCellOwnedTableCarriesScope|TestNewRowsTakeTheLocalCell|TestCheckRefusesAnotherCellIdentity)$'
for pair in \
  integrated_fresh_test:56795c36a7a0115478e17ee9af9ea98f27c1922c201cfec0b874d8d72b713d1d \
  integrated_55_test:9e464ca9f6708f89731980adc8def9096e05b7f11f842ee09faa7836a12d0e42 \
  integrated_58_test:ded94bd6eeb9c84949fd7647bd1b200e612b03fd4aedf8462280f5dacd87ce5d; do
  db=${pair%%:*}
  expected=${pair#*:}
  actual=$(docker exec "$name" psql -XqAt -U postgres -d "$db" -v ON_ERROR_STOP=1 -f /fixture/catalog.sql)
  test "$actual" = "$expected"
  count=$(docker exec "$name" psql -XqAt -U postgres -d "$db" -v ON_ERROR_STOP=1 -c \
    "SELECT count(*) FROM pg_class WHERE relnamespace=current_schema()::regnamespace AND relkind IN ('r','p') AND NOT relispartition")
  test "$count" = 118
  printf 'catalog %s %s, %s tables\n' "$db" "$actual" "$count"
done
checkpoint=$(python3 - "$scratch/prefix.jsonl" <<'CHECKPOINT'
import json, pathlib, sys
lines=pathlib.Path(sys.argv[1]).read_text().splitlines()
assert len(lines)==2
print(json.loads(lines[-1])['hash'])
CHECKPOINT
)
for version in 55 58; do
  docker exec "$name" psql -XqAt -U postgres -d "integrated_${version}_test" -v ON_ERROR_STOP=1 \
    -f /repo/tools/audit-verify/export.sql > "$scratch/restored${version}.jsonl"
  cmp "$scratch/prefix.jsonl" "$scratch/restored${version}.jsonl"
  docker exec "$name" /fixture/audit-verify --file "/fixture/restored${version}.jsonl" --expected-last-hash "$checkpoint"
  printf 'retained restored%s checkpoint accepted\n' "$version"
done
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn -e OWNSOLID_TEST_AUDIT_VERIFY=/fixture/audit-verify \
  -w /repo/services/control/internal/modules/audit "$name" /fixture/audit.test -test.v -test.timeout 4m
