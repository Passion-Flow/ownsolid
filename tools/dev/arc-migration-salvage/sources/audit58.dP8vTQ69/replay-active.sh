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
docker exec "$name" sh /fixture/verify-active.sh | tee "$scratch/active-matrix.log"
diff -u <(grep '^catalog ' "$scratch/post-arc-restore.log") <(grep '^catalog ' "$scratch/active-matrix.log")
for db in fresh58_test native_test restored55_test restored56_test restored57_test restored58_test; do
  count=$(docker exec "$name" psql -XAt -U postgres -d "$db" -v ON_ERROR_STOP=1 -c \
    "SELECT count(*) FROM pg_class WHERE relnamespace=current_schema()::regnamespace AND relkind IN ('r','p') AND NOT relispartition")
  test "$count" = 118
  printf '%s: %s tables\n' "$db" "$count"
done
docker exec "$name" createdb -U postgres integrated_fresh_test
docker exec "$name" psql -Xq -U postgres -d integrated_fresh_test -v ON_ERROR_STOP=1 -c "CREATE EXTENSION vector VERSION '0.8.7'"
for version in 55 56 57 58; do
  db="integrated_${version}_test"
  docker exec "$name" createdb -U postgres "$db"
  docker exec "$name" pg_restore -U postgres --exit-on-error --dbname "$db" "/fixture/schema${version}.dump"
done
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn -e AUDIT_FIXTURE_PREFIX=integrated \
  "$name" /fixture/database.test -test.v -test.timeout 4m \
  -test.run '^(TestAuditCandidateProductionMigratorAfterRestore|TestEveryCellOwnedTableCarriesScope|TestNewRowsTakeTheLocalCell|TestCheckRefusesAnotherCellIdentity)$'
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn -e OWNSOLID_TEST_AUDIT_VERIFY=/fixture/audit-verify \
  -w /repo/services/control/internal/modules/audit "$name" /fixture/audit.test -test.v -test.timeout 4m
