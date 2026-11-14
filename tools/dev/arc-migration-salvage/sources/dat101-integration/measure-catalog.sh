#!/usr/bin/env bash
set -euo pipefail
umask 077
work=$(cd "$(dirname "$0")" && pwd -P)
repo=${1:?exact formal source checkout}
image=pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47
id=$(uuidgen | tr 'A-Z' 'a-z')
name=arc-dat101-catalog-$id
cleanup() {
 docker rm -f "$name" >/dev/null 2>&1 || true
 docker volume rm "$name" >/dev/null 2>&1 || true
 python3 - "$work" <<'PY'
from pathlib import Path
import sys
p=Path(sys.argv[1])
for name in ('pg-password','test-dsn'): (p/name).unlink(missing_ok=True)
PY
}
trap cleanup EXIT
od -An -N32 -tx1 /dev/urandom | tr -d ' \n' > "$work/pg-password"
printf 'postgres://postgres:%s@127.0.0.1:5432/gateway_description_test?sslmode=disable\n' "$(cat "$work/pg-password")" > "$work/test-dsn"
docker volume create "$name" >/dev/null
docker run -d --name "$name" --pull never --network none --cpus 2 --memory 1g --shm-size 128m \
 --label ownsolid.test=arc-dat101-catalog \
 --mount "type=volume,src=$name,dst=/var/lib/postgresql" \
 --mount "type=bind,src=$work,dst=/fixture" \
 --mount "type=bind,src=$repo,dst=/repo,readonly" \
 --mount "type=bind,src=$work/predecessor.test,dst=/predecessor.test,readonly" \
 -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD_FILE=/run/fixture-password \
 --entrypoint bash "$image" -ec 'install -o postgres -m 0400 /fixture/pg-password /run/fixture-password; exec docker-entrypoint.sh postgres -c "listen_addresses=127.0.0.1"' >/dev/null
for _ in {1..60}; do
 if [[ $(docker inspect "$name" --format '{{.State.Running}}') != true ]]; then
  docker logs --tail 60 "$name"; exit 1
 fi
 if [[ $(docker exec "$name" cat /proc/1/comm) == postgres ]] && docker exec "$name" pg_isready -q -h 127.0.0.1 -U postgres -d postgres; then break; fi
 sleep 0.5
done
docker inspect "$name" --format 'network={{.HostConfig.NetworkMode}} architecture-image={{.Image}}'
docker exec "$name" uname -sm
db() { docker exec "$name" createdb -U postgres "$1"; }
psql() { docker exec "$name" psql -XqAt -U postgres -d "$1" -v ON_ERROR_STOP=1 "${@:2}"; }
for database in gateway_description_test integrated_fresh_test; do
 db "$database"
 psql "$database" -c "CREATE EXTENSION vector VERSION '0.8.7'; REVOKE CREATE ON SCHEMA public FROM PUBLIC"
done
# Run the accepted schema-58 production migrator before the native upgrade.
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn -e AUDIT_FIXTURE_PREFIX=integrated "$name" /predecessor.test \
 -test.v -test.timeout 4m -test.run '^TestAcceptedSchema58ProductionMigrator$/^integrated_fresh_test$'
psql postgres -c 'ALTER DATABASE integrated_fresh_test RENAME TO integrated_native58_test'
db integrated_fresh_test
psql integrated_fresh_test -c "CREATE EXTENSION vector VERSION '0.8.7'"
for version in 55 56 57 58; do
 db "integrated_${version}_test"
 docker exec "$name" pg_restore -U postgres --exit-on-error --dbname "integrated_${version}_test" "/fixture/schema${version}.dump"
done
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn "$name" /fixture/database.test -test.v -test.timeout 5m \
 -test.run '^TestFormalSchema59ProductionMigratorAfterRestore$/^(fresh|native58|55|56|57|58)$'
db integrated_59_test
docker exec "$name" pg_restore -U postgres --exit-on-error --dbname integrated_59_test /fixture/schema59.dump
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn "$name" /fixture/database.test -test.v -test.timeout 3m \
 -test.run '^TestFormalSchema59ProductionMigratorAfterRestore$/^59$'
for suffix in fresh native58 55 56 57 58 59; do
 expected=18bc8279d46bf95454b7fd0236b9ab6ee54b073eb12551a87968d2a545db59dc
 if [[ $suffix == fresh || $suffix == native58 ]]; then expected=72aa05f9653316c589fd79c12ba43580b589f019a35b09b2569754b91d1ee782; fi
 if [[ $suffix == 55 ]]; then expected=8c2f55bab0055c374d9083cfa993fc9986cf9b3ebb7e8fd6b06c0adfdd415069; fi
 actual=$(psql "integrated_${suffix}_test" -f /fixture/catalog.sql)
 test "$actual" = "$expected"
 printf 'catalog %s %s, 119 tables, 59 entries\n' "$suffix" "$actual"
done
for suffix in fresh native58 55 56 57 58 59; do
 before=$(psql "integrated_${suffix}_test" -f /fixture/catalog.sql)
 psql "integrated_${suffix}_test" --single-transaction -f /fixture/receipt.up.sql
 actual=$(psql "integrated_${suffix}_test" -f /fixture/catalog.sql)
 counts=$(psql "integrated_${suffix}_test" -c "SELECT (SELECT count(*) FROM pg_class WHERE relnamespace=current_schema()::regnamespace AND relkind IN ('r','p') AND NOT relispartition)||'/'||(SELECT count(*) FROM schema_migrations)")
 test "$counts" = 120/59
 printf 'prospective_catalog %s %s tables/accepted-ledger=%s\n' "$suffix" "$actual" "$counts"
 psql "integrated_${suffix}_test" --single-transaction -f /fixture/receipt.down.sql
 test "$(psql "integrated_${suffix}_test" -f /fixture/catalog.sql)" = "$before"
 psql "integrated_${suffix}_test" --single-transaction -f /fixture/receipt.up.sql
 test "$(psql "integrated_${suffix}_test" -f /fixture/catalog.sql)" = "$actual"
done
docker exec "$name" pg_dump -U postgres -d integrated_fresh_test -Fc --no-owner --no-privileges > "$work/prospective-receipt.dump"
db prospective_receipt_restore_test
docker exec "$name" pg_restore -U postgres --exit-on-error --dbname prospective_receipt_restore_test /fixture/prospective-receipt.dump
actual=$(psql prospective_receipt_restore_test -f /fixture/catalog.sql)
counts=$(psql prospective_receipt_restore_test -c "SELECT (SELECT count(*) FROM pg_class WHERE relnamespace=current_schema()::regnamespace AND relkind IN ('r','p') AND NOT relispartition)||'/'||(SELECT count(*) FROM schema_migrations)")
test "$counts" = 120/59
printf 'prospective_catalog restored-receipt %s tables/accepted-ledger=%s\n' "$actual" "$counts"
checkpoint=$(python3 - "$work/prefix.jsonl" <<'PY'
import json,pathlib,sys
lines=pathlib.Path(sys.argv[1]).read_text().splitlines();assert len(lines)==2
print(json.loads(lines[-1])['hash'])
PY
)
for version in 55 56 57 58; do
 psql "integrated_${version}_test" -f /repo/tools/audit-verify/export.sql > "$work/restored${version}.jsonl"
 cmp "$work/prefix.jsonl" "$work/restored${version}.jsonl"
 docker exec "$name" /fixture/audit-verify --file "/fixture/restored${version}.jsonl" --expected-last-hash "$checkpoint"
 printf 'retained schema-%s audit checkpoint accepted\n' "$version"
done
