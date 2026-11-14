set -euo pipefail
umask 077
run=$1
repo=$2
base=$(dirname "$run")
name=arc-dat101-$(uuidgen | tr 'A-Z' 'a-z')
printf '%s\n' "$name" > "$run/container-name"
mountpoint=$(mktemp -d /private/tmp/osdat.XXXXXXXX)
cleanup() {
 docker rm -f "$name" >/dev/null 2>&1 || true
 docker volume rm "$name" >/dev/null 2>&1 || true
 rm -f "$run/pg-password" "$run/test-dsn"
 if mount | grep -F "on $mountpoint (" >/dev/null;then hdiutil detach "$mountpoint" >> "$run/cleanup.log" 2>&1;fi
 rmdir "$mountpoint"
}
trap cleanup EXIT
trap 'exit 143' TERM INT
hdiutil create -size 8g -fs APFS -type SPARSE -volname ownsolid-dat101 -o "$run/scratch" > "$run/create.log" 2>&1
hdiutil attach -nobrowse -mountpoint "$mountpoint" "$run/scratch.sparseimage" > "$run/attach.log" 2>&1
mkdir "$mountpoint/t";chgrp "$(id -g)" "$mountpoint/t";chmod 700 "$mountpoint/t"
export TMPDIR="$mountpoint/t/" GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOMAXPROCS=2 GOFLAGS=-p=1
cd "$repo"
test -z "$(git status --porcelain)"
printf 'race\n' > "$run/stage"
go -C services/control test -race -count=1 -v ./internal/operations/databaseaccess > "$run/race.log" 2>&1
image=pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47
arch=$(docker image inspect "$image" --format '{{.Architecture}}')
[[ $arch == arm64 ]]
printf 'compile\n' > "$run/stage"
GOOS=linux GOARCH=$arch CGO_ENABLED=0 go -C services/control test -c -overlay "$base/formal-overlay.json" -o "$run/database.test" ./internal/platform/database > "$run/compile-database.log" 2>&1
GOOS=linux GOARCH=$arch CGO_ENABLED=0 go -C services/control test -c -o "$run/databaseaccess.test" ./internal/operations/databaseaccess > "$run/compile-access.log" 2>&1
GOOS=linux GOARCH=$arch CGO_ENABLED=0 go -C services/control test -c -tags dataset_processor_integration -o "$run/datasets.test" ./internal/modules/datasets > "$run/compile-datasets.log" 2>&1
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go -C services/control test -c -tags dataset_processor_integration -o "$run/datasets-linux-amd64.test" ./internal/modules/datasets > "$run/compile-datasets-amd64.log" 2>&1
od -An -N32 -tx1 /dev/urandom | tr -d ' \n' > "$run/pg-password"
printf 'postgres://postgres:%s@127.0.0.1:5432/dataset_draft_migration_test?sslmode=disable\n' "$(cat "$run/pg-password")" > "$run/test-dsn"
chmod 600 "$run/pg-password" "$run/test-dsn"
docker volume create "$name" >/dev/null
docker run -d --name "$name" --pull never --network none --platform linux/arm64 --cpus 2 --memory 1g --shm-size 128m \
 --label ownsolid.test=arc-dat101-formal --mount "type=volume,src=$name,dst=/var/lib/postgresql" \
 --mount "type=bind,src=$run,dst=/fixture" --mount "type=bind,src=$repo,dst=/repo,readonly" \
 -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD_FILE=/run/fixture-password \
 --entrypoint bash "$image" -ec 'install -o postgres -m 0400 /fixture/pg-password /run/fixture-password; exec docker-entrypoint.sh postgres -c "listen_addresses=127.0.0.1"' >/dev/null
for ((attempt=0;attempt<60;attempt++));do
 if [[ $(docker inspect "$name" --format '{{.State.Running}}') != true ]];then docker logs --tail 60 "$name";exit 1;fi
 if docker exec "$name" pg_isready -q -h 127.0.0.1 -U postgres -d postgres;then break;fi
 sleep .5
done
db() { docker exec "$name" createdb -U postgres "$1"; }
psql() { docker exec "$name" psql -XqAt -U postgres -d "$1" -v ON_ERROR_STOP=1 "${@:2}"; }
db dataset_draft_migration_test
psql dataset_draft_migration_test -c "CREATE EXTENSION vector VERSION '0.8.7'; REVOKE CREATE ON SCHEMA public FROM PUBLIC"
docker inspect "$name" --format 'network={{.HostConfig.NetworkMode}} image={{.Image}}' > "$run/database.log"
docker exec "$name" uname -sm >> "$run/database.log"
printf 'numbered-scripts\n' > "$run/stage"
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn -w /repo/services/control/internal/platform/database "$name" /fixture/database.test -test.v -test.timeout=4m -test.run '^TestDatasetDraftImport' >> "$run/database.log" 2>&1
! grep -E -- '--- (SKIP|FAIL)' "$run/database.log"
printf 'runtime-grants\n' > "$run/stage"
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn -w /repo/services/control/internal/operations/databaseaccess "$name" /fixture/databaseaccess.test -test.v -test.timeout=5m -test.run '^(TestDatasetDraftImportUpgradeResumesExistingRuntimePrincipal|TestAliasDescriptionUpgradeResumesExistingRuntimePrincipal|TestActualRuntimeAccessPreparationAccountWorkspaceDMLAndNoDDL|TestUpgradeFromDeliveredPrefixPreservesRoleAndData|TestUpgradeResumesAfterSchemaCommittedAndUnknownGrantCommit|TestUpgradeResumesGrantsAfterSchemaCommitted|TestUpgradeRejectsForeignLedgerAndMissingRole|TestUpgradeRejectsMissingRole|TestUpgradeRejectsElevatedRoleBeforeDDL)$' > "$run/access.log" 2>&1
! grep -E -- '--- (SKIP|FAIL)' "$run/access.log"
printf 'restore-matrix\n' > "$run/stage"
for database in integrated_fresh_test;do
 db "$database";psql "$database" -c "CREATE EXTENSION vector VERSION '0.8.7'; REVOKE CREATE ON SCHEMA public FROM PUBLIC"
done
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn "$name" /fixture/accepted59.test -test.v -test.timeout=3m -test.run '^TestFormalSchema59ProductionMigratorAfterRestore$/^fresh$' > "$run/predecessor.log" 2>&1
psql postgres -c 'ALTER DATABASE integrated_fresh_test RENAME TO integrated_native59_test'
db integrated_fresh_test
psql integrated_fresh_test -c "CREATE EXTENSION vector VERSION '0.8.7'"
for version in 55 56 57 58 59;do
 db "integrated_${version}_test"
 docker exec "$name" pg_restore -U postgres --exit-on-error --dbname "integrated_${version}_test" "/fixture/schema${version}.dump"
done
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn "$name" /fixture/database.test -test.v -test.timeout=4m -test.run '^TestFormalSchema60ProductionMigratorAfterRestore$/^(fresh|native59|55|56|57|58|59)$' > "$run/matrix.log" 2>&1
docker exec "$name" pg_dump -U postgres -d integrated_fresh_test -Fc --no-owner --no-privileges > "$run/schema60.dump"
db integrated_60_test
docker exec "$name" pg_restore -U postgres --exit-on-error --dbname integrated_60_test /fixture/schema60.dump
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn "$name" /fixture/database.test -test.v -test.timeout=3m -test.run '^TestFormalSchema60ProductionMigratorAfterRestore$/^60$' >> "$run/matrix.log" 2>&1
! grep -E -- '--- (SKIP|FAIL)' "$run/matrix.log"
for suffix in fresh native59 55 56 57 58 59 60;do
 expected=93b8967dbcc4b90c1692dcf67076192a8b97ead256335cf2a4fbccb6dcdf87b4
 if [[ $suffix == fresh || $suffix == native59 ]];then expected=4999ec1b3e7ccad081e2290270860c362d23d56ba088441dee7869f734f608d7;fi
 if [[ $suffix == 55 ]];then expected=f0178764888dc1f0ce36b96a98d4105af4a1b7d8c2c99fbec359f20c4ef7b813;fi
 actual=$(psql "integrated_${suffix}_test" -f /fixture/catalog.sql)
 test "$actual" = "$expected"
 printf 'catalog %s %s, 120 tables, 60 entries\n' "$suffix" "$actual" >> "$run/matrix.log"
done
checkpoint=$(python3 - "$run/prefix.jsonl" <<'PY'
import json,pathlib,sys
lines=pathlib.Path(sys.argv[1]).read_text().splitlines();assert len(lines)==2
print(json.loads(lines[-1])['hash'])
PY
)
for version in 55 56 57 58;do
 psql "integrated_${version}_test" -f /repo/tools/audit-verify/export.sql > "$run/restored${version}.jsonl"
 cmp "$run/prefix.jsonl" "$run/restored${version}.jsonl"
 docker exec "$name" /fixture/audit-verify --file "/fixture/restored${version}.jsonl" --expected-last-hash "$checkpoint" >> "$run/matrix.log"
done
printf 'lineage-regression\n' > "$run/stage"
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn -w /repo/services/control/internal/platform/database "$name" /fixture/database.test -test.v -test.timeout=5m -test.run '^(TestLineageUpgrade|TestEveryCellOwnedTableCarriesScope|TestNewRowsTakeTheLocalCell|TestCheckRefusesAnotherCellIdentity)' > "$run/lineage.log" 2>&1
! grep -E -- '--- (SKIP|FAIL)' "$run/lineage.log"
test -z "$(git status --porcelain)"
printf 'Formal schema60 numbered scripts, runtime grants, eight restore histories and lineage cases passed\n'
