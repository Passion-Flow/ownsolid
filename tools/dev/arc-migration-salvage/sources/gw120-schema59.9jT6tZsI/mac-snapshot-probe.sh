#!/usr/bin/env bash
set -euo pipefail
umask 077
work=$1
repo=$2
lock=/private/tmp/ownsolid-workstation-full-gate.lock
name=arc-gw120-snapshot-$(uuidgen | tr 'A-Z' 'a-z')
cleanup() {
 docker rm -f "$name" >/dev/null 2>&1 || true
 docker volume rm "$name" >/dev/null 2>&1 || true
 rm -f "$work/pg-password" "$work/test-dsn"
 if [[ -f "$lock/owner" && $(cat "$lock/owner") == "$work" ]]; then
  rm "$lock/owner";rmdir "$lock"
 fi
}
trap cleanup EXIT
cd "$repo"
test "$(git rev-parse HEAD)" = 62a3792f86a57fb71d3944dad6d5a75bfb27a603
test -z "$(git status --porcelain)"
image=pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47
arch=$(docker image inspect "$image" --format '{{.Architecture}}')
[[ $arch == arm64 || $arch == amd64 ]]
printf 'Mac host: %s\nPostgreSQL image architecture: %s\n' "$(uname -sm)" "$arch"
export GOOS=linux GOARCH=$arch CGO_ENABLED=0 GOPROXY=off GOSUMDB=off GOMAXPROCS=2 GOFLAGS=-p=1
python3 - "$work" "$repo" <<'PY'
from pathlib import Path
import json,sys
p=Path(sys.argv[1]);repo=Path(sys.argv[2]);probe=p.parent/'stale_snapshot_rollback_test.go'
(p/'overlay.json').write_text(json.dumps({'Replace':{str(repo/'services/control/internal/modules/distribution/stale_snapshot_rollback_test.go'):str(probe)}}))
PY
go -C services/control test -overlay "$work/overlay.json" -c -o "$work/distribution.test" ./internal/modules/distribution
od -An -N32 -tx1 /dev/urandom | tr -d ' \n' > "$work/pg-password"
printf 'postgres://postgres:%s@127.0.0.1:5432/gateway_description_test?sslmode=disable\n' "$(cat "$work/pg-password")" > "$work/test-dsn"
docker volume create "$name" >/dev/null
docker run -d --name "$name" --pull never --platform "linux/$arch" --network none --cpus 2 --memory 1g --shm-size 128m \
 --label ownsolid.test=arc-gw120-snapshot \
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
set +e
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn -w /repo/services/control/internal/modules/distribution "$name" /fixture/distribution.test -test.v -test.timeout 2m -test.run '^TestFormalAliasDescriptionRollbackRejectsStaleSnapshot$' > "$work/red.log" 2>&1
red=$?
set -e
cat "$work/red.log"
test "$red" = 1
grep -F 'stale snapshot allowed the down script to drop newly committed description state' "$work/red.log" >/dev/null
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn -w /repo/services/control/internal/modules/distribution "$name" /fixture/distribution.test -test.v -test.timeout 2m -test.run '^TestFormalAliasDescriptionRollbackIsolationPrototype$' | tee "$work/prototype.log"
if grep -F -- '--- SKIP' "$work/prototype.log";then exit 1;fi
printf 'Expected old-script failure and isolation prototype pass\n'
