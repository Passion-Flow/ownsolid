#!/usr/bin/env bash

# OWNSOLID_OLD_MAC_EPOCH_CLOSED
printf '%s\n' 'Old Mac admission epoch is closed; await coordinated activation.' >&2
exit 78
set -euo pipefail
umask 077
lane="$1"
scratch="$2"
network_id=
database_id=
control_id=
keep_awake=
caffeinate -i -w "$$" &
keep_awake=$!
cleanup() {
  if [ -n "$keep_awake" ]; then kill "$keep_awake" 2>/dev/null || true; fi
  if [ -n "$control_id" ]; then docker rm -fv "$control_id" >/dev/null; fi
  if [ -n "$database_id" ]; then docker rm -fv "$database_id" >/dev/null; fi
  if [ -n "$network_id" ]; then docker network rm "$network_id" >/dev/null; fi
  rm -f "$scratch/pg-password" "$scratch/pg-dsn" "$scratch/person.json"
}
trap cleanup EXIT
if lsof -nP -iTCP:14850 -sTCP:LISTEN; then exit 1; fi
cd "$lane"
export GOMAXPROCS=3 GOFLAGS=-p=1 GOPROXY=off GOSUMDB=off
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -tags repository_integration -overlay "$scratch/overlay.json" -c -o "$scratch/control.test" ./services/control/cmd/server > "$scratch/compile.log" 2>&1
git rev-parse HEAD > "$scratch/control-revision"
(cd web && pnpm --filter @ownsolid/portal build > "$scratch/build.log" 2>&1)
python3 - "$scratch" <<'PY'
import secrets,sys
from pathlib import Path
root=Path(sys.argv[1])
password=secrets.token_hex(32)
(root/'pg-password').write_text(password)
(root/'pg-dsn').write_text(f'postgresql://postgres:{password}@127.0.0.1:5432/ownsolid_portal_scope_test?sslmode=disable')
(root/'schema').write_text('m20_repository_'+secrets.token_hex(16))
PY
pg_image="$(docker image inspect pgvector/pgvector:0.8.7-pg18-bookworm --format '{{.Id}}')"
go_image="$(docker image inspect golang:1.26.10-bookworm --format '{{.Id}}')"
network_id="$(docker network create --internal ownsolid-web-portal-scope)"
database_id="$(docker run -d --pull never --name ownsolid-web-portal-scope-db --network "$network_id" -p 127.0.0.1:14850:14850 -e POSTGRES_DB=ownsolid_portal_scope_test -e POSTGRES_PASSWORD_FILE=/run/fixture-password -v "$scratch/pg-password:/run/fixture-password:ro" "$pg_image")"
for step in $(seq 1 45); do
  if docker exec "$database_id" pg_isready -h 127.0.0.1 -U postgres >/dev/null 2>&1; then break; fi
  sleep 1
done
docker exec "$database_id" pg_isready -h 127.0.0.1 -U postgres
docker exec -u postgres "$database_id" psql -d ownsolid_portal_scope_test -At -c 'SHOW server_version' > "$scratch/postgres-version"
docker exec -u postgres "$database_id" psql -d ownsolid_portal_scope_test -X -v ON_ERROR_STOP=1 -c "CREATE EXTENSION vector WITH SCHEMA public VERSION '0.8.7'; REVOKE CREATE ON SCHEMA public FROM PUBLIC;" > "$scratch/pgvector.log"
control_id="$(docker run -d --pull never --name ownsolid-web-portal-scope-control --network "container:$database_id"   -e PROJECT_ONE_REPOSITORY_ENGINE=postgresql   -e PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE=/fixture/pg-dsn   -e PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA="$(cat "$scratch/schema")"   -v "$scratch:/fixture" -v "$lane/web/apps/portal/dist:/web-portal:ro"   "$go_image" /fixture/control.test -test.run '^TestTemporaryPortalWorkspaceScopeFixture$' -test.v -test.timeout 10m)"
for step in $(seq 1 60); do
  if [ -f "$scratch/spki" ]; then break; fi
  if [ "$(docker inspect --format '{{.State.Running}}' "$control_id")" != true ]; then
    docker logs "$control_id" > "$scratch/postgres.log" 2>&1
    tail -n 25 "$scratch/postgres.log"
    exit 1
  fi
  sleep 1
done
test -s "$scratch/spki"
export OWNSOLID_LIVE_ORIGIN=https://portal.example.invalid:14850
export OWNSOLID_LIVE_PORTAL_SCOPE_FILE="$scratch/person.json"
export OWNSOLID_LIVE_SPKI="$(cat "$scratch/spki")"
export OWNSOLID_LIVE_HOST_RULES='MAP portal.example.invalid 127.0.0.1'
export PLAYWRIGHT_JSON_OUTPUT_FILE="$scratch/playwright-report.json"
cd "$lane/web/apps/portal"
set +e
pnpm exec playwright test e2e/live-workspace-scope.spec.ts --workers=1 --output "$scratch/results" --reporter=line,json > "$scratch/playwright.log" 2>&1
browser_status=$?
set -e
touch "$scratch/browser-finished"
docker wait "$control_id" > "$scratch/control-exit"
docker logs "$control_id" > "$scratch/postgres.log" 2>&1
tail -n 45 "$scratch/playwright.log"
rg 'PORTAL_SCOPE_DB_PROOF' "$scratch/postgres.log"
test "$browser_status" = 0
test "$(cat "$scratch/control-exit")" = 0
