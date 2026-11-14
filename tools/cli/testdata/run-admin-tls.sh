#!/bin/sh
set -eu

repo=$(CDPATH='' cd -- "$(dirname -- "$0")/../../.." && pwd -P)
scratch=$(mktemp -d "${TMPDIR:-/tmp}/ownsolid-cli.XXXXXXXX")
logs_container=
cleanup() {
  if [ -n "$logs_container" ]; then
    docker rm -f "$logs_container" >/dev/null
  fi
  rm -r "$scratch"
}
trap cleanup EXIT
image=pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47
arch=$(docker image inspect --format '{{.Architecture}}' "$image")
cd "$repo"
GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -o "$scratch/ownsolid" ./tools/cli/cmd/ownsolid

# Go's internal package rule requires this test to run in the dispatcher package.
python3 - "$repo" "$scratch/overlay.json" <<'PY'
import json
import sys
from pathlib import Path

repo = Path(sys.argv[1])
target = repo / "services/control/internal/modules/identity/http"
sources = (repo / "tools/cli/testdata").glob("*_tls_test.go")
replacements = {str(target / ("lane_d_" + source.name)): str(source) for source in sources}
Path(sys.argv[2]).write_text(json.dumps({"Replace": replacements}), encoding="utf-8")
PY
GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go -C services/control test -c \
  -overlay "$scratch/overlay.json" -o "$scratch/admin.test" ./internal/modules/identity/http

set --
network=none
if [ -n "${LANE_D_MODEL_BUNDLE:-}" ]; then
  bundle=$(CDPATH='' cd -- "$(dirname -- "$LANE_D_MODEL_BUNDLE")" && pwd -P)/$(basename -- "$LANE_D_MODEL_BUNDLE")
  test -f "$bundle"
  set -- --mount "type=bind,src=$bundle,dst=/bundle/model.tar,readonly" --env LANE_D_MODEL_BUNDLE=/bundle/model.tar
fi
if [ -n "${LANE_D_COMPONENT_STATE:-}" ]; then
  catalog=$(CDPATH='' cd -- "$LANE_D_COMPONENT_STATE/dependencies/components/node-exporter" && pwd -P)
  logs_image=victoriametrics/victoria-logs@sha256:251121fa882af99b95ba0c230a4a2f412ea602d2698c64a96c58dc9842bb755d
  logs_container=$(docker run -d --pull never --network none --label ownsolid.lane=lane-d "$logs_image" \
    -httpListenAddr=127.0.0.1:9428 -storageDataPath=/tmp/lane-d-logs)
  network="container:$logs_container"
  set -- "$@" --mount "type=bind,src=$catalog,dst=/component-catalog,readonly" \
    --env LANE_D_COMPONENT_CATALOG=/component-catalog --env LANE_D_LOG_STORE=http://127.0.0.1:9428
fi

docker run --rm --pull never --network "$network" --label ownsolid.lane=lane-d "$@" \
  --mount "type=bind,src=$scratch,dst=/fixture,readonly" --entrypoint sh "$image" -ec '
    test "$(wc -l < /proc/net/route)" -eq 1
    for interface in /sys/class/net/*; do
      if [ "$interface" != /sys/class/net/lo ] && [ -f "$interface/flags" ]; then
        flags=$(cat "$interface/flags")
        test "$((flags & 1))" -eq 0
      fi
    done
    data=$(mktemp -d)
    chown postgres "$data"
    gosu postgres initdb -D "$data" --auth=trust --locale=C --encoding=UTF8 >/dev/null
    gosu postgres pg_ctl -D "$data" -o "-h 127.0.0.1" -w start >/dev/null
    createdb -h 127.0.0.1 -U postgres lane_d_test
    psql -h 127.0.0.1 -U postgres -d lane_d_test -v ON_ERROR_STOP=1 \
      -c "CREATE EXTENSION vector WITH SCHEMA public VERSION \$\$0.8.7\$\$; REVOKE CREATE ON SCHEMA public FROM PUBLIC" >/dev/null
    export PROJECT_ONE_TEST_DSN=postgresql://postgres@127.0.0.1/lane_d_test?sslmode=disable
    export LANE_D_CLI_BINARY=/fixture/ownsolid
    /fixture/admin.test -test.v -test.run "^TestLaneD(Admin|ModelImport|Exporter|Alias|ManagedGateway|AgentPacing)TLS$" -test.timeout=6m
  '
